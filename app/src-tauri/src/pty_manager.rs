//! Native PTY management using portable-pty.
//!
//! Replaces the Node.js pty-server with direct Rust PTY handling.
//! No Unix socket, no separate process.

use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use portable_pty::{native_pty_system, Child, CommandBuilder, MasterPty, PtySize};
use serde_json::json;
use std::collections::HashMap;
use std::io::{Read, Write};
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::thread;
use tauri::{AppHandle, Emitter, State};

/// Get the user's actual login shell from the system (macOS).
/// Falls back to None if it can't be determined.
fn get_user_login_shell() -> Option<String> {
    // Get username from environment
    let username = std::env::var("USER").ok()?;

    // On macOS, use dscl to get the login shell
    let output = Command::new("dscl")
        .args([".", "-read", &format!("/Users/{}", username), "UserShell"])
        .output()
        .ok()?;

    if output.status.success() {
        let stdout = String::from_utf8_lossy(&output.stdout);
        // Output format: "UserShell: /path/to/shell"
        stdout
            .lines()
            .find(|line| line.starts_with("UserShell:"))
            .map(|line| line.trim_start_matches("UserShell:").trim().to_string())
    } else {
        None
    }
}

/// Find the last safe boundary for both UTF-8 and ANSI escape sequences.
/// Returns the index up to which the slice contains only complete sequences.
/// The remainder (from returned index to end) should be carried over to the next read.
fn find_safe_boundary(bytes: &[u8]) -> usize {
    if bytes.is_empty() {
        return 0;
    }

    let len = bytes.len();

    // First, check for incomplete ANSI escape sequences.
    // Look for ESC (0x1B) in the last portion of the buffer.
    // ANSI sequences can be long (e.g., \x1B[38;2;255;128;64m), so check last 32 bytes.
    let search_start = len.saturating_sub(32);
    for i in (search_start..len).rev() {
        if bytes[i] == 0x1B {
            // Found ESC - check if sequence is complete
            if i + 1 >= len {
                // Just ESC at the end, incomplete
                return i;
            }

            match bytes[i + 1] {
                // CSI sequence: \x1B[ followed by params, terminated by 0x40-0x7E
                b'[' => {
                    // Look for terminating byte (letter or @[\]^_`{|}~)
                    for j in (i + 2)..len {
                        let b = bytes[j];
                        if (0x40..=0x7E).contains(&b) {
                            // Sequence is complete, continue searching for more ESC
                            break;
                        }
                        if j == len - 1 {
                            // Reached end without terminator, incomplete
                            return i;
                        }
                    }
                    if i + 2 >= len {
                        // Just \x1B[ at end, incomplete
                        return i;
                    }
                }
                // OSC sequence: \x1B] followed by text, terminated by BEL (0x07) or ST (\x1B\\)
                b']' => {
                    let mut found_terminator = false;
                    for j in (i + 2)..len {
                        if bytes[j] == 0x07 {
                            found_terminator = true;
                            break;
                        }
                        if bytes[j] == 0x1B && j + 1 < len && bytes[j + 1] == b'\\' {
                            found_terminator = true;
                            break;
                        }
                    }
                    if !found_terminator {
                        return i;
                    }
                }
                // DCS, PM, APC sequences: similar to OSC
                b'P' | b'^' | b'_' => {
                    let mut found_terminator = false;
                    for j in (i + 2)..len {
                        if bytes[j] == 0x1B && j + 1 < len && bytes[j + 1] == b'\\' {
                            found_terminator = true;
                            break;
                        }
                    }
                    if !found_terminator {
                        return i;
                    }
                }
                // Simple two-byte escape (e.g., \x1B7, \x1B8, \x1Bc)
                // These are complete if we have the second byte
                _ => {}
            }
        }
    }

    // Now check for incomplete UTF-8 sequences in the last 4 bytes
    for i in (len.saturating_sub(4)..len).rev() {
        let b = bytes[i];

        // Skip continuation bytes (10xxxxxx)
        if (b & 0b1100_0000) == 0b1000_0000 {
            continue;
        }

        // Found a start byte - determine expected sequence length
        let expected_len = if b < 0x80 {
            1 // ASCII
        } else if (b & 0b1110_0000) == 0b1100_0000 {
            2 // 110xxxxx = 2-byte sequence
        } else if (b & 0b1111_0000) == 0b1110_0000 {
            3 // 1110xxxx = 3-byte sequence
        } else if (b & 0b1111_1000) == 0b1111_0000 {
            4 // 11110xxx = 4-byte sequence
        } else {
            1 // Invalid byte, treat as single byte
        };

        let actual_len = len - i;

        if actual_len >= expected_len {
            // Sequence is complete, safe to send everything
            return len;
        } else {
            // Sequence is incomplete, cut before this start byte
            return i;
        }
    }

    len
}

