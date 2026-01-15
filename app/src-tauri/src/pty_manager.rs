//! Native PTY management using portable-pty.
//!
//! Replaces the Node.js pty-server with direct Rust PTY handling.
//! No Unix socket, no separate process.

use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use portable_pty::{native_pty_system, Child, CommandBuilder, MasterPty, PtySize};
#[cfg(unix)]
use nix::sys::signal::{kill, Signal};
#[cfg(unix)]
use nix::unistd::Pid;
use regex::Regex;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::collections::HashMap;
use std::fs;
use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::thread;
use tauri::{AppHandle, Emitter, State};

/// Arguments for pty_spawn command
#[derive(Debug, Deserialize, Serialize)]
pub struct PtySpawnArgs {
    pub id: String,
    pub cwd: String,
    pub cols: u16,
    pub rows: u16,
    #[serde(default)]
    pub shell: Option<bool>,
    #[serde(default)]
    pub resume_session_id: Option<String>,
    #[serde(default)]
    pub resume_picker: Option<bool>,
    #[serde(default)]
    pub fork_session: Option<bool>,
    #[serde(default)]
    pub detect_state: Option<bool>,
    #[serde(default)]
    pub agent: Option<String>,
    #[serde(default)]
    pub claude_executable: Option<String>,
    #[serde(default)]
    pub codex_executable: Option<String>,
}

/// Validate that a string is a valid UUID format.
/// Format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (lowercase hex)
fn is_valid_uuid(s: &str) -> bool {
    let uuid_regex =
        Regex::new(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$").unwrap();
    uuid_regex.is_match(s)
}

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

const STATE_WORKING: &str = "working";
const STATE_WAITING_INPUT: &str = "waiting_input";
const STATE_PENDING_APPROVAL: &str = "pending_approval";
const STATE_IDLE: &str = "idle";

#[derive(Clone, Copy)]
struct StateHeuristics {
    prompt_markers: &'static [&'static str],
    status_markers: &'static [&'static str],
    request_phrases: &'static [&'static str],
    list_request_triggers: &'static [&'static str],
}

const DEFAULT_HEURISTICS: StateHeuristics = StateHeuristics {
    prompt_markers: &[" › ", " > ", "❯ ", "» ", "❱ "],
    status_markers: &["context left", "for shortcuts"],
    request_phrases: &[
        "let me know what",
        "let me know if",
        "tell me what else",
        "tell me what to do",
        "what should i do",
        "what would you like",
        "what do you want",
        "how can i help",
        "can you",
        "could you",
        "do you want",
    ],
    list_request_triggers: &["pick one", "choose", "select", "tell me"],
};

fn parse_bool_env(var: &str) -> Option<bool> {
    let value = std::env::var(var).ok()?.to_lowercase();
    match value.as_str() {
        "1" | "true" | "yes" | "on" => Some(true),
        "0" | "false" | "no" | "off" => Some(false),
        _ => None,
    }
}

fn attn_socket_path() -> Option<PathBuf> {
    if let Ok(path) = std::env::var("ATTN_SOCKET_PATH") {
        return Some(PathBuf::from(path));
    }

    let home = dirs::home_dir()?;
    let config_path = home.join(".attn/config.json");
    if let Ok(data) = fs::read_to_string(&config_path) {
        if let Ok(value) = serde_json::from_str::<serde_json::Value>(&data) {
            if let Some(path) = value.get("socket_path").and_then(|v| v.as_str()) {
                return Some(PathBuf::from(path));
            }
        }
    }

    let default_path = home.join(".attn/attn.sock");
    if default_path.exists() {
        return Some(default_path);
    }

    let legacy_path = home.join(".attn.sock");
    if legacy_path.exists() {
        return Some(legacy_path);
    }

    Some(default_path)
}

fn send_state_update(session_id: &str, state: &str) {
    let Some(socket_path) = attn_socket_path() else {
        return;
    };

    if let Ok(mut stream) = UnixStream::connect(socket_path) {
        let msg = json!({ "cmd": "state", "id": session_id, "state": state });
        let _ = stream.write_all(msg.to_string().as_bytes());
    }
}

