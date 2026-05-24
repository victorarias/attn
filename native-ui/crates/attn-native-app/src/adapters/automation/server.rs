use std::{
    fs::{self, OpenOptions},
    io::Write as _,
    path::Path,
    path::PathBuf,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use futures_util::{
    future::BoxFuture, AsyncBufReadExt as _, AsyncReadExt as _, AsyncWriteExt as _, StreamExt as _,
};
use serde_json::Value;
use smol::{io::BufReader, net::TcpListener};

use super::{
    manifest::{self, Manifest},
    protocol::{Request, Response},
};

pub type Dispatcher =
    Arc<dyn Fn(String, Value) -> BoxFuture<'static, Result<Value, String>> + Send + Sync>;
pub type Spawner = Arc<dyn Fn(BoxFuture<'static, ()>) + Send + Sync>;

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

pub fn bind() -> std::io::Result<TcpListener> {
    smol::block_on(TcpListener::bind("127.0.0.1:0"))
}

pub fn start(
    listener: TcpListener,
    manifest_path: PathBuf,
    dispatcher: Dispatcher,
    spawner: Spawner,
) -> std::io::Result<Handle> {
    let token = manifest::generate_token();
    let manifest = Manifest {
        enabled: true,
        port: listener.local_addr()?.port(),
        token: token.clone(),
        pid: std::process::id(),
        started_at: SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_secs().to_string())
            .unwrap_or_default(),
    };
    manifest::write(&manifest_path, &manifest)?;
    let log_path = manifest_path
        .parent()
        .unwrap_or_else(|| Path::new("."))
        .join("ui-automation-server.log");
    append_log(&log_path, "automation server started");
    let connection_spawner = spawner.clone();
    spawner(Box::pin(accept_loop(
        listener,
        token,
        dispatcher,
        connection_spawner,
        log_path,
    )));
    Ok(Handle { manifest_path })
}

async fn accept_loop(
    listener: TcpListener,
    token: String,
    dispatcher: Dispatcher,
    spawner: Spawner,
    log_path: PathBuf,
) {
    let mut incoming = listener.incoming();
    while let Some(result) = incoming.next().await {
        let Ok(stream) = result else {
            continue;
        };
        let token = token.clone();
        let dispatcher = dispatcher.clone();
        let log_path = log_path.clone();
        spawner(Box::pin(async move {
            if let Err(error) = handle_connection(stream, token, dispatcher, log_path).await {
                eprintln!("[native automation] connection failed: {error}");
            }
        }));
    }
}

async fn handle_connection(
    stream: smol::net::TcpStream,
    token: String,
    dispatcher: Dispatcher,
    log_path: PathBuf,
) -> std::io::Result<()> {
    let (reader, mut writer) = stream.split();
    let mut reader = BufReader::new(reader);
    let mut line = String::new();
    let mut request_count = 0_u64;
    loop {
        line.clear();
        if reader.read_line(&mut line).await? == 0 {
            return Ok(());
        }
        if line.trim().is_empty() {
            continue;
        }
        request_count += 1;
        let response = process_request(
            line.trim(),
            &token,
            request_count,
            dispatcher.clone(),
            Some(&log_path),
        )
        .await;
        let body = serde_json::to_string(&response).unwrap_or_else(|error| {
            format!("{{\"id\":\"invalid\",\"ok\":false,\"error\":\"{error}\"}}")
        });
        writer.write_all(body.as_bytes()).await?;
        writer.write_all(b"\n").await?;
        writer.flush().await?;
    }
}

async fn process_request(
    input: &str,
    token: &str,
    request_count: u64,
    dispatcher: Dispatcher,
    log_path: Option<&Path>,
) -> Response {
    let request: Request = match serde_json::from_str(input) {
        Ok(request) => request,
        Err(error) => {
            if let Some(path) = log_path {
                append_log(path, &format!("request invalid-json error={error}"));
            }
            return Response::err(
                format!("ui-automation-{request_count}"),
                format!("invalid request json: {error}"),
            );
        }
    };
    let id = request
        .id
        .unwrap_or_else(|| format!("ui-automation-{request_count}"));
    if let Some(path) = log_path {
        append_log(
            path,
            &format!("request start id={id} action={}", request.action),
        );
    }
    if !constant_time_eq(request.token.as_bytes(), token.as_bytes()) {
        if let Some(path) = log_path {
            append_log(
                path,
                &format!(
                    "request reject id={id} action={} invalid-token",
                    request.action
                ),
            );
        }
        return Response::err(id, "invalid token");
    }
    let action = request.action;
    match dispatcher(action.clone(), request.payload.unwrap_or(Value::Null)).await {
        Ok(result) => {
            if let Some(path) = log_path {
                append_log(
                    path,
                    &format!("request done id={id} action={action} ok=true"),
                );
            }
            Response::ok(id, result)
        }
        Err(error) => {
            if let Some(path) = log_path {
                append_log(
                    path,
                    &format!("request done id={id} action={action} ok=false error={error}"),
                );
            }
            Response::err(id, error)
        }
    }
}

fn append_log(path: &Path, message: &str) {
    if let Some(parent) = path.parent() {
        let _ = fs::create_dir_all(parent);
    }
    if let Ok(mut file) = OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(file, "{message}");
    }
}

fn constant_time_eq(left: &[u8], right: &[u8]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    left.iter()
        .zip(right.iter())
        .fold(0_u8, |different, (left, right)| different | (left ^ right))
        == 0
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn rejects_invalid_token() {
        let response = smol::block_on(process_request(
            r#"{"id":"1","token":"wrong","action":"ping"}"#,
            "expected",
            1,
            Arc::new(|_, _| Box::pin(async { Ok(json!({"pong": true})) })),
            None,
        ));
        assert!(!response.ok);
        assert_eq!(response.error.as_deref(), Some("invalid token"));
    }
}
