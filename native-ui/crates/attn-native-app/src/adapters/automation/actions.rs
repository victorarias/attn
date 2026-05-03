/// GPUI-bound action handlers. The wire-protocol server runs on the GPUI
/// background executor and can't access entities directly (`AsyncApp` is
/// !Send across some boundaries, and entity access has to happen on the
/// foreground thread). We bridge with an async channel: the dispatcher
/// sends an `ActionRequest`, a foreground-spawned `pump_actions` task
/// reads each request, runs the handler with `&mut AsyncApp`, and sends
/// the result back.
use std::sync::Arc;

use async_channel::{unbounded, Receiver, Sender};
use attn_protocol::{PtyInputMessage, UnregisterSessionMessage, UnregisterWorkspaceMessage};
use gpui::{
    prelude::*, AnyView, App, AsyncApp, Entity, Keystroke, Modifiers, SharedString, WeakEntity,
    Window,
};
use serde_json::{json, Value};

use crate::app::NativeApp;
use crate::domain::panel_placement::{
    place_panel_adjacent_avoiding, AdjacentPanelDirection, PanelPlacementItem, Rect,
};
use crate::domain::viewport::pf;
use crate::state::terminal_model::TerminalModel;
use crate::views::terminal_view::TerminalView;

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
        "focus_panel" => focus_panel(app, cx, payload),
        "move_panel" => move_panel(app, cx, payload),
        "send_pty_input" => send_pty_input(app, cx, payload),
        "type_into_panel" => type_into_panel(app, cx, payload),
        "read_pane_text" => read_pane_text(app, cx, payload),
        "tail_events" => tail_events(payload),
        "set_zoom" => set_zoom(app, cx, payload),
        "create_workspace" => create_workspace(app, cx, payload),
        "destroy_workspace" => destroy_workspace(app, cx, payload),
        "spawn_session" => spawn_session(app, cx, payload),
        "split_shell" => split_shell(app, cx, payload),
        "unregister_session" => unregister_session(app, cx, payload),
        _ => Err(format!("unknown action: {action}")),
    }
}

fn focus_panel(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .to_string();
    let input_focus = payload
        .get("input_focus")
        .and_then(Value::as_bool)
        .unwrap_or(true);

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let window = cx
        .update(|app: &mut App| app.windows().into_iter().next())
        .map_err(|e| format!("list windows: {e}"))?
        .ok_or("no open windows")?;
    let session_id_for_focus = session_id.clone();

    cx.update_window(
        window,
        move |_root: AnyView, window: &mut Window, app: &mut App| {
            app_entity.update(app, |native: &mut NativeApp, cx| {
                native.set_canvas_panel_focus_by_session(
                    &session_id_for_focus,
                    input_focus,
                    window,
                    cx,
                )
            })
        },
    )
    .map_err(|e| format!("update window: {e}"))??;

    Ok(json!({
        "session_id": session_id,
        "input_focus": input_focus,
    }))
}

fn get_state(app: &WeakEntity<NativeApp>, cx: &mut AsyncApp) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&entity, |app: &NativeApp, cx: &App| {
        app.automation_snapshot(cx)
    })
    .map_err(|e| format!("read entity: {e}"))
}

