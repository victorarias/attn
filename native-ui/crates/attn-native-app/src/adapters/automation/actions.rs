use std::{fs, path::Path, process::Command, sync::Arc};

use async_channel::{unbounded, Receiver, Sender};
use attn_protocol::{
    KillSessionMessage, MuteMessage, PtyInputMessage, RegisterWorkspaceMessage,
    SpawnSessionMessage, UnregisterWorkspaceMessage, WorkspaceLayoutClosePaneMessage,
    WorkspaceLayoutFocusPaneMessage, WorkspaceLayoutSplitDirection,
    WorkspaceLayoutSplitPaneMessage,
};
use gpui::{AnyView, App, AppContext as _, AsyncApp, Keystroke, Modifiers, WeakEntity};
use serde_json::{json, Value};

use crate::app::NativeApp;

use super::{events, server::Dispatcher};

pub struct ActionRequest {
    action: String,
    payload: Value,
    reply: Sender<Result<Value, String>>,
}

pub fn make_dispatcher() -> (Dispatcher, Receiver<ActionRequest>) {
    let (sender, receiver) = unbounded::<ActionRequest>();
    let sender = Arc::new(sender);
    let dispatcher: Dispatcher = Arc::new(move |action, payload| {
        let sender = sender.clone();
        Box::pin(async move {
            let (reply, result) = async_channel::bounded(1);
            sender
                .send(ActionRequest {
                    action,
                    payload,
                    reply,
                })
                .await
                .map_err(|error| format!("automation queue closed: {error}"))?;
            result
                .recv()
                .await
                .map_err(|error| format!("automation reply dropped: {error}"))?
        })
    });
    (dispatcher, receiver)
}

pub async fn pump_actions(
    receiver: Receiver<ActionRequest>,
    app: WeakEntity<NativeApp>,
    mut cx: AsyncApp,
) {
    while let Ok(request) = receiver.recv().await {
        let result = handle_action(&request.action, request.payload, &app, &mut cx);
        let _ = request.reply.send(result).await;
    }
}

fn handle_action(
    action: &str,
    payload: Value,
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
) -> Result<Value, String> {
    match action {
        "ping" => Ok(json!({
            "pong": true,
            "frontendReady": true,
            "pid": std::process::id(),
        })),
        "get_state" => read_app(app, cx, |app, cx| app.automation_snapshot(cx)),
        "capture_structured_snapshot" => {
            let include_text = payload
                .get("includePaneText")
                .and_then(Value::as_bool)
                .unwrap_or(true);
            read_app(app, cx, |app, cx| {
                app.automation_structured_snapshot(cx, include_text)
            })
        }
        "capture_render_health" => read_app(app, cx, |app, cx| app.automation_render_health(cx)),
        "list_sessions" => read_app(app, cx, |app, _| app.automation_sessions()),
        "tail_events" => Ok(events::tail(
            payload.get("since_id").and_then(Value::as_u64).unwrap_or(0),
        )),
        "get_window_bounds" => window_bounds(cx),
        "screenshot" => capture_screenshot(cx, payload),
        "create_workspace" => create_workspace(app, cx, payload),
        "spawn_session" => spawn_session(app, cx, payload),
        "destroy_workspace" => destroy_workspace(app, cx, payload),
        "kill_runtime" => kill_runtime(app, cx, payload),
        "select_workspace" => select_workspace(app, cx, payload),
        "focus_pane" => focus_pane(app, cx, payload),
        "split_pane" => split_pane(app, cx, payload),
        "mute_session" => mute_session(app, cx, payload),
        "close_pane" => close_pane(app, cx, payload),
        "write_pane" => write_pane(app, cx, payload),
        "type_pane_via_ui" => type_pane_via_ui(app, cx, payload),
        "read_pane_text" => read_pane_text(app, cx, payload),
        _ => Err(format!("unknown action: {action}")),
    }
}

