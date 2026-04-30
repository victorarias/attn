/// TCP server that accepts newline-delimited JSON requests from external
/// test scripts. Mirrors the Tauri bridge's wire format so the existing
/// `uiAutomationClient.mjs` works against either app.
///
/// Lifecycle: `start` binds a random local port, writes the manifest, and
/// spawns the accept loop. The returned `Handle` deletes the manifest on
/// drop. The accept loop itself stays alive as long as the GPUI app does;
/// `Handle::shutdown` would be the place to add an explicit signal if we
/// ever need it.
use std::path::PathBuf;
use std::pin::Pin;
use std::sync::Arc;

use futures_util::future::BoxFuture;
use futures_util::AsyncBufReadExt;
use futures_util::AsyncReadExt;
use futures_util::AsyncWriteExt;
use serde_json::Value;
use smol::io::BufReader;
use smol::net::TcpListener;
use smol::stream::StreamExt;

use super::manifest::{self, Manifest};
use super::protocol::{Request, Response};

/// Action dispatch surface. The server hands the action name + payload to
/// this function and awaits the resulting `Value` (or `String` error). The
/// real implementation in `actions.rs` runs handlers on the GPUI main
/// thread; tests can plug in a synchronous stub.
pub type Dispatcher = Arc<
    dyn Fn(String, Value) -> BoxFuture<'static, Result<Value, String>> + Send + Sync + 'static,
>;

/// How the server spawns its background tasks. Decouples the wire layer
/// from a specific executor so production code can use GPUI's
/// `BackgroundExecutor` while tests use a `smol::Executor`. Tasks must be
/// `Send` because they will be polled on whatever thread the executor
/// happens to run them on.
pub type Spawner = Arc<dyn Fn(BoxFuture<'static, ()>) + Send + Sync + 'static>;

/// Build a `Spawner` from a `smol::Executor`. Convenience for tests.
#[cfg(test)]
pub fn spawner_from_smol(executor: Arc<smol::Executor<'static>>) -> Spawner {
    Arc::new(move |fut| {
        executor.spawn(fut).detach();
    })
}

/// Resources the server owns. Drop deletes the manifest so external
/// observers can't see a stale port/token after the app exits.
pub struct Handle {
    manifest_path: PathBuf,
}

impl Handle {
    pub fn manifest_path(&self) -> &std::path::Path {
        &self.manifest_path
    }
}

impl Drop for Handle {
    fn drop(&mut self) {
        let _ = manifest::delete(&self.manifest_path);
    }
}

/// Bind a localhost TCP listener on a random port. Pulled out so callers
/// can inspect the bound address before the server is started — useful
/// in tests and to avoid time-of-check races between `bind` and the
/// manifest write.
pub fn bind() -> std::io::Result<TcpListener> {
    smol::block_on(async { TcpListener::bind("127.0.0.1:0").await })
}

/// Start the server with an already-bound listener. Returns immediately;
/// the accept loop is spawned via the provided `Spawner` so the wire
/// layer doesn't depend on which executor (smol, GPUI background) is in
/// use.
pub fn start(
    listener: TcpListener,
    manifest_path: PathBuf,
    dispatcher: Dispatcher,
    spawner: Spawner,
) -> std::io::Result<Handle> {
    let port = listener.local_addr()?.port();
    let token = manifest::generate_token();

    let manifest_value = Manifest {
        enabled: true,
        port,
        token: token.clone(),
        pid: std::process::id(),
        started_at: started_at_unix_secs(),
    };
    manifest::write(&manifest_path, &manifest_value)?;

    let spawner_for_loop = spawner.clone();
    spawner(Box::pin(accept_loop(
        listener,
        token,
        dispatcher,
        spawner_for_loop,
    )));

    Ok(Handle { manifest_path })
}

fn started_at_unix_secs() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or_default();
    secs.to_string()
}

async fn accept_loop(
    listener: TcpListener,
    expected_token: String,
    dispatcher: Dispatcher,
    spawner: Spawner,
) {
    let mut incoming = listener.incoming();
    while let Some(stream) = incoming.next().await {
        let stream = match stream {
            Ok(s) => s,
            Err(error) => {
                eprintln!("[automation] accept failed: {error}");
                continue;
            }
        };
        let token = expected_token.clone();
        let dispatcher = dispatcher.clone();
        spawner(Box::pin(async move {
            if let Err(error) = handle_connection(stream, token, dispatcher).await {
                eprintln!("[automation] connection ended: {error}");
            }
        }));
    }
}

