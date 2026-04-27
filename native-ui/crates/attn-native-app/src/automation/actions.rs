/// GPUI-bound action handlers. The wire-protocol server runs on the GPUI
/// background executor and can't access entities directly (`AsyncApp` is
/// !Send across some boundaries, and entity access has to happen on the
/// foreground thread). We bridge with an async channel: the dispatcher
/// sends an `ActionRequest`, a foreground-spawned `pump_actions` task
/// reads each request, runs the handler with `&mut AsyncApp`, and sends
/// the result back.
use std::sync::Arc;

use async_channel::{unbounded, Receiver, Sender};
use attn_protocol::PtyInputMessage;
use gpui::{prelude::*, AnyView, App, AsyncApp, Entity, SharedString, WeakEntity, Window};
use serde_json::{json, Value};

use crate::canvas_view::pf;
use crate::panel::PanelContent;
use crate::spike5_app::Spike5App;
use crate::terminal_model::TerminalModel;

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
    app: WeakEntity<Spike5App>,
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
    app: &WeakEntity<Spike5App>,
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
        "read_pane_text" => read_pane_text(app, cx, payload),
        "tail_events" => tail_events(payload),
        _ => Err(format!("unknown action: {action}")),
    }
}

fn get_state(app: &WeakEntity<Spike5App>, cx: &mut AsyncApp) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("Spike5App entity dropped")?;
    cx.read_entity(&entity, |app: &Spike5App, cx: &App| app.automation_snapshot(cx))
        .map_err(|e| format!("read entity: {e}"))
}

fn list_sessions(app: &WeakEntity<Spike5App>, cx: &mut AsyncApp) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("Spike5App entity dropped")?;
    cx.read_entity(&entity, |app: &Spike5App, cx: &App| {
        let sessions = app.daemon().read(cx).sessions();
        serde_json::to_value(sessions).unwrap_or(Value::Null)
    })
    .map_err(|e| format!("read entity: {e}"))
}

fn select_workspace(
    app: &WeakEntity<Spike5App>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let id = payload
        .get("id")
        .and_then(Value::as_str)
        .ok_or("payload.id (string) is required")?
        .to_string();
    let entity = app.upgrade().ok_or("Spike5App entity dropped")?;
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
    app: &WeakEntity<Spike5App>,
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

    let app_entity = app.upgrade().ok_or("Spike5App entity dropped")?;
    let workspace = cx
        .read_entity(&app_entity, |app: &Spike5App, _cx: &App| {
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
    app: &WeakEntity<Spike5App>,
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

    let app_entity = app.upgrade().ok_or("Spike5App entity dropped")?;
    cx.read_entity(&app_entity, |app: &Spike5App, cx: &App| {
        if find_terminal_model(app, &session_id, cx).is_none() {
            return Err(format!("no terminal panel for session: {session_id}"));
        }
        // The daemon routes by session id; we just need to send the
        // message. Passing through TerminalModel would also work but
        // it's strictly equivalent and adds an indirection.
        app.daemon()
            .read(cx)
            .send_cmd(&PtyInputMessage::new(session_id.clone(), text.clone()));
        Ok(json!({
            "session_id": session_id,
            "bytes_sent": text.len(),
        }))
    })
    .map_err(|e| format!("read entity: {e}"))?
}

fn read_pane_text(
    app: &WeakEntity<Spike5App>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = payload
        .get("session_id")
        .and_then(Value::as_str)
        .ok_or("payload.session_id (string) is required")?
        .to_string();

    let app_entity = app.upgrade().ok_or("Spike5App entity dropped")?;
    let model = cx
        .read_entity(&app_entity, |app: &Spike5App, cx: &App| {
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
    app: &Spike5App,
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