fn create_workspace(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id =
        required_string(&payload, "workspace_id").or_else(|_| required_string(&payload, "id"))?;
    let directory = required_string(&payload, "directory")?;
    let title = payload
        .get("title")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or("Automation Workspace")
        .to_string();
    send_daemon(
        app,
        cx,
        &RegisterWorkspaceMessage::new(workspace_id.clone(), title.clone(), directory.clone()),
    )?;
    events::record(
        "workspace_create_requested",
        json!({"workspace_id": workspace_id, "title": title, "directory": directory}),
    );
    Ok(json!({
        "workspace_id": workspace_id,
        "title": title,
        "directory": directory,
    }))
}

fn spawn_session(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id =
        required_string(&payload, "session_id").or_else(|_| required_string(&payload, "id"))?;
    let workspace_id = required_string(&payload, "workspace_id")?;
    let cwd = required_string(&payload, "cwd")?;
    let agent = payload
        .get("agent")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or("shell")
        .to_string();
    let cols = payload.get("cols").and_then(Value::as_u64).unwrap_or(100) as u16;
    let rows = payload.get("rows").and_then(Value::as_u64).unwrap_or(36) as u16;
    let mut message = SpawnSessionMessage::new(
        session_id.clone(),
        cwd.clone(),
        workspace_id.clone(),
        agent.clone(),
        cols,
        rows,
    );
    message.executable = payload
        .get("executable")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned);
    send_daemon(app, cx, &message)?;
    events::record(
        "session_spawn_requested",
        json!({
            "session_id": session_id,
            "workspace_id": workspace_id,
            "cwd": cwd,
            "agent": agent,
            "executable": message.executable,
        }),
    );
    Ok(json!({
        "session_id": session_id,
        "workspace_id": workspace_id,
        "agent": agent,
        "executable": message.executable,
    }))
}

fn destroy_workspace(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id =
        required_string(&payload, "workspace_id").or_else(|_| required_string(&payload, "id"))?;
    send_daemon(
        app,
        cx,
        &UnregisterWorkspaceMessage::new(workspace_id.clone()),
    )?;
    events::record(
        "workspace_destroy_requested",
        json!({"workspace_id": workspace_id}),
    );
    Ok(json!({"workspace_id": workspace_id}))
}

fn kill_runtime(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let runtime_id = required_string(&payload, "runtime_id")
        .or_else(|_| required_string(&payload, "session_id"))
        .or_else(|_| required_string(&payload, "id"))?;
    send_daemon(app, cx, &KillSessionMessage::new(runtime_id.clone()))?;
    events::record("runtime_kill_requested", json!({"runtime_id": runtime_id}));
    Ok(json!({"runtime_id": runtime_id}))
}

fn read_app(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    read: impl FnOnce(&NativeApp, &App) -> Value,
) -> Result<Value, String> {
    let entity = app.upgrade().ok_or("native app dropped")?;
    cx.read_entity(&entity, read)
        .map_err(|error| format!("read native app: {error}"))
}

fn select_workspace(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id =
        required_string(&payload, "workspace_id").or_else(|_| required_string(&payload, "id"))?;
    let entity = app.upgrade().ok_or("native app dropped")?;
    cx.update_entity(&entity, |app, cx| {
        app.automation_select_workspace(&workspace_id, cx)
    })
    .map_err(|error| format!("update native app: {error}"))??;
    events::record("workspace_selected", json!({"workspace_id": workspace_id}));
    Ok(json!({"selected_workspace_id": workspace_id}))
}

fn focus_pane(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id = required_string(&payload, "workspace_id")?;
    let pane_id = required_string(&payload, "pane_id")?;
    let runtime_id = resolve_runtime_id(app, cx, &payload).ok();
    send_daemon(
        app,
        cx,
        &WorkspaceLayoutFocusPaneMessage::new(workspace_id.clone(), pane_id.clone()),
    )?;
    events::record(
        "pane_focus_requested",
        json!({"workspace_id": workspace_id, "pane_id": pane_id}),
    );
    if let Some(runtime_id) = runtime_id {
        focus_terminal_view(app, cx, &runtime_id)?;
    }
    Ok(json!({"workspace_id": workspace_id, "pane_id": pane_id}))
}

