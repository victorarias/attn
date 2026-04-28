/// GPUI-bound action handlers. The wire-protocol server runs on the GPUI
/// background executor and can't access entities directly (`AsyncApp` is
/// !Send across some boundaries, and entity access has to happen on the
/// foreground thread). We bridge with an async channel: the dispatcher
/// sends an `ActionRequest`, a foreground-spawned `pump_actions` task
/// reads each request, runs the handler with `&mut AsyncApp`, and sends
/// the result back.
use std::sync::Arc;

use async_channel::{unbounded, Receiver, Sender};
use attn_protocol::{PtyInputMessage, UnregisterWorkspaceMessage};
use gpui::{prelude::*, AnyView, App, AsyncApp, Entity, Keystroke, Modifiers, SharedString, WeakEntity, Window};
use serde_json::{json, Value};

use crate::app::NativeApp;
use crate::panel::PanelContent;
use crate::terminal_model::TerminalModel;
use crate::terminal_view::TerminalView;
use crate::viewport::pf;

use super::events;
use super::server::Dispatcher;

pub struct ActionRequest {
    pub action: String,
    pub payload: Value,
    pub reply: Sender<Result<Value, String>>,
}

/// Build a `(Dispatcher, Receiver)` pair. The `Dispatcher` is what the
/// TCP server uses to fulfill requests; the `Receiver` is consumed by
/// `pump_actions` on the foreground.
pub fn make_dispatcher() -> (Dispatcher, Receiver<ActionRequest>) {
    let (tx, rx) = unbounded::<ActionRequest>();
    let tx = Arc::new(tx);
    let dispatcher: Dispatcher = Arc::new(move |action, payload| {
        let tx = tx.clone();
        Box::pin(async move {
            let (reply_tx, reply_rx) = async_channel::bounded(1);
            tx.send(ActionRequest {
                action,
                payload,
                reply: reply_tx,
            })
            .await
            .map_err(|e| format!("dispatcher: action queue closed: {e}"))?;
            reply_rx
                .recv()
                .await
                .map_err(|e| format!("dispatcher: reply channel closed: {e}"))?
        })
    });
    (dispatcher, rx)
}

/// Foreground-side pump. Spawn via `cx.spawn` so it has access to
/// `&mut AsyncApp` across awaits. Returns when the channel is closed
/// (i.e. when the dispatcher is dropped on app shutdown).
pub async fn pump_actions(
    rx: Receiver<ActionRequest>,
    app: WeakEntity<NativeApp>,
    mut cx: AsyncApp,
) {
    while let Ok(req) = rx.recv().await {
        let result = handle_action(&req.action, req.payload, &app, &mut cx).await;
        let _ = req.reply.send(result).await;
    }
}

async fn handle_action(
    action: &str,
    payload: Value,
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
) -> Result<Value, String> {
    match action {
        "ping" => Ok(json!({
            "pong": true,
            "pid": std::process::id(),
        })),
        "get_state" => get_state(app, cx),
        "list_sessions" => list_sessions(app, cx),
        "get_window_geometry" => get_window_geometry(cx),
        "select_workspace" => select_workspace(app, cx, payload),
        "move_panel" => move_panel(app, cx, payload),
        "send_pty_input" => send_pty_input(app, cx, payload),
        "type_into_panel" => type_into_panel(app, cx, payload),
        "read_pane_text" => read_pane_text(app, cx, payload),
        "tail_events" => tail_events(payload),
        "set_zoom" => set_zoom(app, cx, payload),
        "create_workspace" => create_workspace(app, cx, payload),
        "destroy_workspace" => destroy_workspace(app, cx, payload),
        _ => Err(format!("unknown action: {action}")),
    }
}

fn get_state(app: &WeakEntity<NativeApp>, cx: &mut AsyncApp) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&entity, |app: &NativeApp, cx: &App| app.automation_snapshot(cx))
        .map_err(|e| format!("read entity: {e}"))
}

