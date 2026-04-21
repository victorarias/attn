mod thumbs;
mod ui_automation;

use std::env;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::{Duration, Instant};

static ENSURE_DAEMON_LOCK: Mutex<()> = Mutex::new(());

fn daemon_socket_path() -> Option<PathBuf> {
    if let Ok(path) = env::var("ATTN_SOCKET_PATH") {
        let trimmed = path.trim();
        if !trimmed.is_empty() {
            return Some(PathBuf::from(trimmed));
        }
    }

    let home = dirs::home_dir()?;
    Some(home.join(".attn").join("attn.sock"))
}

fn daemon_pid_path() -> Option<PathBuf> {
    let socket_path = daemon_socket_path()?;
    Some(socket_path.parent()?.join("attn.pid"))
}

#[cfg(unix)]
fn socket_is_live(path: &Path) -> bool {
    use std::os::unix::net::UnixStream;
    UnixStream::connect(path).is_ok()
}

#[cfg(not(unix))]
fn socket_is_live(path: &Path) -> bool {
    path.exists()
}

fn daemon_is_running_at(socket_path: &Path) -> bool {
    if !socket_path.exists() {
        return false;
    }
    socket_is_live(socket_path)
}

fn daemon_http_is_live(timeout: Duration) -> bool {
    let addr = SocketAddr::from(([127, 0, 0, 1], daemon_http_port()));
    TcpStream::connect_timeout(&addr, timeout).is_ok()
}

#[derive(Debug, serde::Deserialize, Default)]
struct DaemonHealth {
    #[serde(default)]
    status: String,
}

fn resolve_daemon_binary() -> Result<PathBuf, String> {
    if let Ok(path) = env::var("ATTN_DAEMON_BINARY") {
        let trimmed = path.trim();
        if !trimmed.is_empty() {
            return Ok(PathBuf::from(trimmed));
        }
    }
    let bundled_path = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.join("attn")));
    if let Some(ref path) = bundled_path {
        if path.exists() {
            return Ok(path.clone());
        }
    }
    Err("No bundled daemon binary found. Reinstall attn.app.".into())
}

fn spawn_daemon(bin_path: &Path) -> Result<(), String> {
    Command::new(bin_path)
        .env("ATTN_WRAPPER_PATH", bin_path)
        .arg("daemon")
        .spawn()
        .map_err(|e| format!("Failed to start daemon: {}", e))?;
    Ok(())
}

fn run_daemon_ensure(bin_path: &Path) -> Result<String, String> {
    let mut child = Command::new(bin_path)
        .arg("daemon")
        .arg("ensure")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| {
            format!(
                "Failed to run daemon ensure with {}: {}",
                bin_path.display(),
                e
            )
        })?;

    let deadline = Instant::now() + DAEMON_ENSURE_TIMEOUT;
    let status = loop {
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) => {
                if Instant::now() >= deadline {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(format!(
                        "Daemon ensure did not finish within {} seconds",
                        DAEMON_ENSURE_TIMEOUT.as_secs()
                    ));
                }
                thread::sleep(Duration::from_millis(50));
            }
            Err(e) => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(format!("Failed while waiting for daemon ensure: {}", e));
            }
        }
    };

    let mut stdout = String::new();
    let mut stderr = String::new();
    if let Some(mut pipe) = child.stdout.take() {
        let _ = pipe.read_to_string(&mut stdout);
    }
    if let Some(mut pipe) = child.stderr.take() {
        let _ = pipe.read_to_string(&mut stderr);
    }

    if !status.success() {
        let stderr = stderr.trim();
        return Err(if stderr.is_empty() {
            "daemon ensure failed".to_string()
        } else {
            format!("daemon ensure failed: {}", stderr)
        });
    }

    Ok(stdout)
}

fn daemon_http_port() -> u16 {
    env::var("ATTN_WS_PORT")
        .ok()
        .and_then(|value| value.trim().parse::<u16>().ok())
        .filter(|port| *port > 0)
        .unwrap_or(9849)
}