fn split_pane(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id = optional_string(&payload, &["workspaceId", "workspace_id"])
        .ok_or("payload.workspaceId is required")?;
    let target_pane_id = optional_string(
        &payload,
        &["targetPaneId", "target_pane_id", "paneId", "pane_id"],
    )
    .ok_or("payload.targetPaneId is required")?;
    let direction = match optional_string(&payload, &["direction"]).as_deref() {
        Some("horizontal") => WorkspaceLayoutSplitDirection::Horizontal,
        Some("vertical") | None => WorkspaceLayoutSplitDirection::Vertical,
        Some(value) => return Err(format!("unknown split direction: {value}")),
    };
    send_daemon(
        app,
        cx,
        &WorkspaceLayoutSplitPaneMessage {
            cmd: "workspace_layout_split_pane",
            workspace_id: workspace_id.clone(),
            target_pane_id: target_pane_id.clone(),
            direction,
        },
    )?;
    events::record(
        "pane_split_requested",
        json!({"workspace_id": workspace_id, "target_pane_id": target_pane_id}),
    );
    Ok(json!({
        "workspaceId": workspace_id,
        "targetPaneId": target_pane_id,
    }))
}

fn mute_session(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let session_id = required_string(&payload, "session_id")?;
    send_daemon(app, cx, &MuteMessage::new(session_id.clone()))?;
    events::record("session_mute_requested", json!({"session_id": session_id}));
    Ok(json!({"session_id": session_id}))
}

fn close_pane(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let workspace_id = required_string(&payload, "workspace_id")?;
    let pane_id = required_string(&payload, "pane_id")?;
    send_daemon(
        app,
        cx,
        &WorkspaceLayoutClosePaneMessage::new(workspace_id.clone(), pane_id.clone()),
    )?;
    events::record(
        "pane_close_requested",
        json!({"workspace_id": workspace_id, "pane_id": pane_id}),
    );
    Ok(json!({"workspace_id": workspace_id, "pane_id": pane_id}))
}

fn write_pane(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let runtime_id = resolve_runtime_id(app, cx, &payload)?;
    let mut text = required_raw_string(&payload, "text")?;
    if payload
        .get("submit")
        .and_then(Value::as_bool)
        .unwrap_or(true)
    {
        text.push('\r');
    }
    let entity = app.upgrade().ok_or("native app dropped")?;
    let visible = cx
        .read_entity(&entity, |app, _| app.has_visible_runtime(&runtime_id))
        .map_err(|error| format!("read native app: {error}"))?;
    if !visible {
        return Err(format!("no visible terminal runtime: {runtime_id}"));
    }
    send_daemon(
        app,
        cx,
        &PtyInputMessage::new(runtime_id.clone(), text.clone()),
    )?;
    Ok(target_result(&payload, runtime_id, text.len()))
}

fn type_pane_via_ui(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let runtime_id = resolve_runtime_id(app, cx, &payload)?;
    let text = required_raw_string(&payload, "text")?;
    let entity = app.upgrade().ok_or("native app dropped")?;
    let view = cx
        .read_entity(&entity, |app, _| app.terminal_view(&runtime_id))
        .map_err(|error| format!("read native app: {error}"))?
        .ok_or_else(|| format!("no visible terminal runtime: {runtime_id}"))?;
    let window = cx
        .update(|app| app.windows().into_iter().next())
        .map_err(|error| format!("list windows: {error}"))?
        .ok_or("no native window")?;
    let keystrokes = keystrokes_for_text(&text);
    let count = keystrokes.len();
    let accepted = cx
        .update_window(window, move |_root: AnyView, window, app| {
            view.update(app, |view, cx| {
                keystrokes
                    .into_iter()
                    .filter(|keystroke| view.inject_keystroke(keystroke.clone(), window, cx))
                    .count()
            })
        })
        .map_err(|error| format!("update native window: {error}"))?;
    if accepted != count {
        return Err(format!(
            "terminal pane is not focused for UI input: accepted {accepted} of {count} keystrokes"
        ));
    }
    let mut result = target_result(&payload, runtime_id, 0);
    result["keystrokes"] = json!(count);
    result["focusedFirst"] = json!(false);
    Ok(result)
}