fn list_sessions(app: &WeakEntity<NativeApp>, cx: &mut AsyncApp) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&entity, |app: &NativeApp, cx: &App| {
        let sessions = app.daemon().read(cx).sessions();
        serde_json::to_value(sessions).unwrap_or(Value::Null)
    })
    .map_err(|e| format!("read entity: {e}"))
}

fn select_workspace(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let id = payload
        .get("id")
        .and_then(Value::as_str)
        .ok_or("payload.id (string) is required")?
        .to_string();
    let entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.update_entity(&entity, |app, cx| {
        if app.workspace(&id).is_none() {
            return Err(format!("unknown workspace id: {id}"));
        }
        app.select_workspace(SharedString::from(id.clone()), cx);
        Ok(json!({ "selected_workspace_id": id }))
    })
    .map_err(|e| format!("update entity: {e}"))?
}

fn move_panel(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id = payload
        .get("workspace_id")
        .and_then(Value::as_str)
        .ok_or("payload.workspace_id (string) is required")?
        .to_string();
    let panel_id = payload
        .get("panel_id")
        .and_then(Value::as_u64)
        .ok_or("payload.panel_id (number) is required")? as usize;
    let world_x = payload.get("world_x").and_then(Value::as_f64).map(|n| n as f32);
    let world_y = payload.get("world_y").and_then(Value::as_f64).map(|n| n as f32);
    let width = payload.get("width").and_then(Value::as_f64).map(|n| n as f32);
    let height = payload.get("height").and_then(Value::as_f64).map(|n| n as f32);

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let workspace = cx
        .read_entity(&app_entity, |app: &NativeApp, _cx: &App| {
            app.workspace(&workspace_id)
        })
        .map_err(|e| format!("read entity: {e}"))?
        .ok_or_else(|| format!("unknown workspace id: {workspace_id}"))?;

    cx.update_entity(&workspace, |ws, cx| {
        ws.update_panel(panel_id, world_x, world_y, width, height, cx)
            .map(|panel| json!({ "panel": panel }))
            .ok_or_else(|| format!("unknown panel id: {panel_id}"))
    })
    .map_err(|e| format!("update entity: {e}"))?
}

fn send_pty_input(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .to_string();
    let text = payload
        .get("text")
        .and_then(Value::as_str)
        .ok_or("payload.text (string) is required")?
        .to_string();

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&app_entity, |app: &NativeApp, cx: &App| {
        if find_terminal_model(app, &session_id, cx).is_none() {
            return Err(format!("no terminal panel for session: {session_id}"));
        }
        // The daemon routes by session id; we just need to send the
        // message. Passing through TerminalModel would also work but
        // it's strictly equivalent and adds an indirection.
        app.daemon()
            .read(cx)
            .send_cmd(&PtyInputMessage::new(session_id.clone(), text.clone()))?;
        Ok(json!({
            "session_id": session_id,
            "bytes_sent": text.len(),
        }))
    })
    .map_err(|e| format!("read entity: {e}"))?
}

/// Register a new workspace with the daemon. Caller may supply an `id`
/// for deterministic test setups; otherwise we generate a UUIDv4-style
/// hex id locally. Daemon enforces `directory` non-empty but does not
/// validate filesystem existence — tests can use `/tmp/whatever` freely.
/// Fails if the daemon command cannot be queued for delivery. Observers
/// (canvas, sidebar) still react to the daemon's `workspace_registered`
/// broadcast on their own schedule, so callers should poll `get_state` or
/// `tail_events` for the post-condition rather than treating action
/// success as "workspace is now in the UI".
fn create_workspace(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let directory = payload
        .get("directory")
        .and_then(Value::as_str)
        .ok_or("payload.directory (string) is required")?
        .trim()
        .to_string();
    if directory.is_empty() {
        return Err("payload.directory must be non-empty".to_string());
    }
    let title = payload
        .get("title")
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_string();
    let id = match payload.get("id").and_then(Value::as_str) {
        Some(s) if !s.trim().is_empty() => s.trim().to_string(),
        _ => generate_workspace_id(),
    };

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.update_entity(&app_entity, |app: &mut NativeApp, cx| {
        app.register_workspace_and_select(id.clone(), title.clone(), directory.clone(), cx)
    })
    .map_err(|e| format!("update entity: {e}"))??;

    Ok(json!({
        "id": id,
        "title": title,
        "directory": directory,
    }))
}

