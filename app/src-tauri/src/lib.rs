mod pty_manager;
mod thumbs;

use pty_manager::PtyState;

// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
#[tauri::command]
fn greet(name: &str) -> String {
    format!("Hello, {}! You've been greeted from Rust!", name)
}

/// Check if the daemon is running by checking for the socket file
#[tauri::command]
fn is_daemon_running() -> bool {
    let home = match dirs::home_dir() {
        Some(h) => h,
        None => return false,
    };
    let socket_path = home.join(".attn.sock");
    socket_path.exists()
}

/// Start the daemon process
/// Checks local dev path first (~/.local/bin/attn), then falls back to bundled binary
#[tauri::command]
fn start_daemon(_app: tauri::AppHandle) -> Result<(), String> {
    use std::process::Command;
    use std::thread;
    use std::time::Duration;

    let home = dirs::home_dir().ok_or("Cannot find home directory")?;

    // 1. Check local dev path first (~/.local/bin/attn)
    let local_path = home.join(".local/bin/attn");

    // 2. Check bundled path (same directory as the app executable)
    let bundled_path = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.join("attn")));

    // Use local if exists (dev mode), otherwise bundled
    let bin_path = if local_path.exists() {
        local_path
    } else if let Some(ref bp) = bundled_path {
        if bp.exists() {
            bp.clone()
        } else {
            return Err("No daemon binary found. Run 'make install' or reinstall the app.".into());
        }
    } else {
        return Err("No daemon binary found.".into());
    };

    Command::new(&bin_path)
        .arg("daemon")
        .spawn()
        .map_err(|e| format!("Failed to start daemon: {}", e))?;

    // Wait for socket to appear (up to 2 seconds)
    let socket_path = home.join(".attn.sock");
    for _ in 0..20 {
        if socket_path.exists() {
            return Ok(());
        }
        thread::sleep(Duration::from_millis(100));
    }

    Err("Daemon did not start within 2 seconds".to_string())
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

    let entries = fs::read_dir(&dir_path)
        .map_err(|e| format!("Cannot read directory: {}", e))?;

    let prefix_lower = prefix.map(|p| p.to_lowercase());

    let mut directories: Vec<String> = entries
        .filter_map(|entry| {
            let entry = entry.ok()?;
            let metadata = entry.metadata().ok()?;
            if metadata.is_dir() {
                let name = entry.file_name().to_string_lossy().to_string();
                // Filter by prefix if provided
                if let Some(ref p) = prefix_lower {
                    if !name.to_lowercase().starts_with(p) {
                        return None;
                    }
                }
                Some(name)
            } else {
                None
            }
        })
        .collect();

    directories.sort();
    directories.truncate(50); // Limit to 50 results

    Ok(directories)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .manage(PtyState::default())
        .invoke_handler(tauri::generate_handler![
            greet,
            pty_manager::pty_spawn,
            pty_manager::pty_write,
            pty_manager::pty_resize,
            pty_manager::pty_kill,
            list_directory,
            is_daemon_running,
            start_daemon,
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