fn should_detect_state(args: &PtySpawnArgs) -> bool {
    if let Some(enabled) = args.detect_state {
        return enabled;
    }

    if let Some(enabled) = parse_bool_env("ATTN_PTY_STATE_DETECTION") {
        return enabled;
    }

    if let Some(agent) = args.agent.as_deref() {
        return agent.eq_ignore_ascii_case("codex");
    }

    let agent = std::env::var("ATTN_AGENT").unwrap_or_else(|_| "codex".to_string());
    agent.eq_ignore_ascii_case("codex")
}

fn normalize_agent(agent: Option<&str>) -> &'static str {
    match agent {
        Some(value) if value.eq_ignore_ascii_case("claude") => "claude",
        Some(value) if value.eq_ignore_ascii_case("codex") => "codex",
        _ => "codex",
    }
}

fn trim_to_last_chars(input: &str, max_chars: usize) -> String {
    let char_count = input.chars().count();
    if char_count <= max_chars {
        return input.to_string();
    }
    let skip = char_count - max_chars;
    if let Some((idx, _)) = input.char_indices().nth(skip) {
        input[idx..].to_string()
    } else {
        input.to_string()
    }
}

fn strip_ansi(input: &str) -> String {
    let mut out = String::with_capacity(input.len());
    let mut chars = input.chars().peekable();

    while let Some(ch) = chars.next() {
        if ch != '\u{1b}' {
            out.push(ch);
            continue;
        }

        match chars.peek() {
            Some('[') => {
                chars.next();
                while let Some(c) = chars.next() {
                    let b = c as u32;
                    if (0x40..=0x7E).contains(&b) {
                        break;
                    }
                }
            }
            Some(']') => {
                chars.next();
                loop {
                    match chars.next() {
                        Some('\u{7}') => break,
                        Some('\u{1b}') => {
                            if let Some('\\') = chars.peek() {
                                chars.next();
                                break;
                            }
                        }
                        Some(_) => continue,
                        None => break,
                    }
                }
            }
            _ => {}
        }
    }

    out
}

fn tail_lines(text: &str, max_lines: usize) -> String {
    let lines: Vec<&str> = text.lines().collect();
    let start = lines.len().saturating_sub(max_lines);
    lines[start..].join("\n")
}

fn is_pending_approval(text: &str) -> bool {
    let lower = text.to_lowercase();
    if lower.contains("would you like to run the following command") {
        return true;
    }
    let has_keyword = lower.contains("approve")
        || lower.contains("approval")
        || lower.contains("permission")
        || lower.contains("allow")
        || lower.contains("confirm")
        || lower.contains("proceed")
        || lower.contains("run this command")
        || lower.contains("execute command")
        || lower.contains("run command");
    let has_prompt = lower.contains("y/n")
        || lower.contains("[y/n")
        || lower.contains("(y/n")
        || lower.contains("[y/n]")
        || lower.contains("y or n")
        || lower.contains("yes/no")
        || lower.contains("press y")
        || lower.contains("type y")
        || lower.contains("press enter to confirm");
    let has_reason = lower.contains("reason:");
    let has_option = lower.contains("yes, proceed")
        || lower.contains("don't ask again")
        || lower.contains("dont ask again")
        || lower.contains("no, and tell");
    (has_keyword && has_prompt) || (has_reason && has_option)
}

fn is_prompt_line(line: &str) -> bool {
    let trimmed = line.trim_start();
    if trimmed.is_empty() {
        return false;
    }

    let mut chars = trimmed.chars();
    let first = chars.next().unwrap_or('\0');
    if matches!(first, '>' | '›' | '❯' | '»' | '❱') {
        return true;
    }

    false
}

fn is_assistant_line(line: &str) -> bool {
    let trimmed = line.trim_start();
    if trimmed.is_empty() {
        return false;
    }

    let mut chars = trimmed.chars();
    let first = chars.next().unwrap_or('\0');
    if !matches!(first, '•' | '·' | '●') {
        return false;
    }

    let rest = chars.as_str().trim_start().to_lowercase();
    if rest.starts_with("working")
        || rest.starts_with("thinking")
        || rest.starts_with("running")
        || rest.starts_with("executing")
    {
        return false;
    }

    true
}

fn last_assistant_text(lines: &[&str], heuristics: &StateHeuristics) -> Option<String> {
    for line in lines.iter().rev() {
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        if is_assistant_line(trimmed) {
            let mut chars = trimmed.chars();
            chars.next();
            let mut text = chars.as_str().trim().to_string();
            // Strip inline prompt/status tails (Codex often appends them to the same line).
            for marker in heuristics.prompt_markers {
                if let Some(idx) = text.find(marker) {
                    text.truncate(idx);
                }
            }
            for marker in heuristics.status_markers {
                if let Some(idx) = text.find(marker) {
                    text.truncate(idx);
                }
            }
            let text = text.trim();
            if !text.is_empty() {
                return Some(text.to_string());
            }
        }
    }
    None
}

