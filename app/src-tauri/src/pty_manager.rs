//! Native PTY management using portable-pty.
//!
//! Replaces the Node.js pty-server with direct Rust PTY handling.
//! No Unix socket, no separate process.

use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use portable_pty::{native_pty_system, CommandBuilder, MasterPty, PtySize};
use serde_json::json;
use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::{Arc, Mutex};
use std::thread;
use tauri::{AppHandle, Emitter, State};

/// Holds a PTY session's resources
struct PtySession {
    #[allow(dead_code)]
    master: Arc<Mutex<Box<dyn MasterPty + Send>>>,
    writer: Arc<Mutex<Box<dyn Write + Send>>>,
    // Reader runs in dedicated thread, no handle needed
}

/// Global PTY state managed by Tauri
#[derive(Default)]
pub struct PtyState {
    sessions: Arc<Mutex<HashMap<String, PtySession>>>,
}

#[tauri::command]
pub async fn pty_spawn(
    state: State<'_, PtyState>,
    app: AppHandle,
    id: String,
    cwd: String,
    cols: u16,
    rows: u16,
    shell: Option<bool>,
) -> Result<u32, String> {
    let pty_system = native_pty_system();

    let pair = pty_system
        .openpty(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|e| format!("Failed to open PTY: {}", e))?;

    // Determine command to spawn
    let is_shell = shell.unwrap_or(false);
    let mut cmd = if is_shell {
        // Plain shell for utility terminals
        let shell_path = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".to_string());
        let mut cmd = CommandBuilder::new(&shell_path);
        cmd.arg("-l");
        cmd
    } else {
        // Claude Code with hooks via attn wrapper
        // Use absolute path since bundled apps have minimal PATH
        let attn_path = dirs::home_dir()
            .map(|h| h.join(".local/bin/attn"))
            .filter(|p| p.exists())
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|| "attn".to_string());

        let mut cmd = CommandBuilder::new(&attn_path);
        cmd.arg("-y");
        cmd.env("ATTN_INSIDE_APP", "1");
        cmd
    };

    cmd.cwd(&cwd);
    cmd.env("TERM", "xterm-256color");

    // Spawn the child process
    let child = pair
        .slave
        .spawn_command(cmd)
        .map_err(|e| format!("Failed to spawn: {}", e))?;

    let pid = child.process_id().unwrap_or(0);

    // Get reader and writer
    let reader = pair
        .master
        .try_clone_reader()
        .map_err(|e| format!("Failed to clone reader: {}", e))?;

    let writer = pair
        .master
        .take_writer()
        .map_err(|e| format!("Failed to take writer: {}", e))?;

    // Store session
    let session = PtySession {
        master: Arc::new(Mutex::new(pair.master)),
        writer: Arc::new(Mutex::new(writer)),
    };

    state
        .sessions
        .lock()
        .map_err(|_| "Lock poisoned")?
        .insert(id.clone(), session);

    // Spawn reader thread - streams output to frontend
    let session_id = id.clone();
    let sessions_ref = Arc::clone(&state.sessions);
    thread::spawn(move || {
        let mut reader = reader;
        let mut buf = [0u8; 4096];

        loop {
            match reader.read(&mut buf) {
                Ok(0) => break, // EOF
                Ok(n) => {
                    // Send base64-encoded data to frontend
                    let data = BASE64.encode(&buf[..n]);
                    let _ = app.emit(
                        "pty-event",
                        json!({
                            "event": "data",
                            "id": session_id,
                            "data": data,
                        }),
                    );
                }
                Err(_) => break,
            }
        }

        // Process exited - notify frontend and clean up
        let _ = app.emit(
            "pty-event",
            json!({
                "event": "exit",
                "id": session_id,
                "code": 0,
            }),
        );

        // Remove session from map
        if let Ok(mut sessions) = sessions_ref.lock() {
            sessions.remove(&session_id);
        }
    });

    Ok(pid)
}

#[tauri::command]
pub async fn pty_write(state: State<'_, PtyState>, id: String, data: String) -> Result<(), String> {
    let sessions = state.sessions.lock().map_err(|_| "Lock poisoned")?;
    let session = sessions.get(&id).ok_or("Session not found")?;

    let mut writer = session.writer.lock().map_err(|_| "Lock poisoned")?;
    writer
        .write_all(data.as_bytes())
        .map_err(|e| format!("Write failed: {}", e))?;
    writer
        .flush()
        .map_err(|e| format!("Flush failed: {}", e))?;

    Ok(())
}

#[tauri::command]
pub async fn pty_resize(
    state: State<'_, PtyState>,
    id: String,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    let sessions = state.sessions.lock().map_err(|_| "Lock poisoned")?;
    let session = sessions.get(&id).ok_or("Session not found")?;

    let master = session.master.lock().map_err(|_| "Lock poisoned")?;
    master
        .resize(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|e| format!("Resize failed: {}", e))?;

    Ok(())
}

#[tauri::command]
pub async fn pty_kill(state: State<'_, PtyState>, id: String) -> Result<(), String> {
    let mut sessions = state.sessions.lock().map_err(|_| "Lock poisoned")?;

    // Removing the session drops the master PTY, which sends SIGHUP to child
    sessions.remove(&id);

    Ok(())
}