/// Send `unregister_workspace` to the daemon. Daemon cascades: SIGTERMs
/// every member session and broadcasts `session_unregistered` for each
/// before broadcasting `workspace_unregistered`. Idempotent on unknown id
/// (daemon silently no-ops), so tests don't have to guard against double-
/// destroy in cleanup paths.
fn destroy_workspace(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let id = payload
        .get("id")
        .and_then(Value::as_str)
        .ok_or("payload.id (string) is required")?
        .trim()
        .to_string();
    if id.is_empty() {
        return Err("payload.id must be non-empty".to_string());
    }

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&app_entity, |app: &NativeApp, cx: &App| {
        app.daemon()
            .read(cx)
            .send_cmd(&UnregisterWorkspaceMessage::new(id.clone()))
    })
    .map_err(|e| format!("read entity: {e}"))??;

    Ok(json!({ "id": id }))
}

/// UUIDv4-shaped hex id (`xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx`) so
/// generated workspace ids look like the daemon's CLI-generated ones in
/// logs and the recent-locations table. Pulling in the `uuid` crate just
/// for this would add a dep without buying anything; `getrandom` is
/// already a workspace dependency. `pub(crate)` so the sidebar's "+ New
/// Workspace" flow can mint ids the same way.
pub(crate) fn generate_workspace_id() -> String {
    let mut bytes = [0u8; 16];
    if getrandom::getrandom(&mut bytes).is_err() {
        // OS RNG failure shouldn't happen, but if it does, fall back to a
        // process-pid + timestamp scheme so the action still produces a
        // distinguishable id rather than panicking the action pump.
        let pid = std::process::id();
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or_default();
        let mut idx = 0;
        for b in pid.to_le_bytes() {
            bytes[idx] = b;
            idx += 1;
        }
        for b in now.to_le_bytes() {
            if idx >= bytes.len() {
                break;
            }
            bytes[idx] = b;
            idx += 1;
        }
    }
    // Set version (4) and variant (10) bits per RFC 4122.
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;

    let mut s = String::with_capacity(36);
    for (i, b) in bytes.iter().enumerate() {
        if matches!(i, 4 | 6 | 8 | 10) {
            s.push('-');
        }
        s.push(hex_nibble(b >> 4));
        s.push(hex_nibble(b & 0x0f));
    }
    s
}

fn hex_nibble(n: u8) -> char {
    match n {
        0..=9 => (b'0' + n) as char,
        10..=15 => (b'a' + n - 10) as char,
        _ => unreachable!(),
    }
}

/// Pump a string of text through the focused panel's keyboard handler so
/// tests exercise the same encode/send path as real keypresses
/// (`TerminalView::on_key_down → encode_key → send_input`). Differs from
/// `send_pty_input`, which constructs the wire message directly and
/// therefore can't catch regressions in focus routing or key encoding.
fn type_into_panel(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .to_string();
    let text = payload
        .get("text")
        .and_then(Value::as_str)
        .ok_or("payload.text (string) is required")?
        .to_string();

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let view = cx
        .read_entity(&app_entity, |app: &NativeApp, cx: &App| {
            find_terminal_view(app, &session_id, cx)
        })
        .map_err(|e| format!("read entity: {e}"))?
        .ok_or_else(|| format!("no terminal panel for session: {session_id}"))?;

    let window = cx
        .update(|app: &mut App| app.windows().into_iter().next())
        .map_err(|e| format!("list windows: {e}"))?
        .ok_or("no open windows")?;

    let keystrokes = keystrokes_for_text(&text);
    let count = keystrokes.len();

    cx.update_window(window, |_root: AnyView, window: &mut Window, app: &mut App| {
        view.update(app, |view: &mut TerminalView, cx| {
            // Focus first so the on_key_down handler accepts the
            // keystroke. Mirrors what a real click would do before the
            // user starts typing — we want the test to fail if focus
            // routing is broken, not silently succeed.
            view.focus_handle.clone().focus(window);
            for keystroke in keystrokes {
                view.inject_keystroke(keystroke, window, cx);
            }
        });
    })
    .map_err(|e| format!("update window: {e}"))?;

    Ok(json!({
        "session_id": session_id,
        "keystrokes": count,
    }))
}