fn focus_terminal_view(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    runtime_id: &str,
) -> Result<(), String> {
    let entity = app.upgrade().ok_or("native app dropped")?;
    let view = cx
        .read_entity(&entity, |app, _| app.terminal_view(runtime_id))
        .map_err(|error| format!("read native app: {error}"))?
        .ok_or_else(|| format!("no visible terminal runtime: {runtime_id}"))?;
    let window = cx
        .update(|app| app.windows().into_iter().next())
        .map_err(|error| format!("list windows: {error}"))?
        .ok_or("no native window")?;
    cx.update_window(window, move |_root: AnyView, window, app| {
        view.update(app, |view, _| view.focus_for_input(window));
    })
    .map_err(|error| format!("focus native terminal: {error}"))
}

fn read_pane_text(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: Value,
) -> Result<Value, String> {
    let runtime_id = resolve_runtime_id(app, cx, &payload)?;
    let entity = app.upgrade().ok_or("native app dropped")?;
    let view = cx
        .read_entity(&entity, |app, _| app.terminal_view(&runtime_id))
        .map_err(|error| format!("read native app: {error}"))?
        .ok_or_else(|| format!("no visible terminal runtime: {runtime_id}"))?;
    cx.read_entity(&view, |view, cx| {
        let (cols, rows) = view.terminal_size(cx);
        let mut result = json!({
            "runtimeId": runtime_id,
            "cols": cols,
            "rows": rows,
            "text": view.screen_text().unwrap_or_default(),
        });
        if let Some(workspace_id) = optional_string(&payload, &["workspaceId", "workspace_id"]) {
            result["workspaceId"] = json!(workspace_id);
        }
        if let Some(pane_id) = optional_string(&payload, &["paneId", "pane_id"]) {
            result["paneId"] = json!(pane_id);
        }
        result
    })
    .map_err(|error| format!("read terminal: {error}"))
}

fn send_daemon<T: serde::Serialize>(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    message: &T,
) -> Result<(), String> {
    let entity = app.upgrade().ok_or("native app dropped")?;
    cx.read_entity(&entity, |app, cx| app.daemon().read(cx).send(message))
        .map_err(|error| format!("read native app: {error}"))?
}

fn window_bounds(cx: &mut AsyncApp) -> Result<Value, String> {
    let window = cx
        .update(|app| app.windows().into_iter().next())
        .map_err(|error| format!("list windows: {error}"))?
        .ok_or("no native window")?;
    cx.update_window(window, |_root: AnyView, window, _| {
        let bounds = window.bounds();
        json!({
            "scaleFactor": window.scale_factor(),
            "logicalBounds": {
                "x": f32::from(bounds.origin.x),
                "y": f32::from(bounds.origin.y),
                "width": f32::from(bounds.size.width),
                "height": f32::from(bounds.size.height),
            },
            "globalBounds": {
                "x": f32::from(bounds.origin.x),
                "y": f32::from(bounds.origin.y),
                "width": f32::from(bounds.size.width),
                "height": f32::from(bounds.size.height),
            }
        })
    })
    .map_err(|error| format!("read native window: {error}"))
}