fn list_sessions(app: &WeakEntity<NativeApp>, cx: &mut AsyncApp) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&entity, |app: &NativeApp, _cx: &App| {
        serde_json::to_value(app.sessions_snapshot()).unwrap_or(Value::Null)
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
    let world_x = payload
        .get("world_x")
        .and_then(Value::as_f64)
        .map(|n| n as f32);
    let world_y = payload
        .get("world_y")
        .and_then(Value::as_f64)
        .map(|n| n as f32);
    let width = payload
        .get("width")
        .and_then(Value::as_f64)
        .map(|n| n as f32);
    let height = payload
        .get("height")
        .and_then(Value::as_f64)
        .map(|n| n as f32);

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let workspace = cx
        .read_entity(&app_entity, |app: &NativeApp, _cx: &App| {
            app.workspace(&workspace_id)
        })
        .map_err(|e| format!("read entity: {e}"))?
        .ok_or_else(|| format!("unknown workspace id: {workspace_id}"))?;

    let (
        daemon_panel_id,
        session_id,
        title,
        target_world_x,
        target_world_y,
        target_width,
        target_height,
    ) = cx
        .read_entity(&workspace, |ws, _cx| {
            let existing = ws
                .panels
                .iter()
                .find(|panel| panel.id == panel_id)
                .ok_or_else(|| format!("unknown panel id: {panel_id}"))?;
            let target_world_x = world_x.unwrap_or(existing.world_x);
            let target_world_y = world_y.unwrap_or(existing.world_y);
            let target_width = width.unwrap_or(existing.width);
            let target_height = height.unwrap_or(existing.height);
            Ok::<_, String>((
                existing.daemon_panel_id.to_string(),
                existing.session_id.to_string(),
                existing.title.to_string(),
                target_world_x,
                target_world_y,
                target_width,
                target_height,
            ))
        })
        .map_err(|e| format!("read workspace: {e}"))??;

    let daemon_panel_id_for_send = daemon_panel_id.clone();
    cx.read_entity(&app_entity, |app: &NativeApp, cx: &App| {
        app.daemon()
            .read(cx)
            .send_cmd(&attn_protocol::UpdateWorkspacePanelGeometryMessage::new(
                workspace_id.clone(),
                daemon_panel_id_for_send,
                Some(target_world_x),
                Some(target_world_y),
                Some(target_width),
                Some(target_height),
            ))
    })
    .map_err(|e| format!("read entity: {e}"))??;

    Ok(json!({
        "panel": {
            "id": panel_id,
            "daemon_panel_id": daemon_panel_id,
            "kind": "terminal",
            "title": title,
            "session_id": session_id,
            "world_x": target_world_x,
            "world_y": target_world_y,
            "width": target_width,
            "height": target_height,
        }
    }))
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

/// Spawn a new session inside an existing workspace through the same
/// `NativeApp::spawn_session_in_workspace` path the canvas toolbar uses,
/// so a regression in id generation, pending-spawn tracking, or wire
/// shape trips this action just like it would in the UI. Caller may
/// override `cwd` for tests that want to spawn outside the workspace's
/// recorded directory; otherwise we fall back to the workspace's cwd
/// (matching the toolbar's behaviour).
fn spawn_session(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id = payload
        .get("workspace_id")
        .and_then(Value::as_str)
        .ok_or("payload.workspace_id (string) is required")?
        .trim()
        .to_string();
    if workspace_id.is_empty() {
        return Err("payload.workspace_id must be non-empty".to_string());
    }
    let agent = payload
        .get("agent")
        .and_then(Value::as_str)
        .ok_or("payload.agent (string) is required")?
        .trim()
        .to_string();
    if agent.is_empty() {
        return Err("payload.agent must be non-empty".to_string());
    }
    let cwd = payload
        .get("cwd")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned);

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let session_id = cx
        .update_entity(&app_entity, |app: &mut NativeApp, cx| match cwd.clone() {
            Some(directory) => app.spawn_session_in_workspace_at(
                SharedString::from(workspace_id.clone()),
                directory,
                SharedString::from(agent.clone()),
                cx,
            ),
            None => app.spawn_session_in_workspace(
                SharedString::from(workspace_id.clone()),
                SharedString::from(agent.clone()),
                cx,
            ),
        })
        .map_err(|e| format!("update entity: {e}"))??;

    Ok(json!({
        "session_id": session_id.as_ref(),
        "workspace_id": workspace_id,
        "agent": agent,
        "cwd": cwd,
    }))
}

fn split_shell(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .trim()
        .to_string();
    if session_id.is_empty() {
        return Err("payload.session_id must be non-empty".to_string());
    }
    let direction = parse_adjacent_direction(
        payload
            .get("direction")
            .and_then(Value::as_str)
            .unwrap_or("right"),
    )?;

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    let (workspace_id, placement) = cx
        .read_entity(&app_entity, |app: &NativeApp, cx: &App| {
            for ws in app.workspaces() {
                let ws = ws.read(cx);
                if let Some(panel) = ws
                    .panels
                    .iter()
                    .find(|panel| panel.session_id.as_ref() == session_id.as_str())
                {
                    let anchor = Rect {
                        x: panel.world_x,
                        y: panel.world_y,
                        width: panel.width,
                        height: panel.height,
                    };
                    let existing = ws
                        .panels
                        .iter()
                        .map(|panel| PanelPlacementItem {
                            id: panel.id,
                            rect: Rect {
                                x: panel.world_x,
                                y: panel.world_y,
                                width: panel.width,
                                height: panel.height,
                            },
                        })
                        .collect::<Vec<_>>();
                    return Ok::<_, String>((
                        ws.id.clone(),
                        place_panel_adjacent_avoiding(anchor, direction, &existing),
                    ));
                }
            }
            Err(format!("no terminal panel for session: {session_id}"))
        })
        .map_err(|e| format!("read entity: {e}"))??;

    let spawned = cx
        .update_entity(&app_entity, |app: &mut NativeApp, cx| {
            app.spawn_shell_split_in_workspace(
                workspace_id.clone(),
                SharedString::from(session_id.clone()),
                direction,
                placement,
                cx,
            )
        })
        .map_err(|e| format!("update entity: {e}"))??;

    Ok(json!({
        "session_id": spawned.as_ref(),
        "workspace_id": workspace_id.as_ref(),
        "anchor_session_id": session_id.as_str(),
        "agent": "shell",
        "direction": adjacent_direction_name(direction),
        "placement": {
            "world_x": placement.x,
            "world_y": placement.y,
            "width": placement.width,
            "height": placement.height,
        },
    }))
}

