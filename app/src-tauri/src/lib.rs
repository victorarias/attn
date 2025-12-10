mod pty_bridge;

use pty_bridge::PtyState;

// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
#[tauri::command]
fn greet(name: &str) -> String {
    format!("Hello, {}! You've been greeted from Rust!", name)
}

#[tauri::command]
async fn list_directory(path: String) -> Result<Vec<String>, String> {
    use std::fs;
    use std::path::Path;

    let dir_path = if path.starts_with('~') {
        let home = dirs::home_dir().ok_or("Cannot get home directory")?;
        home.join(&path[2..]) // Skip "~/"
    } else {
        Path::new(&path).to_path_buf()
    };

    let entries = fs::read_dir(&dir_path)
        .map_err(|e| format!("Cannot read directory: {}", e))?;

    let mut directories: Vec<String> = entries
        .filter_map(|entry| {
            let entry = entry.ok()?;
            let metadata = entry.metadata().ok()?;
            if metadata.is_dir() {
                Some(entry.file_name().to_string_lossy().to_string())
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
            pty_bridge::pty_connect,
            pty_bridge::pty_spawn,
            pty_bridge::pty_write,
            pty_bridge::pty_resize,
            pty_bridge::pty_kill,
            list_directory,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