async fn handle_connection(
    stream: smol::net::TcpStream,
    expected_token: String,
    dispatcher: Dispatcher,
) -> std::io::Result<()> {
    let (read_half, mut write_half) = stream.split();
    let mut reader = BufReader::new(read_half);
    let mut request_counter: u64 = 0;
    let mut line = String::new();
    loop {
        line.clear();
        let bytes = reader.read_line(&mut line).await?;
        if bytes == 0 {
            // Peer closed.
            return Ok(());
        }
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        request_counter += 1;
        let response = process_request(
            trimmed,
            &expected_token,
            request_counter,
            dispatcher.clone(),
        )
        .await;

        let body = match serde_json::to_string(&response) {
            Ok(s) => s,
            Err(error) => format!(
                "{{\"id\":\"{}\",\"ok\":false,\"error\":\"failed to encode response: {}\"}}",
                response.id, error
            ),
        };
        write_half.write_all(body.as_bytes()).await?;
        write_half.write_all(b"\n").await?;
        write_half.flush().await?;
    }
}

/// Pulled out for unit-testing without a real socket. Takes the raw line
/// (already trimmed) plus the expected token; returns a `Response`.
async fn process_request(
    line: &str,
    expected_token: &str,
    request_counter: u64,
    dispatcher: Dispatcher,
) -> Response {
    let request: Request = match serde_json::from_str(line) {
        Ok(r) => r,
        Err(error) => {
            // Best-effort: pull `id` out of the malformed payload so the
            // client can still correlate the error to its request. A client
            // that sent `id: 7` (number) instead of `"7"` would otherwise
            // get an error tagged with a synthetic id, never resolve its
            // pending Promise, and hang silently.
            let echoed_id = serde_json::from_str::<Value>(line)
                .ok()
                .and_then(|v| match v.get("id")? {
                    Value::String(s) => Some(s.clone()),
                    Value::Number(n) => Some(n.to_string()),
                    Value::Bool(b) => Some(b.to_string()),
                    _ => None,
                })
                .unwrap_or_else(|| format!("ui-automation-{request_counter}"));
            return Response::err(echoed_id, format!("invalid request json: {error}"));
        }
    };
    let id = request
        .id
        .clone()
        .unwrap_or_else(|| format!("ui-automation-{request_counter}"));

    if !constant_time_eq(request.token.as_bytes(), expected_token.as_bytes()) {
        return Response::err(id, "invalid token");
    }

    let payload = request.payload.unwrap_or(Value::Null);
    match dispatcher(request.action, payload).await {
        Ok(result) => Response::ok(id, result),
        Err(error) => Response::err(id, error),
    }
}

/// Constant-time byte comparison so token validation doesn't leak length
/// information to a slow attacker. Local-only sockets soften the threat
/// model, but the cost of doing this right is two lines.
fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let mut diff = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