/// Holds a PTY session's resources
struct PtySession {
    #[allow(dead_code)]
    master: Arc<Mutex<Box<dyn MasterPty + Send>>>,
    writer: Arc<Mutex<Box<dyn Write + Send>>>,
    child: Arc<Mutex<Box<dyn Child + Send + Sync>>>,
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

    // Get user's actual login shell (not $SHELL which may differ)
    let login_shell = get_user_login_shell()
        .unwrap_or_else(|| std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".to_string()));

    let mut cmd = if is_shell {
        // Plain shell for utility terminals
        let mut cmd = CommandBuilder::new(&login_shell);
        cmd.arg("-l");
        cmd
    } else {
        // Claude Code with hooks via attn wrapper
        let attn_path = dirs::home_dir()
            .map(|h| h.join(".local/bin/attn"))
            .filter(|p| p.exists())
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|| "attn".to_string());

        let mut cmd = CommandBuilder::new(&login_shell);
        cmd.arg("-l");
        cmd.arg("-c");
        // Use shell-agnostic env var syntax
        cmd.arg(format!("ATTN_INSIDE_APP=1 exec {attn_path}"));
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
        child: Arc::new(Mutex::new(child)),
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
        // Large buffer to naturally coalesce PTY output at OS level
        let mut buf = [0u8; 16384];
        // Buffer for incomplete UTF-8 sequences carried over between reads
        let mut utf8_carryover: Vec<u8> = Vec::with_capacity(4);

        loop {
            match reader.read(&mut buf) {
                Ok(0) => {
                    // EOF - flush any remaining carryover
                    if !utf8_carryover.is_empty() {
                        let data = BASE64.encode(&utf8_carryover);
                        let _ = app.emit(
                            "pty-event",
                            json!({
                                "event": "data",
                                "id": session_id,
                                "data": data,
                            }),
                        );
                    }
                    break;
                }
                Ok(n) => {
                    // Combine carryover with new data
                    let mut combined = std::mem::take(&mut utf8_carryover);
                    combined.extend_from_slice(&buf[..n]);

                    // Find safe boundary (UTF-8 + ANSI escape sequences)
                    let boundary = find_safe_boundary(&combined);

                    // Only emit if we have complete sequences to send
                    if boundary > 0 {
                        let data = BASE64.encode(&combined[..boundary]);
                        let _ = app.emit(
                            "pty-event",
                            json!({
                                "event": "data",
                                "id": session_id,
                                "data": data,
                            }),
                        );
                    }

                    // Carry over incomplete sequence for next read
                    if boundary < combined.len() {
                        utf8_carryover = combined[boundary..].to_vec();
                    }
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

    // Kill the child process before removing session
    if let Some(session) = sessions.get(&id) {
        if let Ok(mut child) = session.child.lock() {
            // kill() sends SIGHUP to the child process
            let _ = child.kill();
        }
    }

    sessions.remove(&id);

    Ok(())
}