fn fetch_daemon_health(timeout: Duration) -> Result<DaemonHealth, String> {
    let addr = SocketAddr::from(([127, 0, 0, 1], daemon_http_port()));
    let mut stream = TcpStream::connect_timeout(&addr, timeout)
        .map_err(|e| format!("connect /health: {}", e))?;
    stream
        .set_read_timeout(Some(timeout))
        .map_err(|e| format!("set /health read timeout: {}", e))?;
    stream
        .set_write_timeout(Some(timeout))
        .map_err(|e| format!("set /health write timeout: {}", e))?;
    stream
        .write_all(b"GET /health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
        .map_err(|e| format!("write /health request: {}", e))?;
    stream
        .flush()
        .map_err(|e| format!("flush /health request: {}", e))?;

    let mut response = Vec::new();
    stream
        .read_to_end(&mut response)
        .map_err(|e| format!("read /health response: {}", e))?;

    let split = response
        .windows(4)
        .position(|window| window == b"\r\n\r\n")
        .ok_or_else(|| "invalid /health response".to_string())?;
    let body = &response[split + 4..];
    serde_json::from_slice(body).map_err(|e| format!("decode /health response: {}", e))
}

fn wait_for_daemon_shutdown(socket_path: &Path, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    loop {
        if !daemon_is_running_at(socket_path) && !daemon_http_is_live(Duration::from_millis(250)) {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(100));
    }
}

const DAEMON_START_TIMEOUT: Duration = Duration::from_secs(10);
const DAEMON_ENSURE_TIMEOUT: Duration = Duration::from_secs(20);

fn wait_for_daemon_health(socket_path: &Path, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    loop {
        if daemon_is_running_at(socket_path) && daemon_http_is_live(Duration::from_millis(250)) {
            if let Ok(health) = fetch_daemon_health(Duration::from_millis(500)) {
                if health.status.trim() == "ok" {
                    return true;
                }
            }
        }
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(100));
    }
}

fn stop_running_daemon(socket_path: &Path) -> Result<(), String> {
    let pid_path = daemon_pid_path().ok_or("Cannot resolve daemon pid path")?;
    let pid = read_daemon_pid(&pid_path)?;
    let self_pid = std::process::id();
    if pid == self_pid || parent_process_id() == Some(pid) {
        return Err(format!(
            "Refusing to stop daemon pid {} because it matches the current app process tree",
            pid
        ));
    }

    terminate_process(pid)?;
    if !wait_for_daemon_shutdown(socket_path, Duration::from_secs(5)) {
        return Err("Timed out waiting for daemon to stop".into());
    }
    let _ = std::fs::remove_file(&socket_path);
    Ok(())
}

#[cfg(unix)]
fn listening_pids_for_daemon_port() -> Vec<u32> {
    let port = daemon_http_port();
    let output = Command::new("lsof")
        .arg("-ti")
        .arg(format!("tcp:{port}"))
        .arg("-sTCP:LISTEN")
        .output();

    let Ok(output) = output else {
        return Vec::new();
    };
    if !output.status.success() && output.stdout.is_empty() {
        return Vec::new();
    }

    String::from_utf8_lossy(&output.stdout)
        .lines()
        .filter_map(|line| line.trim().parse::<u32>().ok())
        .collect()
}

#[cfg(not(unix))]
fn listening_pids_for_daemon_port() -> Vec<u32> {
    Vec::new()
}

#[cfg(unix)]
fn command_for_pid(pid: u32) -> Option<String> {
    let output = Command::new("ps")
        .arg("-p")
        .arg(pid.to_string())
        .arg("-o")
        .arg("command=")
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    let command = String::from_utf8_lossy(&output.stdout).trim().to_string();
    if command.is_empty() {
        return None;
    }
    Some(command)
}

#[cfg(not(unix))]
fn command_for_pid(_pid: u32) -> Option<String> {
    None
}

fn looks_like_attn_daemon_command(command: &str) -> bool {
    let mut parts = command.split_whitespace();
    let Some(program) = parts.next() else {
        return false;
    };
    if !parts.any(|part| part == "daemon") {
        return false;
    }

    let program_name = Path::new(program)
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or(program)
        .to_ascii_lowercase();
    program_name.contains("attn")
}