/// Translate a UTF-8 string into one `Keystroke` per logical key press,
/// matching what GPUI would deliver if a real user typed the text. `\n`
/// becomes `Enter`, `\t` becomes `Tab`; printable chars round-trip through
/// `key_char` (which `encode_key` prefers over `key` for non-modified
/// printable input). Doesn't try to model international layouts or IME
/// composition — single-line ASCII commands are the test surface here.
fn keystrokes_for_text(text: &str) -> Vec<Keystroke> {
    let mut out = Vec::with_capacity(text.len());
    for ch in text.chars() {
        let keystroke = match ch {
            '\n' => Keystroke {
                modifiers: Modifiers::default(),
                key: "enter".to_string(),
                key_char: None,
            },
            '\t' => Keystroke {
                modifiers: Modifiers::default(),
                key: "tab".to_string(),
                key_char: None,
            },
            other => {
                let s = other.to_string();
                Keystroke {
                    modifiers: Modifiers::default(),
                    key: s.clone(),
                    key_char: Some(s),
                }
            }
        };
        out.push(keystroke);
    }
    out
}

fn read_pane_text(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .to_string();

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let model = cx
        .read_entity(&app_entity, |app: &NativeApp, cx: &App| {
            find_terminal_model(app, &session_id, cx)
        })
        .map_err(|e| format!("read entity: {e}"))?
        .ok_or_else(|| format!("no terminal panel for session: {session_id}"))?;

    cx.read_entity(&model, |term: &TerminalModel, _cx: &App| {
        let rows = term.screen_text();
        let joined = rows.join("\n");
        json!({
            "session_id": term.session_id,
            "cols": term.cols,
            "rows": rows,
            "text": joined,
        })
    })
    .map_err(|e| format!("read terminal model: {e}"))
}

/// Walks every workspace's panels for a Terminal panel matching
/// `session_id` and returns its model handle. None when no such panel
/// exists in any workspace.
fn find_terminal_model(
    app: &NativeApp,
    session_id: &str,
    cx: &App,
) -> Option<Entity<TerminalModel>> {
    for ws in app.workspaces() {
        for panel in ws.read(cx).panels.iter() {
            if let PanelContent::Terminal { session_id: sid, view } = &panel.content {
                if sid.as_ref() == session_id {
                    return Some(view.read(cx).model().clone());
                }
            }
        }
    }
    None
}

/// Like `find_terminal_model` but returns the GPUI view entity. Needed by
/// `type_into_panel` because keystroke dispatch happens on the view, not
/// the model.
fn find_terminal_view(
    app: &NativeApp,
    session_id: &str,
    cx: &App,
) -> Option<Entity<TerminalView>> {
    for ws in app.workspaces() {
        for panel in ws.read(cx).panels.iter() {
            if let PanelContent::Terminal { session_id: sid, view } = &panel.content {
                if sid.as_ref() == session_id {
                    return Some(view.clone());
                }
            }
        }
    }
    None
}

/// Drain the in-process event ring buffer past `since_id` (defaults to 0,
/// meaning "give me everything you still have"). Doesn't need entity
/// access — the buffer is global — but routes through the action pump
/// for protocol uniformity.
fn tail_events(payload: Value) -> Result<Value, String> {
    let cursor = payload
        .get("since_id")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    Ok(events::tail_events_response(cursor))
}

