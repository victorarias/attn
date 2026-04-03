mod thumbs;
mod ui_automation;

use std::env;
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

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

fn resolve_prefer_local(prefer_local: Option<bool>) -> bool {
    let prefer_local_env = matches!(
        env::var("ATTN_PREFER_LOCAL_DAEMON")
            .ok()
            .map(|value| value.trim().to_ascii_lowercase()),
        Some(value) if value == "1" || value == "true" || value == "yes"
    );
    let prefer_local_hint = prefer_local.unwrap_or(false);
    prefer_local_env || prefer_local_hint
}

fn resolve_daemon_binary(prefer_local: bool) -> Result<PathBuf, String> {
    let home = dirs::home_dir().ok_or("Cannot find home directory")?;
    let local_path = home.join(".local/bin/attn");
    let bundled_path = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.join("attn")));

    if prefer_local {
        if local_path.exists() {
            return Ok(local_path);
        }
        if let Some(ref path) = bundled_path {
            if path.exists() {
                return Ok(path.clone());
            }
        }
    } else {
        if let Some(ref path) = bundled_path {
            if path.exists() {
                return Ok(path.clone());
            }
        }
        if local_path.exists() {
            return Ok(local_path);
        }
    }

    Err("No daemon binary found. Run 'make install' or reinstall the app.".into())
}

fn spawn_daemon(bin_path: &Path) -> Result<(), String> {
    Command::new(bin_path)
        .env("ATTN_WRAPPER_PATH", bin_path)
        .arg("daemon")
        .spawn()
        .map_err(|e| format!("Failed to start daemon: {}", e))?;
    Ok(())
}

fn daemon_binary_protocol_version(bin_path: &Path) -> Result<String, String> {
    let empty_path = env::temp_dir().join("attn-protocol-probe-empty-path");
    let mut child = Command::new(bin_path)
        .arg("--protocol-version")
        // Older binaries may not understand this flag and fall back into the
        // wrapper path. Force the in-app direct-launch branch and strip PATH so
        // any nested agent exec fails fast instead of opening the app.
        .env("ATTN_INSIDE_APP", "1")
        .env("ATTN_DAEMON_MANAGED", "1")
        .env("ATTN_AGENT", "codex")
        .env("PATH", &empty_path)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| {
            format!(
                "Failed to query protocol version from daemon binary {}: {}",
                bin_path.display(),
                e
            )
        })?;

    let deadline = Instant::now() + Duration::from_secs(1);
    let status = loop {
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) => {
                if Instant::now() >= deadline {
                    let _ = child.kill();
                    let _ = child.wait();
                    return Err(format!(
                        "Timed out querying protocol version from daemon binary {}",
                        bin_path.display()
                    ));
                }
                thread::sleep(Duration::from_millis(25));
            }
            Err(e) => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(format!(
                    "Failed while querying protocol version from daemon binary {}: {}",
                    bin_path.display(),
                    e
                ));
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
        let stderr = stderr.trim().to_string();
        return Err(format!(
            "Daemon binary {} failed protocol preflight{}",
            bin_path.display(),
            if stderr.is_empty() {
                String::new()
            } else {
                format!(": {}", stderr)
            }
        ));
    }

    let reported = stdout.trim().to_string();
    if reported.is_empty() {
        return Err(format!(
            "Daemon binary {} returned an empty protocol version",
            bin_path.display()
        ));
    }

    Ok(reported)
}

fn wait_for_socket_state(socket_path: &Path, want_live: bool, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    loop {
        let live = daemon_is_running_at(socket_path);
        if live == want_live {
            return true;
        }
        if !want_live && !socket_path.exists() {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(100));
    }
}

const DAEMON_START_TIMEOUT: Duration = Duration::from_secs(10);

fn start_daemon_impl(prefer_local: Option<bool>) -> Result<(), String> {
    let prefer_local = resolve_prefer_local(prefer_local);
    let bin_path = resolve_daemon_binary(prefer_local)?;
    let socket_path = daemon_socket_path().ok_or("Cannot resolve daemon socket path")?;
    if !daemon_is_running_at(&socket_path) {
        let _ = std::fs::remove_file(&socket_path);
    } else {
        return Ok(());
    }

    spawn_daemon(&bin_path)?;

    if wait_for_socket_state(&socket_path, true, DAEMON_START_TIMEOUT) {
        return Ok(());
    }

    Err(format!(
        "Daemon did not start within {} seconds",
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
    let rc = unsafe { libc::kill(pid as i32, libc::SIGTERM) };
    if rc == 0 {
        return Ok(());
    }

    let err = std::io::Error::last_os_error();
    if err.raw_os_error() == Some(libc::ESRCH) {
        return Ok(());
    }

    Err(format!("Failed to stop daemon process {}: {}", pid, err))
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

/// Check if the daemon is running by validating the socket is live.
#[tauri::command]
fn is_daemon_running() -> bool {
    let socket_path = match daemon_socket_path() {
        Some(path) => path,
        None => return false,
    };
    if daemon_is_running_at(&socket_path) {
        return true;
    }

    // Clean up stale socket files so startup can recover.
    let _ = std::fs::remove_file(&socket_path);
    false
}

/// Start the daemon process
/// Uses bundled app daemon by default, with optional local override for development.
#[tauri::command]
fn start_daemon(_app: tauri::AppHandle, prefer_local: Option<bool>) -> Result<(), String> {
    start_daemon_impl(prefer_local)
}

#[tauri::command]
fn restart_daemon(
    _app: tauri::AppHandle,
    prefer_local: Option<bool>,
    expected_protocol: String,
) -> Result<(), String> {
    let prefer_local = resolve_prefer_local(prefer_local);
    let bin_path = resolve_daemon_binary(prefer_local)?;
    let reported_protocol = daemon_binary_protocol_version(&bin_path)?;
    if reported_protocol != expected_protocol.trim() {
        return Err(format!(
            "Refusing restart: resolved daemon binary {} reports protocol {}, app expects {}",
            bin_path.display(),
            reported_protocol,
            expected_protocol.trim()
        ));
    }

    let socket_path = daemon_socket_path().ok_or("Cannot resolve daemon socket path")?;
    if !daemon_is_running_at(&socket_path) {
        let _ = std::fs::remove_file(&socket_path);
        spawn_daemon(&bin_path)?;
        if wait_for_socket_state(&socket_path, true, DAEMON_START_TIMEOUT) {
            return Ok(());
        }
        return Err(format!(
            "Daemon did not start within {} seconds",
            DAEMON_START_TIMEOUT.as_secs()
        ));
    }

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
    if !wait_for_socket_state(&socket_path, false, Duration::from_secs(5)) {
        return Err("Timed out waiting for daemon to stop".into());
    }
    let _ = std::fs::remove_file(&socket_path);

    spawn_daemon(&bin_path)?;
    if wait_for_socket_state(&socket_path, true, DAEMON_START_TIMEOUT) {
        return Ok(());
    }

    Err(format!(
        "Daemon did not start within {} seconds",
        DAEMON_START_TIMEOUT.as_secs()
    ))
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
        .invoke_handler(tauri::generate_handler![
            list_directory,
            is_daemon_running,
            start_daemon,
            restart_daemon,
            open_in_editor,
            thumbs::extract_patterns,
            thumbs::reveal_in_finder,
        ])
        .setup(|app| {
            ui_automation::maybe_start(&app.handle().clone());
            Ok(())
        })
        .on_page_load(|webview, _payload| {
            // Show window as soon as page content is loaded (loading screen visible)
            let _ = webview.window().show();
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
