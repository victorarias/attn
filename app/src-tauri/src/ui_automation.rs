use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::net::{Shutdown, TcpListener, TcpStream};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tauri::{AppHandle, Emitter, Listener, Manager, Runtime};

pub const UI_AUTOMATION_ENABLED: bool = option_env!("ATTN_UI_AUTOMATION").is_some();

const REQUEST_EVENT: &str = "attn://ui-automation/request";
const RESPONSE_EVENT: &str = "attn://ui-automation/response";
const READY_EVENT: &str = "attn://ui-automation/ready";
// The perf harness can intentionally keep the frontend busy for tens of seconds.
const DEFAULT_TIMEOUT_MS: u64 = 120_000;
const MANIFEST_RELATIVE_PATH: &str = "debug/ui-automation.json";
const LOG_RELATIVE_PATH: &str = "debug/ui-automation-server.log";

#[derive(Clone)]
struct PendingAutomationResponses {
    by_request_id: Arc<Mutex<HashMap<String, mpsc::Sender<BridgeResponse>>>>,
}

impl PendingAutomationResponses {
    fn new() -> Self {
        Self {
            by_request_id: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    fn insert(&self, request_id: String, sender: mpsc::Sender<BridgeResponse>) {
        self.by_request_id
            .lock()
            .expect("pending automation responses lock poisoned")
            .insert(request_id, sender);
    }

    fn resolve(&self, response: BridgeResponse) {
        if let Some(sender) = self
            .by_request_id
            .lock()
            .expect("pending automation responses lock poisoned")
            .remove(&response.request_id)
        {
            let _ = sender.send(response);
        }
    }

    fn remove(&self, request_id: &str) {
        self.by_request_id
            .lock()
            .expect("pending automation responses lock poisoned")
            .remove(request_id);
    }
}

#[derive(Debug, Serialize)]
struct AutomationManifest {
    enabled: bool,
    port: u16,
    token: String,
    pid: u32,
    started_at: String,
}

#[derive(Debug, Deserialize)]
struct AutomationSocketRequest {
    #[serde(default)]
    id: Option<String>,
    token: String,
    action: String,
    #[serde(default)]
    payload: Option<Value>,
}

#[derive(Debug, Serialize)]
struct AutomationSocketResponse {
    id: String,
    ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct BridgeRequest {
    request_id: String,
    action: String,
    payload: Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct BridgeResponse {
    request_id: String,
    ok: bool,
    #[serde(default)]
    result: Option<Value>,
    #[serde(default)]
    error: Option<String>,
}

fn generate_token() -> String {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    format!("{:x}{:x}", std::process::id(), now)
}

fn manifest_path<R: Runtime>(app: &AppHandle<R>) -> Option<PathBuf> {
    app.path()
        .app_local_data_dir()
        .ok()
        .map(|dir| dir.join(MANIFEST_RELATIVE_PATH))
}

fn write_manifest<R: Runtime>(app: &AppHandle<R>, manifest: &AutomationManifest) {
    let Some(path) = manifest_path(app) else {
        eprintln!("[UIAutomation] Failed to resolve app local data dir for manifest");
        return;
    };

    if let Some(parent) = path.parent() {
        if let Err(error) = fs::create_dir_all(parent) {
            eprintln!("[UIAutomation] Failed to create manifest dir: {error}");
            return;
        }
    }

    match serde_json::to_string_pretty(manifest) {
        Ok(contents) => {
            if let Err(error) = fs::write(&path, format!("{contents}\n")) {
                eprintln!(
                    "[UIAutomation] Failed to write manifest {}: {error}",
                    path.display()
                );
            }
        }
        Err(error) => {
            eprintln!("[UIAutomation] Failed to encode manifest: {error}");
        }
    }
}

fn log_path<R: Runtime>(app: &AppHandle<R>) -> Option<PathBuf> {
    app.path()
        .app_local_data_dir()
        .ok()
        .map(|dir| dir.join(LOG_RELATIVE_PATH))
}

fn append_log<R: Runtime>(app: &AppHandle<R>, message: &str) {
    let Some(path) = log_path(app) else {
        eprintln!("[UIAutomation] {message}");
        return;
    };

    if let Some(parent) = path.parent() {
        if let Err(error) = fs::create_dir_all(parent) {
            eprintln!("[UIAutomation] Failed to create log dir: {error}");
            return;
        }
    }

    let timestamp = chrono_like_now();
    match OpenOptions::new().create(true).append(true).open(&path) {
        Ok(mut file) => {
            let _ = writeln!(file, "[{timestamp}] {message}");
        }
        Err(error) => {
            eprintln!(
                "[UIAutomation] Failed to open log {}: {error}",
                path.display()
            );
        }
    }
}

fn next_request_id(counter: &AtomicU64) -> String {
    format!("ui-automation-{}", counter.fetch_add(1, Ordering::Relaxed))
}

fn handle_request<R: Runtime>(
    app: &AppHandle<R>,
    pending: &PendingAutomationResponses,
    request_counter: &AtomicU64,
    expected_token: &str,
    frontend_ready: &AtomicBool,
    request: AutomationSocketRequest,
) -> AutomationSocketResponse {
    let request_id = request
        .id
        .unwrap_or_else(|| next_request_id(request_counter));
    let started_at = SystemTime::now();
    append_log(
        app,
        &format!(
            "request start id={} action={} frontend_ready={}",
            request_id,
            request.action,
            frontend_ready.load(Ordering::Relaxed)
        ),
    );

    if request.token != expected_token {
        append_log(
            app,
            &format!(
                "request reject id={} action={} invalid-token",
                request_id, request.action
            ),
        );
        return AutomationSocketResponse {
            id: request_id,
            ok: false,
            result: None,
            error: Some("invalid token".into()),
        };
    }

    if request.action == "ping" {
        append_log(app, &format!("request ok id={} action=ping", request_id));
        return AutomationSocketResponse {
            id: request_id,
            ok: true,
            result: Some(serde_json::json!({
                "pong": true,
                "frontendReady": frontend_ready.load(Ordering::Relaxed),
            })),
            error: None,
        };
    }

    if request.action == "get_window_bounds" {
        return match window_bounds(app) {
            Ok(result) => {
                append_log(
                    app,
                    &format!("request ok id={} action=get_window_bounds", request_id),
                );
                AutomationSocketResponse {
                    id: request_id,
                    ok: true,
                    result: Some(result),
                    error: None,
                }
            }
            Err(error) => {
                append_log(
                    app,
                    &format!(
                        "request err id={} action=get_window_bounds error={}",
                        request_id, error
                    ),
                );
                AutomationSocketResponse {
                    id: request_id,
                    ok: false,
                    result: None,
                    error: Some(error),
                }
            }
        };
    }

    if request.action == "capture_screenshot" {
        return match capture_screenshot(app, request.payload.as_ref()) {
            Ok(result) => {
                append_log(
                    app,
                    &format!("request ok id={} action=capture_screenshot", request_id),
                );
                AutomationSocketResponse {
                    id: request_id,
                    ok: true,
                    result: Some(result),
                    error: None,
                }
            }
            Err(error) => {
                append_log(
                    app,
                    &format!(
                        "request err id={} action=capture_screenshot error={}",
                        request_id, error
                    ),
                );
                AutomationSocketResponse {
                    id: request_id,
                    ok: false,
                    result: None,
                    error: Some(error),
                }
            }
        };
    }

    let payload = request.payload.unwrap_or(Value::Null);
    let bridge_request = BridgeRequest {
        request_id: request_id.clone(),
        action: request.action,
        payload,
    };

    let (sender, receiver) = mpsc::channel();
    pending.insert(request_id.clone(), sender);

    if let Err(error) = app.emit(REQUEST_EVENT, &bridge_request) {
        pending.remove(&request_id);
        append_log(
            app,
            &format!(
                "request err id={} action={} emit={}",
                request_id, bridge_request.action, error
            ),
        );
        return AutomationSocketResponse {
            id: request_id,
            ok: false,
            result: None,
            error: Some(format!("failed to emit automation request: {error}")),
        };
    }

    match receiver.recv_timeout(Duration::from_millis(DEFAULT_TIMEOUT_MS)) {
        Ok(response) => {
            let elapsed_ms = started_at.elapsed().unwrap_or_default().as_millis();
            append_log(
                app,
                &format!(
                    "request done id={} action={} ok={} elapsed_ms={}",
                    response.request_id, bridge_request.action, response.ok, elapsed_ms
                ),
            );
            AutomationSocketResponse {
                id: response.request_id,
                ok: response.ok,
                result: response.result,
                error: response.error,
            }
        }
        Err(_) => {
            pending.remove(&request_id);
            let elapsed_ms = started_at.elapsed().unwrap_or_default().as_millis();
            append_log(
                app,
                &format!(
                    "request timeout id={} action={} elapsed_ms={}",
                    request_id, bridge_request.action, elapsed_ms
                ),
            );
            AutomationSocketResponse {
                id: request_id,
                ok: false,
                result: None,
                error: Some("frontend automation request timed out".into()),
            }
        }
    }
}

fn serve_connection<R: Runtime>(
    mut stream: TcpStream,
    app: AppHandle<R>,
    pending: PendingAutomationResponses,
    request_counter: Arc<AtomicU64>,
    expected_token: String,
    frontend_ready: Arc<AtomicBool>,
) {
    let reader_stream = match stream.try_clone() {
        Ok(clone) => clone,
        Err(error) => {
            eprintln!("[UIAutomation] Failed to clone TCP stream: {error}");
            return;
        }
    };
    let mut reader = BufReader::new(reader_stream);

    loop {
        let mut line = String::new();
        match reader.read_line(&mut line) {
            Ok(0) => break,
            Ok(_) => {
                let trimmed = line.trim();
                if trimmed.is_empty() {
                    continue;
                }

                let response = match serde_json::from_str::<AutomationSocketRequest>(trimmed) {
                    Ok(request) => handle_request(
                        &app,
                        &pending,
                        request_counter.as_ref(),
                        &expected_token,
                        frontend_ready.as_ref(),
                        request,
                    ),
                    Err(error) => AutomationSocketResponse {
                        id: "invalid".into(),
                        ok: false,
                        result: None,
                        error: Some(format!("invalid request json: {error}")),
                    },
                };

                match serde_json::to_string(&response) {
                    Ok(json) => {
                        if writeln!(stream, "{json}").is_err() {
                            break;
                        }
                        if stream.flush().is_err() {
                            break;
                        }
                    }
                    Err(error) => {
                        let fallback = format!(
                            "{{\"id\":\"{}\",\"ok\":false,\"error\":\"failed to encode response: {}\"}}",
                            response.id, error
                        );
                        let _ = writeln!(stream, "{fallback}");
                        let _ = stream.flush();
                    }
                }
            }
            Err(error) => {
                eprintln!("[UIAutomation] Failed to read TCP request: {error}");
                break;
            }
        }
    }

    let _ = stream.shutdown(Shutdown::Both);
}

pub fn maybe_start<R: Runtime>(app: &AppHandle<R>) {
    if !UI_AUTOMATION_ENABLED {
        return;
    }

    let listener = match TcpListener::bind(("127.0.0.1", 0)) {
        Ok(listener) => listener,
        Err(error) => {
            eprintln!("[UIAutomation] Failed to bind localhost automation server: {error}");
            return;
        }
    };

    let port = match listener.local_addr() {
        Ok(address) => address.port(),
        Err(error) => {
            eprintln!("[UIAutomation] Failed to read automation server address: {error}");
            return;
        }
    };

    let token = generate_token();
    if let Some(path) = log_path(app) {
        if let Some(parent) = path.parent() {
            let _ = fs::create_dir_all(parent);
        }
        let _ = fs::write(&path, "");
    }
    let manifest = AutomationManifest {
        enabled: true,
        port,
        token: token.clone(),
        pid: std::process::id(),
        started_at: chrono_like_now(),
    };
    write_manifest(app, &manifest);
    append_log(
        app,
        &format!(
            "server start pid={} port={} enabled={}",
            std::process::id(),
            port,
            UI_AUTOMATION_ENABLED
        ),
    );

    let pending = PendingAutomationResponses::new();
    let frontend_ready = Arc::new(AtomicBool::new(false));
    let pending_for_events = pending.clone();
    let _event_id = app.listen_any(RESPONSE_EVENT, move |event| {
        match serde_json::from_str::<BridgeResponse>(event.payload()) {
            Ok(response) => pending_for_events.resolve(response),
            Err(error) => eprintln!("[UIAutomation] Failed to parse frontend response: {error}"),
        }
    });
    let ready_state = frontend_ready.clone();
    let app_for_ready = app.clone();
    let _ready_event_id = app.listen_any(READY_EVENT, move |_event| {
        ready_state.store(true, Ordering::Relaxed);
        append_log(&app_for_ready, "frontend ready");
    });

    let app_handle = app.clone();
    let request_counter = Arc::new(AtomicU64::new(1));

    thread::spawn(move || {
        for stream in listener.incoming() {
            match stream {
                Ok(stream) => {
                    let app = app_handle.clone();
                    let pending = pending.clone();
                    let request_counter = request_counter.clone();
                    let token = token.clone();
                    let frontend_ready = frontend_ready.clone();
                    thread::spawn(move || {
                        serve_connection(
                            stream,
                            app,
                            pending,
                            request_counter,
                            token,
                            frontend_ready,
                        );
                    });
                }
                Err(error) => {
                    eprintln!("[UIAutomation] Failed to accept automation connection: {error}");
                }
            }
        }
    });
}

fn chrono_like_now() -> String {
    let seconds = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    format!("{seconds}")
}

fn window_bounds<R: Runtime>(app: &AppHandle<R>) -> Result<Value, String> {
    let window = app
        .get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?;
    let position = window
        .outer_position()
        .map_err(|error| format!("failed to get outer position: {error}"))?;
    let size = window
        .outer_size()
        .map_err(|error| format!("failed to get outer size: {error}"))?;
    Ok(serde_json::json!({
        "x": position.x,
        "y": position.y,
        "width": size.width,
        "height": size.height,
    }))
}

fn default_screenshot_path<R: Runtime>(app: &AppHandle<R>) -> Result<PathBuf, String> {
    let base = app
        .path()
        .app_local_data_dir()
        .map_err(|error| format!("failed to resolve app local data dir: {error}"))?;
    let dir = base.join("debug").join("ui-automation-screenshots");
    fs::create_dir_all(&dir)
        .map_err(|error| format!("failed to create screenshot dir: {error}"))?;
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    Ok(dir.join(format!("screenshot-{timestamp}.png")))
}

fn capture_screenshot<R: Runtime>(
    app: &AppHandle<R>,
    payload: Option<&Value>,
) -> Result<Value, String> {
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        let _ = payload;
        return Err("capture_screenshot is currently implemented only on macOS".into());
    }

    #[cfg(target_os = "macos")]
    {
        let bounds = window_bounds(app)?;
        let x = bounds
            .get("x")
            .and_then(Value::as_i64)
            .ok_or_else(|| "invalid x bound".to_string())?;
        let y = bounds
            .get("y")
            .and_then(Value::as_i64)
            .ok_or_else(|| "invalid y bound".to_string())?;
        let width = bounds
            .get("width")
            .and_then(Value::as_u64)
            .ok_or_else(|| "invalid width bound".to_string())?;
        let height = bounds
            .get("height")
            .and_then(Value::as_u64)
            .ok_or_else(|| "invalid height bound".to_string())?;

        let output_path = match payload
            .and_then(|value| value.get("path"))
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|value| !value.is_empty())
        {
            Some(path) => PathBuf::from(path),
            None => default_screenshot_path(app)?,
        };

        if let Some(parent) = output_path.parent() {
            fs::create_dir_all(parent)
                .map_err(|error| format!("failed to create screenshot parent dir: {error}"))?;
        }

        let region = format!("{x},{y},{width},{height}");
        let status = std::process::Command::new("/usr/sbin/screencapture")
            .args(["-x", "-R", &region, output_path.to_string_lossy().as_ref()])
            .status()
            .map_err(|error| format!("failed to run screencapture: {error}"))?;

        if !status.success() {
            return Err(format!("screencapture exited with status {status}"));
        }

        Ok(serde_json::json!({
            "path": output_path.to_string_lossy().to_string(),
            "bounds": bounds,
        }))
    }
}