fn force_stop_running_daemon(socket_path: &Path) -> Result<(), String> {
    let self_pid = std::process::id();
    let parent_pid = parent_process_id();
    let mut pids = listening_pids_for_daemon_port();
    pids.sort_unstable();
    pids.dedup();
    pids.retain(|pid| *pid != 0 && *pid != self_pid && Some(*pid) != parent_pid);

    let mut attn_daemon_pids = Vec::new();
    let mut other_listeners = Vec::new();
    for pid in pids {
        match command_for_pid(pid) {
            Some(command) if looks_like_attn_daemon_command(&command) => {
                attn_daemon_pids.push(pid);
            }
            Some(command) => other_listeners.push(format!("{pid}:{command}")),
            None => other_listeners.push(format!("{pid}:<unknown>")),
        }
    }

    if attn_daemon_pids.is_empty() {
        if other_listeners.is_empty() {
            return Err(format!(
                "No attn daemon listener found on port {} for temporary fallback recovery",
                daemon_http_port()
            ));
        }
        return Err(format!(
            "Refusing temporary fallback recovery because port {} is owned by non-attn listener(s): {}",
            daemon_http_port(),
            other_listeners.join(", ")
        ));
    }

    for pid in &attn_daemon_pids {
        let _ = terminate_process(*pid);
    }

    if wait_for_daemon_shutdown(socket_path, Duration::from_secs(3)) {
        return Ok(());
    }

    #[cfg(unix)]
    {
        for pid in &attn_daemon_pids {
            let _ = kill_process(*pid, libc::SIGKILL);
        }
    }

    if wait_for_daemon_shutdown(socket_path, Duration::from_secs(2)) {
        return Ok(());
    }

    Err(format!(
        "Timed out waiting for daemon listener to stop on port {}",
        daemon_http_port()
    ))
}

fn temporary_force_daemon_recovery(bin_path: &Path) -> Result<(), String> {
    let socket_path = daemon_socket_path().ok_or("Cannot resolve daemon socket path")?;
    if daemon_is_running_at(&socket_path) {
        if stop_running_daemon(&socket_path).is_err() {
            force_stop_running_daemon(&socket_path)?;
        }
    } else if daemon_http_is_live(Duration::from_millis(250)) {
        force_stop_running_daemon(&socket_path)?;
    }

    let pid_path = daemon_pid_path().ok_or("Cannot resolve daemon pid path")?;
    let _ = std::fs::remove_file(&socket_path);
    let _ = std::fs::remove_file(&pid_path);

    spawn_daemon(bin_path)?;
    if wait_for_daemon_health(&socket_path, DAEMON_START_TIMEOUT) {
        return Ok(());
    }
    Err(format!(
        "Daemon did not become healthy within {} seconds",
        DAEMON_START_TIMEOUT.as_secs()
    ))
}

fn read_daemon_pid(pid_path: &Path) -> Result<u32, String> {
    let data = std::fs::read_to_string(pid_path).map_err(|e| {
        format!(
            "Failed to read daemon pid file {}: {}",
            pid_path.display(),
            e
        )
    })?;
    let pid = data.trim().parse::<u32>().map_err(|e| {
        format!(
            "Failed to parse daemon pid file {}: {}",
            pid_path.display(),
            e
        )
    })?;
    if pid == 0 {
        return Err(format!("Invalid daemon pid in {}", pid_path.display()));
    }
    Ok(pid)
}

#[cfg(unix)]
fn terminate_process(pid: u32) -> Result<(), String> {
    kill_process(pid, libc::SIGTERM)
}

#[cfg(unix)]
fn kill_process(pid: u32, signal: libc::c_int) -> Result<(), String> {
    let rc = unsafe { libc::kill(pid as i32, signal) };
    if rc == 0 {
        return Ok(());
    }

    let err = std::io::Error::last_os_error();
    if err.raw_os_error() == Some(libc::ESRCH) {
        return Ok(());
    }

    Err(format!(
        "Failed to send signal {} to daemon process {}: {}",
        signal, pid, err
    ))
}

#[cfg(not(unix))]
fn terminate_process(_pid: u32) -> Result<(), String> {
    Err("Daemon restart is only supported on unix targets".into())
}

#[cfg(unix)]
fn parent_process_id() -> Option<u32> {
    Some(unsafe { libc::getppid() as u32 })
}

