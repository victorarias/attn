mod pty_manager;

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
#[tauri::command]
fn start_daemon() -> Result<(), String> {
    use std::process::Command;
    use std::thread;
    use std::time::Duration;

    let home = dirs::home_dir().ok_or("Cannot find home directory")?;
    let bin_path = home.join(".local/bin/attn");

    if !bin_path.exists() {
        return Err(format!("attn binary not found at {:?}. Run 'make install' first.", bin_path));
    }

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
        ])
        .on_page_load(|webview, _payload| {
            // Show window as soon as page content is loaded (loading screen visible)
            let _ = webview.window().show();
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
