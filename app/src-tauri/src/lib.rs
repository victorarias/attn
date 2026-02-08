mod thumbs;

use std::env;
use std::path::{Path, PathBuf};
use std::process::Command;

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
fn start_daemon(_app: tauri::AppHandle) -> Result<(), String> {
    use std::thread;
    use std::time::Duration;

    let home = dirs::home_dir().ok_or("Cannot find home directory")?;
    let prefer_local = matches!(
        env::var("ATTN_PREFER_LOCAL_DAEMON")
            .ok()
            .map(|value| value.trim().to_ascii_lowercase()),
        Some(value) if value == "1" || value == "true" || value == "yes"
    );

    // 1. Local dev daemon (~/.local/bin/attn)
    let local_path = home.join(".local/bin/attn");

    // 2. Bundled path (same directory as the app executable)
    let bundled_path = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.join("attn")));

    // Default behavior: prefer bundled daemon so cask installs are independent of PATH/local bins.
    // Dev override: set ATTN_PREFER_LOCAL_DAEMON=1 to prefer ~/.local/bin/attn.
    let bin_path = if prefer_local {
        if local_path.exists() {
            local_path
        } else if let Some(ref bp) = bundled_path {
            if bp.exists() {
                bp.clone()
            } else {
                return Err("No daemon binary found. Run 'make install' or reinstall the app.".into());
            }
        } else {
            return Err("No daemon binary found.".into());
        }
    } else {
        if let Some(ref bp) = bundled_path {
            if bp.exists() {
                bp.clone()
            } else if local_path.exists() {
                local_path
            } else {
                return Err("No daemon binary found. Run 'make install' or reinstall the app.".into());
            }
        } else if local_path.exists() {
            local_path
        } else {
            return Err("No daemon binary found.".into());
        }
    };

    let socket_path = daemon_socket_path().ok_or("Cannot resolve daemon socket path")?;
    if !daemon_is_running_at(&socket_path) {
        let _ = std::fs::remove_file(&socket_path);
    } else {
        return Ok(());
    }

    Command::new(&bin_path)
        .env("ATTN_WRAPPER_PATH", &bin_path)
        .arg("daemon")
        .spawn()
        .map_err(|e| format!("Failed to start daemon: {}", e))?;

    // Wait for live socket (up to 3 seconds)
    for _ in 0..30 {
        if daemon_is_running_at(&socket_path) {
            return Ok(());
        }
        thread::sleep(Duration::from_millis(100));
    }

    Err("Daemon did not start within 3 seconds".to_string())
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

#[tauri::command]
fn open_in_editor(
    cwd: String,
    file_path: Option<String>,
    editor: Option<String>,
) -> Result<(), String> {
    let editor = editor
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
        .or_else(|| env::var("EDITOR").ok())
        .or_else(|| env::var("VISUAL").ok())
        .ok_or_else(|| "EDITOR (or VISUAL) is not set".to_string())?;

    let cwd_path = PathBuf::from(&cwd);
    if !cwd_path.exists() {
        return Err(format!("Directory does not exist: {}", cwd));
    }

    let mut args: Vec<String> = Vec::new();
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

    command
        .current_dir(&cwd_path)
        .spawn()
        .map_err(|e| format!("Failed to open editor: {}", e))?;

    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .invoke_handler(tauri::generate_handler![
            list_directory,
            is_daemon_running,
            start_daemon,
            open_in_editor,
            thumbs::extract_patterns,
            thumbs::reveal_in_finder,
        ])
        .on_page_load(|webview, _payload| {
            // Show window as soon as page content is loaded (loading screen visible)
            let _ = webview.window().show();
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