#[cfg(not(unix))]
fn parent_process_id() -> Option<u32> {
    None
}

#[tauri::command]
fn ensure_daemon(_app: tauri::AppHandle) -> Result<(), String> {
    let _guard = ENSURE_DAEMON_LOCK
        .lock()
        .map_err(|_| "Failed to acquire daemon ensure lock".to_string())?;
    let bin_path = resolve_daemon_binary()?;
    match run_daemon_ensure(&bin_path) {
        Ok(_) => Ok(()),
        Err(err) => {
            // Temporary fallback for older or unaccounted-for local states.
            eprintln!("[Daemon] daemon ensure failed: {err}; entering temporary fallback recovery");
            temporary_force_daemon_recovery(&bin_path)
                .map(|_| {
                    eprintln!("[Daemon] temporary fallback recovery completed successfully");
                })
                .map_err(|fallback_err| {
                    format!(
                        "daemon ensure failed ({err}); temporary fallback also failed: {fallback_err}"
                    )
                })
        }
    }
}

#[tauri::command]
async fn list_directory(path: String, prefix: Option<String>) -> Result<Vec<String>, String> {
    use std::fs;
    use std::path::Path;

    let dir_path = if let Some(suffix) = path.strip_prefix("~/") {
        let home = dirs::home_dir().ok_or("Cannot get home directory")?;
        home.join(suffix)
    } else if path == "~" {
        dirs::home_dir().ok_or("Cannot get home directory")?
    } else {
        Path::new(&path).to_path_buf()
    };

    let entries = fs::read_dir(&dir_path).map_err(|e| format!("Cannot read directory: {}", e))?;

    let prefix_lower = prefix.map(|p| p.to_lowercase());

    let mut directories: Vec<String> = entries
        .filter_map(|entry| {
            let entry = entry.ok()?;
            let metadata = entry.metadata().ok()?;
            if metadata.is_dir() {
                let name = entry.file_name().to_string_lossy().to_string();
                // Filter by search term (contains match)
                if let Some(ref p) = prefix_lower {
                    if !name.to_lowercase().contains(p) {
                        return None;
                    }
                }
                Some(name)
            } else {
                None
            }
        })
        .collect();

    // Sort: starts_with matches first, then contains-only, alphabetically within each group
    if let Some(ref p) = prefix_lower {
        directories.sort_by(|a, b| {
            let a_lower = a.to_lowercase();
            let b_lower = b.to_lowercase();
            let a_starts = a_lower.starts_with(p);
            let b_starts = b_lower.starts_with(p);
            match (a_starts, b_starts) {
                (true, false) => std::cmp::Ordering::Less,
                (false, true) => std::cmp::Ordering::Greater,
                _ => a.cmp(b),
            }
        });
    } else {
        directories.sort();
    }
    directories.truncate(50); // Limit to 50 results

    Ok(directories)
}

fn shell_escape_unix(arg: &str) -> String {
    if arg.is_empty() {
        return "''".to_string();
    }
    let escaped = arg.replace('\'', "'\\''");
    format!("'{}'", escaped)
}

fn shell_escape_windows(arg: &str) -> String {
    if arg.is_empty() {
        return "\"\"".to_string();
    }
    format!("\"{}\"", arg.replace('"', "\\\""))
}

fn percent_encode_path(path: &str) -> String {
    let mut encoded = String::with_capacity(path.len());
    for byte in path.bytes() {
        let ch = byte as char;
        let is_unreserved = ch.is_ascii_alphanumeric() || matches!(ch, '-' | '_' | '.' | '~' | '/');
        if is_unreserved {
            encoded.push(ch);
        } else {
            encoded.push_str(&format!("%{:02X}", byte));
        }
    }
    encoded
}

fn looks_like_zed_editor(editor: &str) -> bool {
    editor.to_ascii_lowercase().contains("zed")
}