/// Tear down a session through the same daemon command the canvas's
/// panel close button issues. Idempotent on the daemon — retrying after
/// a missed response can't double-fault.
fn unregister_session(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .trim()
        .to_string();
    if session_id.is_empty() {
        return Err("payload.session_id must be non-empty".to_string());
    }

    let app_entity = app.upgrade().ok_or("NativeApp entity dropped")?;
    cx.read_entity(&app_entity, |app: &NativeApp, cx: &App| {
        app.daemon()
            .read(cx)
            .send_cmd(&UnregisterSessionMessage::new(session_id.clone()))
    })
    .map_err(|e| format!("read entity: {e}"))??;

    Ok(json!({ "session_id": session_id }))
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
    let focus = payload
        .get("focus")
        .and_then(Value::as_bool)
        .unwrap_or(true);

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
    let session_id_for_focus = session_id.clone();
    let app_entity_for_focus = app_entity.clone();

    cx.update_window(
        window,
        move |_root: AnyView, window: &mut Window, app: &mut App| {
            if focus {
                app_entity_for_focus.update(app, |native: &mut NativeApp, cx| {
                    native.set_canvas_panel_focus_by_session(
                        &session_id_for_focus,
                        true,
                        window,
                        cx,
                    )
                })?;
            }
            view.update(app, |view: &mut TerminalView, cx| {
                if focus && view.set_input_enabled(true) {
                    cx.notify();
                }
                for keystroke in keystrokes {
                    view.inject_keystroke(keystroke, window, cx);
                }
            });
            Ok::<_, String>(())
        },
    )
    .map_err(|e| format!("update window: {e}"))??;

    Ok(json!({
        "session_id": session_id,
        "keystrokes": count,
        "focused_first": focus,
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
            if panel.session_id.as_ref() == session_id {
                return Some(panel.view.read(cx).model().clone());
            }
        }
    }
    None
}

/// Like `find_terminal_model` but returns the GPUI view entity. Needed by
/// `type_into_panel` because keystroke dispatch happens on the view, not
/// the model.
fn find_terminal_view(app: &NativeApp, session_id: &str, cx: &App) -> Option<Entity<TerminalView>> {
    for ws in app.workspaces() {
        for panel in ws.read(cx).panels.iter() {
            if panel.session_id.as_ref() == session_id {
                return Some(panel.view.clone());
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
    let cursor = payload.get("since_id").and_then(Value::as_u64).unwrap_or(0);
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

fn parse_adjacent_direction(value: &str) -> Result<AdjacentPanelDirection, String> {
    match value.trim() {
        "right" => Ok(AdjacentPanelDirection::Right),
        "bottom" | "down" => Ok(AdjacentPanelDirection::Bottom),
        other => Err(format!("unsupported direction: {other}")),
    }
}

fn adjacent_direction_name(direction: AdjacentPanelDirection) -> &'static str {
    match direction {
        AdjacentPanelDirection::Right => "right",
        AdjacentPanelDirection::Bottom => "bottom",
    }
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
        assert!(
            matches!(bytes[19], b'8' | b'9' | b'a' | b'b'),
            "variant nibble: {id}"
        );
    }

    #[test]
    fn generated_workspace_ids_differ() {
        // Two consecutive calls should never collide; if they do the test
        // fails fast rather than leaking duplicate ids into the daemon.
        assert_ne!(generate_workspace_id(), generate_workspace_id());
    }

    #[test]
    fn parse_adjacent_direction_accepts_right_and_bottom() {
        assert_eq!(
            parse_adjacent_direction("right").unwrap(),
            AdjacentPanelDirection::Right
        );
        assert_eq!(
            parse_adjacent_direction("bottom").unwrap(),
            AdjacentPanelDirection::Bottom
        );
        assert_eq!(
            parse_adjacent_direction("down").unwrap(),
            AdjacentPanelDirection::Bottom
        );
        assert!(parse_adjacent_direction("left").is_err());
    }
}