fn set_zoom(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let zoom = payload
        .get("zoom")
        .and_then(Value::as_f64)
        .ok_or("payload.zoom (number) is required")? as f32;
    if !zoom.is_finite() || zoom <= 0.0 {
        return Err(format!("zoom must be a positive finite number, got {zoom}"));
    }
    // Default true: a single set_zoom is a discrete perf measurement
    // and should start from a clean window. Sweeps that mimic
    // scroll-wheel cadence pass `reset: false` so samples accumulate
    // across many small steps.
    let reset_fps = payload
        .get("reset")
        .and_then(Value::as_bool)
        .unwrap_or(true);
    let entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.update_entity(&entity, |app, cx| {
        app.set_canvas_zoom(zoom, reset_fps, cx);
        json!({ "zoom": zoom })
    })
    .map_err(|e| format!("update entity: {e}"))
}

fn get_window_geometry(cx: &mut AsyncApp) -> Result<Value, String> {
    let window = cx
        .update(|app: &mut App| app.windows().into_iter().next())
        .map_err(|e| format!("list windows: {e}"))?
        .ok_or("no open windows")?;
    cx.update_window(
        window,
        |_view: AnyView, window: &mut Window, _app: &mut App| {
            let bounds = window.bounds();
            json!({
                "scaleFactor": window.scale_factor(),
                "globalBounds": {
                    "x": pf(bounds.origin.x),
                    "y": pf(bounds.origin.y),
                    "width": pf(bounds.size.width),
                    "height": pf(bounds.size.height),
                },
            })
        },
    )
    .map_err(|e| format!("update window: {e}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Newline must map to `Enter` with `key_char: None` (not `\n`) so
    /// `terminal_view::encode_key` takes the dedicated-escape branch and
    /// emits `\r`. If `\n` ever leaks into `key_char`, encode_key returns
    /// `\n` and shells stop running the typed command — broken e2e but
    /// silent in production. Locking the shape here.
    #[test]
    fn keystrokes_for_text_uses_named_keys_for_newline_and_tab() {
        let ks = keystrokes_for_text("a\n\tb");
        assert_eq!(ks.len(), 4);
        assert_eq!(ks[0].key, "a");
        assert_eq!(ks[0].key_char.as_deref(), Some("a"));
        assert_eq!(ks[1].key, "enter");
        assert!(ks[1].key_char.is_none());
        assert_eq!(ks[2].key, "tab");
        assert!(ks[2].key_char.is_none());
        assert_eq!(ks[3].key, "b");
        assert_eq!(ks[3].key_char.as_deref(), Some("b"));
    }

    #[test]
    fn keystrokes_for_text_default_modifiers_are_clear() {
        // Bare ASCII shouldn't pick up shift/ctrl/alt — the encoder
        // branches on those modifiers and would emit control sequences
        // instead of the literal char.
        let ks = keystrokes_for_text("X");
        assert_eq!(ks.len(), 1);
        let m = &ks[0].modifiers;
        assert!(!m.shift && !m.control && !m.alt);
    }

    #[test]
    fn generated_workspace_id_is_uuidv4_shaped() {
        let id = generate_workspace_id();
        assert_eq!(id.len(), 36, "uuid: {id}");
        let bytes = id.as_bytes();
        for (i, b) in bytes.iter().enumerate() {
            if matches!(i, 8 | 13 | 18 | 23) {
                assert_eq!(*b, b'-', "expected '-' at {i} of {id}");
            } else {
                assert!(b.is_ascii_hexdigit(), "non-hex at {i} of {id}");
                assert!(!b.is_ascii_uppercase(), "uppercase at {i} of {id}");
            }
        }
        // Version + variant nibbles per RFC 4122.
        assert_eq!(bytes[14], b'4', "version nibble: {id}");
        assert!(matches!(bytes[19], b'8' | b'9' | b'a' | b'b'), "variant nibble: {id}");
    }

    #[test]
    fn generated_workspace_ids_differ() {
        // Two consecutive calls should never collide; if they do the test
        // fails fast rather than leaking duplicate ids into the daemon.
        assert_ne!(generate_workspace_id(), generate_workspace_id());
    }
}
