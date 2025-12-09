use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, State};

#[derive(Default)]
pub struct PtyState {
    stream: Mutex<Option<UnixStream>>,
}

fn socket_path() -> Result<PathBuf, String> {
    dirs::home_dir()
        .ok_or_else(|| "Could not determine home directory".to_string())
        .map(|home| home.join(".cm-pty.sock"))
}

fn write_frame(stream: &mut UnixStream, data: &serde_json::Value) -> std::io::Result<()> {
    let json = serde_json::to_vec(data)?;
    let len = (json.len() as u32).to_be_bytes();
    stream.write_all(&len)?;
    stream.write_all(&json)?;
    stream.flush()?;
    Ok(())
}

fn read_frame(stream: &mut UnixStream) -> std::io::Result<serde_json::Value> {
    let mut len_buf = [0u8; 4];
    stream.read_exact(&mut len_buf)?;
    let len = u32::from_be_bytes(len_buf) as usize;

    let mut json_buf = vec![0u8; len];
    stream.read_exact(&mut json_buf)?;

    serde_json::from_slice(&json_buf).map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))
}

#[tauri::command]
pub async fn pty_connect(state: State<'_, PtyState>, app: AppHandle) -> Result<(), String> {
    let path = socket_path()?;
    let stream = UnixStream::connect(&path).map_err(|e| format!("Connect failed: {}", e))?;
    stream.set_nonblocking(false).map_err(|e| format!("Set blocking failed: {}", e))?;

    // Start reader thread - ONE thread for ALL PTY sessions
    let stream_clone = stream.try_clone().map_err(|e| e.to_string())?;
    let app_clone = app.clone();

    std::thread::spawn(move || {
        let mut stream = stream_clone;
        loop {
            match read_frame(&mut stream) {
                Ok(msg) => {
                    let _ = app_clone.emit("pty-event", msg);
                }
                Err(_) => break,
            }
        }
    });

    *state.stream.lock().map_err(|_| "Mutex poisoned".to_string())? = Some(stream);
    Ok(())
}

#[tauri::command]
pub async fn pty_spawn(
    state: State<'_, PtyState>,
    id: String,
    cwd: String,
    cols: u32,
    rows: u32,
) -> Result<(), String> {
    let mut guard = state.stream.lock().map_err(|_| "Mutex poisoned".to_string())?;
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "spawn",
        "id": id,
        "cwd": cwd,
        "cols": cols,
        "rows": rows,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())?;

    Ok(())
}

#[tauri::command]
pub async fn pty_write(state: State<'_, PtyState>, id: String, data: String) -> Result<(), String> {
    let mut guard = state.stream.lock().map_err(|_| "Mutex poisoned".to_string())?;
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "write",
        "id": id,
        "data": data,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn pty_resize(state: State<'_, PtyState>, id: String, cols: u32, rows: u32) -> Result<(), String> {
    let mut guard = state.stream.lock().map_err(|_| "Mutex poisoned".to_string())?;
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "resize",
        "id": id,
        "cols": cols,
        "rows": rows,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn pty_kill(state: State<'_, PtyState>, id: String) -> Result<(), String> {
    let mut guard = state.stream.lock().map_err(|_| "Mutex poisoned".to_string())?;
    let stream = guard.as_mut().ok_or("Not connected")?;

    let msg = serde_json::json!({
        "cmd": "kill",
        "id": id,
    });

    write_frame(stream, &msg).map_err(|e| e.to_string())
}