fn has_prompt(lines: &[&str], heuristics: &StateHeuristics) -> bool {
    lines.iter().any(|line| {
        if is_prompt_line(line) {
            return true;
        }

        heuristics
            .prompt_markers
            .iter()
            .any(|marker| line.contains(marker))
    })
}

fn has_numbered_list(lines: &[&str]) -> bool {
    for line in lines {
        let trimmed = line.trim_start();
        let mut chars = trimmed.chars();
        let first = chars.next().unwrap_or('\0');
        if first.is_ascii_digit() {
            if let Some(next) = chars.next() {
                if next == '.' {
                    return true;
                }
            }
        }
    }
    false
}

fn assistant_requests_input(
    assistant_text: &str,
    full_text: &str,
    lines: &[&str],
    heuristics: &StateHeuristics,
) -> bool {
    let lower_assistant = assistant_text.to_lowercase();
    let lower_full = full_text.to_lowercase();
    if assistant_text.contains('?') {
        return true;
    }

    for phrase in heuristics.request_phrases {
        if lower_assistant.contains(phrase) {
            return true;
        }
    }

    if has_numbered_list(lines) {
        return heuristics
            .list_request_triggers
            .iter()
            .any(|phrase| lower_full.contains(phrase));
    }

    false
}

fn is_waiting_input(text: &str, heuristics: &StateHeuristics) -> bool {
    let lower = text.to_lowercase();
    if lower.contains("enter your response")
        || lower.contains("type your response")
        || lower.contains("your response:")
        || lower.contains("your reply:")
        || lower.contains("input:")
    {
        return true;
    }

    let lines: Vec<&str> = text.lines().collect();
    let non_empty: Vec<&str> = lines
        .iter()
        .map(|line| line.trim_end_matches(|c: char| c == '\r' || c == '\n'))
        .filter(|line| !line.trim().is_empty())
        .collect();

    if non_empty.is_empty() {
        return false;
    }

    let last = non_empty.last().unwrap();
    if last.ends_with("You:") || last.ends_with("User:") {
        return true;
    }

    if is_prompt_line(last) {
        if let Some(assistant_text) = last_assistant_text(&non_empty, heuristics) {
            return assistant_requests_input(&assistant_text, text, &non_empty, heuristics);
        }
        return true;
    }

    let tail = non_empty
        .iter()
        .rev()
        .take(4)
        .copied()
        .collect::<Vec<&str>>();
    let has_prompt = tail.iter().any(|line| is_prompt_line(line));
    let has_status = tail.iter().any(|line| {
        heuristics
            .status_markers
            .iter()
            .any(|marker| line.contains(marker))
    });

    if has_prompt && has_status {
        if let Some(assistant_text) = last_assistant_text(&non_empty, heuristics) {
            return assistant_requests_input(&assistant_text, text, &non_empty, heuristics);
        }
        return true;
    }

    false
}