/// Convenience for tests/local code that just want a boxed-future closure
/// from a sync function.
#[allow(dead_code)]
pub fn dispatcher_from_sync<F>(f: F) -> Dispatcher
where
    F: Fn(String, Value) -> Result<Value, String> + Send + Sync + 'static,
{
    let f = Arc::new(f);
    Arc::new(move |action, payload| {
        let f = f.clone();
        Box::pin(async move { f(action, payload) }) as Pin<Box<_>>
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn echo_dispatcher() -> Dispatcher {
        dispatcher_from_sync(|action, payload| {
            Ok(json!({"action": action, "payload": payload}))
        })
    }

    #[test]
    fn rejects_invalid_token() {
        let response = smol::block_on(process_request(
            r#"{"id":"a","token":"bad","action":"ping"}"#,
            "good",
            1,
            echo_dispatcher(),
        ));
        assert!(!response.ok);
        assert_eq!(response.error.as_deref(), Some("invalid token"));
        assert_eq!(response.id, "a");
    }

    #[test]
    fn rejects_invalid_json() {
        let response = smol::block_on(process_request(
            "not json",
            "good",
            42,
            echo_dispatcher(),
        ));
        assert!(!response.ok);
        // Truly unparseable input has no recoverable id, so we fall back
        // to the synthetic counter.
        assert_eq!(response.id, "ui-automation-42");
        assert!(response
            .error
            .as_deref()
            .unwrap_or("")
            .starts_with("invalid request json"));
    }

    #[test]
    fn echoes_id_when_type_mismatched() {
        // Numeric id — schema wants string. We still echo it back so the
        // client can correlate; without this it would hang waiting for a
        // response keyed by "1" while the error went to a synthetic id.
        let response = smol::block_on(process_request(
            r#"{"id":1,"token":"good","action":"ping"}"#,
            "good",
            99,
            echo_dispatcher(),
        ));
        assert!(!response.ok);
        assert_eq!(response.id, "1");
        assert!(response
            .error
            .as_deref()
            .unwrap_or("")
            .starts_with("invalid request json"));
    }

    #[test]
    fn assigns_id_when_missing() {
        let response = smol::block_on(process_request(
            r#"{"token":"good","action":"ping"}"#,
            "good",
            7,
            echo_dispatcher(),
        ));
        assert!(response.ok);
        assert_eq!(response.id, "ui-automation-7");
    }

    #[test]
    fn dispatcher_runs_for_valid_request() {
        let response = smol::block_on(process_request(
            r#"{"id":"x","token":"good","action":"ping","payload":{"hello":"world"}}"#,
            "good",
            1,
            echo_dispatcher(),
        ));
        assert!(response.ok);
        assert_eq!(response.id, "x");
        assert_eq!(
            response.result.unwrap(),
            json!({"action":"ping","payload":{"hello":"world"}})
        );
    }

    #[test]
    fn end_to_end_through_real_socket() {
        // Bind, start, connect, exchange one request, shut down.
        let listener = bind().expect("bind");
        let addr = listener.local_addr().unwrap();
        let dir = std::env::temp_dir().join(format!(
            "attn-automation-server-test-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos(),
        ));
        let manifest_path = dir.join("ui-automation.json");

        let executor = Arc::new(smol::Executor::new());
        let runner = executor.clone();
        // Drive the executor on a worker thread so the spawned tasks
        // actually run while the test thread does its synchronous TCP
        // dance.
        let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let stop_inner = stop.clone();
        let runner_thread = std::thread::spawn(move || {
            smol::block_on(async {
                while !stop_inner.load(std::sync::atomic::Ordering::Relaxed) {
                    runner.tick().await;
                }
            });
        });

        let _handle = start(
            listener,
            manifest_path.clone(),
            echo_dispatcher(),
            spawner_from_smol(executor.clone()),
        )
        .expect("start");

        // Read the manifest, fish out the token.
        let body = std::fs::read_to_string(&manifest_path).expect("manifest");
        let parsed: serde_json::Value = serde_json::from_str(&body).unwrap();
        let token = parsed["token"].as_str().unwrap().to_string();
        assert_eq!(parsed["port"].as_u64().unwrap(), addr.port() as u64);

        // Send one request synchronously over a blocking TCP socket.
        use std::io::{BufRead, BufReader as StdBufReader, Write};
        let mut stream = std::net::TcpStream::connect(addr).expect("connect");
        let request = format!(
            r#"{{"id":"abc","token":"{token}","action":"ping","payload":{{"k":"v"}}}}"#
        );
        stream.write_all(request.as_bytes()).unwrap();
        stream.write_all(b"\n").unwrap();
        stream.flush().unwrap();
        let mut reader = StdBufReader::new(stream);
        let mut response_line = String::new();
        reader.read_line(&mut response_line).unwrap();
        let response: serde_json::Value = serde_json::from_str(response_line.trim()).unwrap();
        assert_eq!(response["ok"], json!(true));
        assert_eq!(response["id"], json!("abc"));
        assert_eq!(
            response["result"],
            json!({"action":"ping","payload":{"k":"v"}})
        );

        stop.store(true, std::sync::atomic::Ordering::Relaxed);
        // Force the executor to wake up so the thread observes the stop
        // flag — schedule one trivial task.
        executor.spawn(async {}).detach();
        let _ = runner_thread.join();
        let _ = std::fs::remove_dir_all(&dir);
    }
}
