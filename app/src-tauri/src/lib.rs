mod pty_bridge;

use pty_bridge::PtyState;

// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
#[tauri::command]
fn greet(name: &str) -> String {
    format!("Hello, {}! You've been greeted from Rust!", name)
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
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