fn classify_state(text: &str, heuristics: &StateHeuristics) -> Option<&'static str> {
    let cleaned = strip_ansi(text);
    let lines: Vec<&str> = cleaned.lines().collect();
    let prompt_shown = has_prompt(&lines, heuristics);

    if is_pending_approval(&cleaned) {
        return Some(STATE_PENDING_APPROVAL);
    }
    if is_waiting_input(&cleaned, heuristics) {
        return Some(STATE_WAITING_INPUT);
    }
    if prompt_shown {
        return Some(STATE_IDLE);
    }
    if !cleaned.trim().is_empty() {
        return Some(STATE_WORKING);
    }
    None
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
    args: PtySpawnArgs,
) -> Result<u32, String> {
    let detect_state_enabled = should_detect_state(&args);
    let PtySpawnArgs {
        id,
        cwd,
        cols,
        rows,
        shell,
        resume_session_id,
        resume_picker,
        fork_session,
        agent,
        claude_executable,
        codex_executable,
        detect_state: _,
    } = args;
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
    let detect_state_enabled = detect_state_enabled && !is_shell;

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

        // Validate resume_session_id if provided (defense-in-depth against shell injection)
        if let Some(ref resume_id) = resume_session_id {
            if !is_valid_uuid(resume_id) {
                return Err(format!("Invalid resume session ID format: {}", resume_id));
            }
        }

        // Build resume/fork flags if provided
        let fork_flags = match (
            &resume_session_id,
            resume_picker.unwrap_or(false),
            fork_session.unwrap_or(false),
        ) {
            (Some(resume_id), _, true) => format!(" --resume '{}' --fork-session", resume_id),
            (Some(resume_id), _, false) => format!(" --resume '{}'", resume_id),
            (None, true, false) => " --resume".to_string(),
            _ => String::new(),
        };

        let mut cmd = CommandBuilder::new(&login_shell);
        cmd.arg("-l");
        cmd.arg("-c");
        let resolved_agent = normalize_agent(agent.as_deref());
        cmd.env("ATTN_INSIDE_APP", "1");
        cmd.env("ATTN_SESSION_ID", id.clone());
        cmd.env("ATTN_AGENT", resolved_agent);
        if let Some(path) = claude_executable {
            if !path.is_empty() {
                cmd.env("ATTN_CLAUDE_EXECUTABLE", path);
            }
        }
        if let Some(path) = codex_executable {
            if !path.is_empty() {
                cmd.env("ATTN_CODEX_EXECUTABLE", path);
            }
        }
        // Pass session ID via env var so attn uses the same ID as frontend
        cmd.arg(format!(
            "exec {attn_path}{fork_flags}"
        ));
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
        let mut text_tail = String::new();
        let mut last_state: Option<&'static str> = None;
        const MAX_TAIL: usize = 2000;
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

                    if detect_state_enabled && boundary > 0 {
                        let chunk = String::from_utf8_lossy(&combined[..boundary]);
                        let cleaned = strip_ansi(&chunk);
                        if !cleaned.is_empty() {
                            text_tail.push_str(&cleaned);
                            if text_tail.len() > MAX_TAIL {
                                text_tail = trim_to_last_chars(&text_tail, MAX_TAIL);
                            }

                            let recent_text = tail_lines(&text_tail, 6);
                            let desired_state = classify_state(&recent_text, &DEFAULT_HEURISTICS);

                            if let Some(state) = desired_state {
                                if last_state.as_deref() != Some(state) {
                                    send_state_update(&session_id, state);
                                    last_state = Some(state);
                                }
                            }
                        }
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
    writer.flush().map_err(|e| format!("Flush failed: {}", e))?;

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
            let pid = child.process_id().unwrap_or(0);
            if pid > 0 {
                #[cfg(unix)]
                {
                    let _ = kill(Pid::from_raw(pid as i32), Signal::SIGTERM);
                }
                #[cfg(not(unix))]
                {
                    let _ = child.kill();
                }
            } else {
                let _ = child.kill();
            }
        }
    }

    sessions.remove(&id);

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn state_from_text(raw: &str) -> Option<&'static str> {
        classify_state(raw, &DEFAULT_HEURISTICS)
    }

    #[test]
    fn pending_approval_detects_codex_prompt() {
        let text = "Would you like to run the following command?\n\
Reason: make install needs to write Go build cache outside the workspace.\n\
$ make install\n\
1. Yes, proceed (y)\n\
2. Yes, and don't ask again for commands that start with 'make install' (p)\n\
3. No, and tell Codex what to do differently (esc)\n";
        assert!(is_pending_approval(text));
        assert_eq!(
            state_from_text(text),
            Some(STATE_PENDING_APPROVAL),
            "approval prompt should win precedence"
        );
    }

    #[test]
    fn waiting_input_true_for_question_with_prompt() {
        let text = "• Hi! How can I help with orbis today?\n> ";
        assert!(is_waiting_input(text, &DEFAULT_HEURISTICS));
    }

    #[test]
    fn waiting_input_false_for_wrapup_with_prompt() {
        let text = "• Cleanup done.\n\
\n\
  - Deleted the Codex test logs from today: IDs 680–685.\n\
  - Trimmed the one full-format working memory log that still had Resume/Worklog: ID 671.\n\
\n\
  Now the only session logs from today are the real ones (IDs 671, 666, 660, 651, 646, 635, 610)\n\
  and they’re all in the trimmed core-section format.\n\
\n\
  If you want me to shorten any of those further, say which IDs to trim or delete.\n\
> ";
        assert!(!is_waiting_input(text, &DEFAULT_HEURISTICS));
    }

    #[test]
    fn waiting_input_false_for_log_observation_prompt() {
        let text = "• Logged it as observation #706 (session_log, global / session-log).\n\
  If you want me to adjust or trim anything in that log, say the word.\n\
> ";
        assert!(!is_waiting_input(text, &DEFAULT_HEURISTICS));
    }

    #[test]
    fn waiting_input_true_for_numbered_list_pick_one() {
        let text = "• If you’re good with the behavior, next steps are optional:\n\
\n\
  1. Run a slightly longer Codex session (a real change) to confirm the summaries capture edits/tests/issues.\n\
  2. Decide if you want recent output to be paged or limited by max bytes (since it now prints full content).\n\
  3. If you want me to add a test for the new short-session fallback, I can do that.\n\
\n\
  Pick one, or tell me what else you want to tackle.\n\
> ";
        assert!(is_waiting_input(text, &DEFAULT_HEURISTICS));
        assert_eq!(state_from_text(text), Some(STATE_WAITING_INPUT));
    }

    #[test]
    fn idle_for_numbered_list_without_prompting_language() {
        let text = "• Next steps:\n\
\n\
  1. Run a slightly longer Codex session.\n\
  2. Decide if you want recent output to be paged.\n\
  3. Add a test for the short-session fallback.\n\
> ";
        assert!(!is_waiting_input(text, &DEFAULT_HEURISTICS));
        assert_eq!(state_from_text(text), Some(STATE_IDLE));
    }

    #[test]
    fn waiting_input_for_tell_me_what_next() {
        let text = "• Let me know what you want to tackle next.\n> ";
        assert!(is_waiting_input(text, &DEFAULT_HEURISTICS));
        assert_eq!(state_from_text(text), Some(STATE_WAITING_INPUT));
    }

    #[test]
    fn waiting_input_for_prompt_only() {
        let text = "> ";
        assert!(is_waiting_input(text, &DEFAULT_HEURISTICS));
        assert_eq!(state_from_text(text), Some(STATE_WAITING_INPUT));
    }

    #[test]
    fn idle_for_polite_wrapup_with_prompt_symbol_in_line() {
        let text = "• Hi! If you need anything later, just let me know. › Write tests for @filename 100% context left · ? for shortcuts";
        assert!(!is_waiting_input(text, &DEFAULT_HEURISTICS));
        assert_eq!(state_from_text(text), Some(STATE_IDLE));
    }

    #[test]
    fn idle_when_previous_question_in_tail_but_latest_is_wrapup() {
        let text = "• Hi! How can I help? › Summarize recent commits 100% context left · ? for shortcuts\n\
• Hi! 👋 If you need anything later, I’m here. › Summarize recent commits 100% context left · ? for shortcuts";
        assert!(!is_waiting_input(text, &DEFAULT_HEURISTICS));
        assert_eq!(state_from_text(text), Some(STATE_IDLE));
    }

    #[test]
    fn approval_detection_with_ansi_wrapping() {
        let text = "\u{1b}[1mWould you like to run the following command?\u{1b}[0m\n\
Reason: make install needs to write Go build cache outside the workspace.\n\
$ make install\n\
1. Yes, proceed (y)\n\
2. Yes, and don't ask again for commands that start with 'make install' (p)\n\
3. No, and tell Codex what to do differently (esc)\n";
        assert_eq!(
            state_from_text(text),
            Some(STATE_PENDING_APPROVAL),
            "ansi should not break approval detection"
        );
    }

    #[test]
    fn approval_detection_with_box_drawing() {
        let text = "╭────────────────────────────────────────╮\n\
│ Would you like to run the following command? │\n\
╰────────────────────────────────────────╯\n\
Reason: make install needs to write Go build cache outside the workspace.\n\
$ make install\n\
1. Yes, proceed (y)\n\
2. Yes, and don't ask again for commands that start with 'make install' (p)\n\
3. No, and tell Codex what to do differently (esc)\n";
        assert_eq!(state_from_text(text), Some(STATE_PENDING_APPROVAL));
    }

    #[test]
    fn working_status_line_is_not_assistant_text() {
        let lines = ["• Working(0s • esc to interrupt) › Improve documentation 100% context left"];
        assert!(last_assistant_text(&lines, &DEFAULT_HEURISTICS).is_none());
    }
}