fn capture_screenshot(cx: &mut AsyncApp, payload: Value) -> Result<Value, String> {
    let path =
        optional_string(&payload, &["path"]).unwrap_or_else(|| "/tmp/attn-native.png".to_string());
    let window_id = payload
        .get("windowId")
        .and_then(Value::as_u64)
        .filter(|id| *id > 0)
        .ok_or("payload.windowId (positive number) is required for exact-window capture")?;
    let bounds = window_bounds(cx)?;
    if let Some(parent) = Path::new(&path).parent() {
        fs::create_dir_all(parent)
            .map_err(|error| format!("create screenshot directory: {error}"))?;
    }
    let window_id = window_id.to_string();
    let output = Command::new("/usr/sbin/screencapture")
        .args(["-x", "-l", &window_id, "-o", &path])
        .output()
        .map_err(|error| format!("run screencapture: {error}"))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
        return Err(format!(
            "native screencapture failed{}",
            if stderr.is_empty() {
                String::new()
            } else {
                format!(": {stderr}")
            }
        ));
    }
    Ok(json!({
        "source": "native_process",
        "path": path,
        "windowId": window_id,
        "logicalBounds": bounds.get("logicalBounds"),
    }))
}

fn required_string(payload: &Value, key: &str) -> Result<String, String> {
    payload
        .get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
        .ok_or_else(|| format!("payload.{key} (string) is required"))
}

fn required_raw_string(payload: &Value, key: &str) -> Result<String, String> {
    payload
        .get(key)
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
        .ok_or_else(|| format!("payload.{key} (non-empty string) is required"))
}

fn optional_string(payload: &Value, keys: &[&str]) -> Option<String> {
    keys.iter().find_map(|key| {
        payload
            .get(*key)
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .map(ToOwned::to_owned)
    })
}

fn resolve_runtime_id(
    app: &WeakEntity<NativeApp>,
    cx: &mut AsyncApp,
    payload: &Value,
) -> Result<String, String> {
    if let Some(runtime_id) = optional_string(
        payload,
        &["runtimeId", "runtime_id", "sessionId", "session_id"],
    ) {
        return Ok(runtime_id);
    }
    let workspace_id = optional_string(payload, &["workspaceId", "workspace_id"])
        .ok_or("payload.runtimeId or payload.workspaceId is required")?;
    let pane_id = optional_string(payload, &["paneId", "pane_id"])
        .ok_or("payload.paneId is required with payload.workspaceId")?;
    let entity = app.upgrade().ok_or("native app dropped")?;
    cx.read_entity(&entity, |app, _| {
        app.runtime_for_pane(&workspace_id, &pane_id)
            .ok_or_else(|| format!("no runtime for pane: {workspace_id}/{pane_id}"))
    })
    .map_err(|error| format!("read native app: {error}"))?
}

fn target_result(payload: &Value, runtime_id: String, bytes_sent: usize) -> Value {
    let mut result = json!({
        "runtimeId": runtime_id,
        "bytesSent": bytes_sent,
    });
    if let Some(workspace_id) = optional_string(payload, &["workspaceId", "workspace_id"]) {
        result["workspaceId"] = json!(workspace_id);
    }
    if let Some(pane_id) = optional_string(payload, &["paneId", "pane_id"]) {
        result["paneId"] = json!(pane_id);
    }
    result
}

fn keystrokes_for_text(text: &str) -> Vec<Keystroke> {
    text.chars()
        .map(|character| match character {
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
                let text = other.to_string();
                Keystroke {
                    modifiers: Modifiers::default(),
                    key: text.clone(),
                    key_char: Some(text),
                }
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::{keystrokes_for_text, required_raw_string};
    use serde_json::json;

    #[test]
    fn pty_input_preserves_control_characters() {
        let text = required_raw_string(&json!({"text": "echo ready\r"}), "text").unwrap();

        assert_eq!(text, "echo ready\r");
    }

    #[test]
    fn ui_typing_encodes_newline_as_enter_key() {
        let keys = keystrokes_for_text("a\n\tb");

        assert_eq!(keys.len(), 4);
        assert_eq!(keys[1].key, "enter");
        assert_eq!(keys[1].key_char, None);
        assert_eq!(keys[2].key, "tab");
    }
}