fn build_remote_zed_target(remote_target: &str, cwd: &str, file_path: Option<&str>) -> String {
    let resolved = if let Some(path) = file_path.filter(|value| !value.trim().is_empty()) {
        let path_buf = Path::new(path);
        if path_buf.is_absolute() {
            path_buf.to_path_buf()
        } else {
            Path::new(cwd).join(path_buf)
        }
    } else {
        PathBuf::from(cwd)
    };
    let normalized = resolved.to_string_lossy().replace('\\', "/");
    let with_leading = if normalized.starts_with('/') {
        normalized
    } else {
        format!("/{}", normalized)
    };
    format!(
        "ssh://{}{}",
        remote_target.trim(),
        percent_encode_path(&with_leading)
    )
}

#[tauri::command]
fn open_in_editor(
    cwd: String,
    file_path: Option<String>,
    editor: Option<String>,
    remote_target: Option<String>,
) -> Result<(), String> {
    let editor = editor
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
        .or_else(|| env::var("EDITOR").ok())
        .or_else(|| env::var("VISUAL").ok())
        .ok_or_else(|| "EDITOR (or VISUAL) is not set".to_string())?;

    let mut local_cwd: Option<PathBuf> = None;
    let mut args: Vec<String> = Vec::new();
    if let Some(remote_target) = remote_target
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
    {
        if !looks_like_zed_editor(&editor) {
            return Err("Remote open-in-editor currently requires Zed.".to_string());
        }
        args.push(build_remote_zed_target(
            &remote_target,
            &cwd,
            file_path.as_deref(),
        ));
    } else {
        let cwd_path = PathBuf::from(&cwd);
        if !cwd_path.exists() {
            return Err(format!("Directory does not exist: {}", cwd));
        }
        local_cwd = Some(cwd_path.clone());

        if let Some(path) = file_path {
            let path_buf = Path::new(&path);
            let resolved = if path_buf.is_absolute() {
                path_buf.to_path_buf()
            } else {
                cwd_path.join(path_buf)
            };
            args.push(resolved.to_string_lossy().to_string());
        } else {
            args.push(".".to_string());
        }
    }

    let command_line = if cfg!(windows) {
        let mut cmd = editor.clone();
        for arg in &args {
            cmd.push(' ');
            cmd.push_str(&shell_escape_windows(arg));
        }
        cmd
    } else {
        let mut cmd = editor.clone();
        for arg in &args {
            cmd.push(' ');
            cmd.push_str(&shell_escape_unix(arg));
        }
        cmd
    };

    let mut command = if cfg!(windows) {
        let mut cmd = Command::new("cmd");
        cmd.arg("/C").arg(command_line);
        cmd
    } else {
        let mut cmd = Command::new("sh");
        cmd.arg("-lc").arg(command_line);
        cmd
    };

    if let Some(cwd_path) = local_cwd {
        command.current_dir(cwd_path);
    }

    command
        .spawn()
        .map_err(|e| format!("Failed to open editor: {}", e))?;

    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Disable macOS "press and hold for accents" popup so that holding
    // a key in the terminal produces key repeat instead.
    #[cfg(target_os = "macos")]
    {
        let _ = Command::new("defaults")
            .args([
                "write",
                "com.attn.manager",
                "ApplePressAndHoldEnabled",
                "-bool",
                "false",
            ])
            .output();
    }

    tauri::Builder::default()
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .invoke_handler(tauri::generate_handler![
            list_directory,
            ensure_daemon,
            open_in_editor,
            thumbs::extract_patterns,
            thumbs::reveal_in_finder,
        ])
        .setup(|app| {
            use tauri::Manager;
            ui_automation::maybe_start(&app.handle().clone());
            // Harness-only: keep attn visible (and unthrottled by WKWebView occlusion)
            // without ever becoming the active app, so scenarios don't steal focus.
            // Accessory policy hides the Dock tile and prevents macOS from making
            // attn frontmost on launch; set_focusable(false) ensures the window
            // can't take key via a stray click either.
            #[cfg(target_os = "macos")]
            if env::var("ATTN_HARNESS_ALWAYS_ON_TOP")
                .ok()
                .is_some_and(|v| v == "1")
            {
                app.set_activation_policy(tauri::ActivationPolicy::Accessory);
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.set_always_on_top(true);
                    let _ = window.set_focusable(false);
                }
            }
            Ok(())
        })
        .on_page_load(|webview, _payload| {
            // Show window as soon as page content is loaded (loading screen visible)
            let _ = webview.window().show();
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
