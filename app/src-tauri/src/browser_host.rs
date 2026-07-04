use base64::Engine;
use serde::{Deserialize, Serialize};
use serde_json::{json, Map, Value};
use std::collections::HashMap;
use std::io::Cursor;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::LazyLock;
use std::sync::{mpsc, Arc, Mutex};
use std::time::{Duration, Instant};
use tauri::ipc::{CommandArg, CommandItem, InvokeError};
use tauri::{AppHandle, LogicalPosition, LogicalSize, Manager, WebviewBuilder, WebviewUrl, Wry};

use crate::browser_alerts;

const CONTROL_TIMEOUT: Duration = Duration::from_secs(15);
const MAX_PAGE_ACTION_TIMEOUT: Duration = Duration::from_secs(120);
const PAGE_ACTION_TIMEOUT_MARGIN: Duration = Duration::from_secs(2);
const BROWSER_DATA_STORE_ID: [u8; 16] = *b"attn-browser-v1!";
const BROWSER_CONTENT_WORLD_NAME: &str = "attn-browser-control";
static NEXT_RUNTIME_RESULT_ID: AtomicU64 = AtomicU64::new(1);
static COOKIE_JAR_IO: LazyLock<Mutex<()>> = LazyLock::new(|| Mutex::new(()));
static COOKIE_SYNC_WORKERS: LazyLock<Mutex<HashMap<String, Arc<AtomicBool>>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));
static FOCUSED_BROWSER_LABEL: LazyLock<Mutex<Option<String>>> = LazyLock::new(|| Mutex::new(None));

pub(crate) struct TrustedMainWebview;

fn validate_browser_command_caller(label: &str) -> Result<(), String> {
    if label == "main" {
        Ok(())
    } else {
        Err("browser host commands are restricted to the main webview".into())
    }
}

impl<'de> CommandArg<'de, Wry> for TrustedMainWebview {
    fn from_command(command: CommandItem<'de, Wry>) -> Result<Self, InvokeError> {
        let webview = command.message.webview();
        validate_browser_command_caller(webview.label()).map_err(InvokeError::from)?;
        Ok(Self)
    }
}

pub fn focused_browser_label() -> Option<String> {
    FOCUSED_BROWSER_LABEL.lock().ok()?.clone()
}

fn mark_browser_focused(label: &str) {
    if let Ok(mut focused) = FOCUSED_BROWSER_LABEL.lock() {
        *focused = Some(label.to_string());
    }
}

fn clear_browser_focus_for(label: &str) {
    if let Ok(mut focused) = FOCUSED_BROWSER_LABEL.lock() {
        if focused.as_deref() == Some(label) {
            *focused = None;
        }
    }
}

#[tauri::command]
pub fn browser_host_clear_focus(_caller: TrustedMainWebview) {
    if let Ok(mut focused) = FOCUSED_BROWSER_LABEL.lock() {
        *focused = None;
    }
}

#[tauri::command]
pub fn browser_host_claim_focus(_caller: TrustedMainWebview, label: String) -> Result<(), String> {
    validate_label(&label)?;
    mark_browser_focused(&label);
    Ok(())
}

#[tauri::command]
pub fn browser_host_focus_state(_caller: TrustedMainWebview) -> Option<String> {
    focused_browser_label()
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct StoredCookie {
    name: String,
    value: String,
    domain: Option<String>,
    path: Option<String>,
    secure: bool,
    http_only: bool,
    same_site: Option<String>,
    expiry: Option<i64>,
}

fn validate_label(label: &str) -> Result<(), String> {
    if !label.starts_with("browser-")
        || !label
            .chars()
            .all(|ch| ch.is_ascii_alphanumeric() || ch == '-' || ch == '_')
    {
        return Err("invalid browser host label".into());
    }
    Ok(())
}

fn parse_url(raw: &str) -> Result<tauri::Url, String> {
    let url: tauri::Url = raw
        .parse()
        .map_err(|error| format!("invalid browser URL: {error}"))?;
    match url.scheme() {
        "http" | "https" => Ok(url),
        _ => Err("only http and https browser URLs are supported".into()),
    }
}

fn single_tab_script() -> &'static str {
    r#"(() => {
  const nativeDialogs = window.__ATTN_NATIVE_DIALOGS;
  if (nativeDialogs) {
    window.alert = nativeDialogs.alert;
    window.confirm = nativeDialogs.confirm;
    window.prompt = nativeDialogs.prompt;
  }
  const navigateInPlace = (raw) => {
    try {
      const url = new URL(String(raw), location.href);
      if (url.protocol === 'http:' || url.protocol === 'https:') location.assign(url.href);
    } catch {}
  };
  window.addEventListener('click', (event) => {
    if (event.defaultPrevented || event.button !== 0) return;
    const anchor = event.target?.closest?.('a[href][target]');
    if (!anchor || anchor.download) return;
    const target = anchor.getAttribute('target')?.toLowerCase();
    if (!target || target === '_self') return;
    event.preventDefault();
    navigateInPlace(anchor.href);
  }, true);
  const retargetForm = (form, submitter) => {
    const targetOwner = submitter?.hasAttribute?.('formtarget') ? submitter : form;
    const attribute = targetOwner === form ? 'target' : 'formtarget';
    const target = targetOwner.getAttribute(attribute)?.toLowerCase();
    if (!target || target === '_self') return () => {};
    const original = targetOwner.getAttribute(attribute);
    targetOwner.setAttribute(attribute, '_self');
    return () => {
      if (original === null) targetOwner.removeAttribute(attribute);
      else targetOwner.setAttribute(attribute, original);
    };
  };
  window.addEventListener('submit', (event) => {
    if (!(event.target instanceof HTMLFormElement)) return;
    const restore = retargetForm(event.target, event.submitter);
    window.setTimeout(restore, 0);
  }, true);
  const originalSubmit = HTMLFormElement.prototype.submit;
  HTMLFormElement.prototype.submit = function() {
    const restore = retargetForm(this, null);
    try {
      return originalSubmit.call(this);
    } finally {
      window.setTimeout(restore, 0);
    }
  };
  const originalOpen = window.open.bind(window);
  window.open = (url, target, features) => {
    if (url) {
      navigateInPlace(url);
      return window;
    }
    return originalOpen(url, target, features);
  };
})()"#
}

fn isolated_initialization_script() -> String {
    format!(
        r#"{}
(() => {{
  window.addEventListener('pointerdown', (event) => {{
    if (event.isTrusted) {{
      window.webkit?.messageHandlers?.attnBrowserFocus?.postMessage(null);
    }}
  }}, true);
  let lastLocation = location.href;
  window.setInterval(() => {{
    if (location.href === lastLocation) return;
    lastLocation = location.href;
    window.webkit?.messageHandlers?.attnBrowserLocation?.postMessage(null);
  }}, 100);
}})()"#,
        include_str!("../generated/browser-runtime.js"),
    )
}

fn report_location(app: &AppHandle, label: &str, url: &tauri::Url) {
    if !matches!(url.scheme(), "http" | "https") {
        return;
    }
    let Some(main_webview) = app.get_webview("main") else {
        return;
    };
    let Ok(detail) = serde_json::to_string(&json!({
        "label": label,
        "url": url.as_str(),
    })) else {
        return;
    };
    let _ = main_webview.eval(format!(
        "window.dispatchEvent(new CustomEvent('attn:browser-location', {{ detail: {detail} }}));"
    ));
}

fn content_layout_inset(
    window_height: f64,
    layout_x: f64,
    layout_y: f64,
    layout_height: f64,
) -> (f64, f64) {
    let left = layout_x.max(0.0);
    let top = (window_height - (layout_y + layout_height)).max(0.0);
    (left, top)
}

#[cfg(target_os = "macos")]
fn native_content_inset(window: &tauri::Window) -> (f64, f64) {
    use objc2_app_kit::NSWindow;

    let Ok(window_ptr) = window.ns_window() else {
        return (0.0, 0.0);
    };
    unsafe {
        let window: &NSWindow = &*window_ptr.cast();
        let frame = window.frame();
        let layout = window.contentLayoutRect();
        content_layout_inset(
            frame.size.height,
            layout.origin.x,
            layout.origin.y,
            layout.size.height,
        )
    }
}

#[cfg(not(target_os = "macos"))]
fn native_content_inset(_window: &tauri::Window) -> (f64, f64) {
    (0.0, 0.0)
}

fn native_position(window: &tauri::Window, x: f64, y: f64) -> LogicalPosition<f64> {
    let inset = native_content_inset(window);
    LogicalPosition::new((x + inset.0).max(0.0), (y + inset.1).max(0.0))
}

fn set_geometry(
    webview: &tauri::Webview,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
    visible: bool,
) -> Result<(), String> {
    if !visible {
        clear_browser_focus_for(webview.label());
    }
    let window = webview.window();
    webview
        .set_position(native_position(&window, x, y))
        .map_err(|error| format!("position browser webview: {error}"))?;
    webview
        .set_size(LogicalSize::new(width.max(1.0), height.max(1.0)))
        .map_err(|error| format!("resize browser webview: {error}"))?;
    if visible {
        webview.show()
    } else {
        webview.hide()
    }
    .map_err(|error| format!("change browser webview visibility: {error}"))
}

#[tauri::command]
pub async fn browser_host_mount(
    _caller: TrustedMainWebview,
    app: AppHandle,
    label: String,
    url: String,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
    visible: bool,
) -> Result<(), String> {
    validate_label(&label)?;
    let url = parse_url(&url)?;
    if let Some(webview) = app.get_webview(&label) {
        webview
            .navigate(url)
            .map_err(|error| format!("navigate browser webview: {error}"))?;
        return set_geometry(&webview, x, y, width, height, visible);
    }

    let window = app
        .get_window("main")
        .ok_or_else(|| "main window is unavailable".to_string())?;
    let popup_app = app.clone();
    let popup_label = label.clone();
    let page_load_app = app.clone();
    let page_load_label = label.clone();
    let initial_url = "about:blank"
        .parse()
        .map_err(|error| format!("create blank browser URL: {error}"))?;
    let builder = WebviewBuilder::new(label, WebviewUrl::External(initial_url))
        .data_store_identifier(BROWSER_DATA_STORE_ID)
        .background_throttling(tauri::utils::config::BackgroundThrottlingPolicy::Disabled)
        .initialization_script(single_tab_script())
        .on_navigation(|url| matches!(url.scheme(), "http" | "https" | "about"))
        .on_page_load(move |_webview, payload| {
            report_location(&page_load_app, &page_load_label, payload.url());
        })
        .on_new_window(move |url, _features| {
            if matches!(url.scheme(), "http" | "https") {
                let app = popup_app.clone();
                let label = popup_label.clone();
                std::thread::spawn(move || {
                    std::thread::sleep(Duration::from_millis(10));
                    if let Some(webview) = app.get_webview(&label) {
                        let _ = webview.navigate(url);
                    }
                });
            }
            tauri::webview::NewWindowResponse::Deny
        });
    let webview = window
        .add_child(
            builder,
            native_position(&window, x, y),
            LogicalSize::new(width.max(1.0), height.max(1.0)),
        )
        .map_err(|error| format!("create browser webview: {error}"))?;
    register_focus_handler(&webview)?;
    browser_alerts::register(&webview)?;
    restore_cookie_jar(&app, &webview)?;
    start_cookie_sync(app.clone(), webview.clone())?;
    webview
        .navigate(url)
        .map_err(|error| format!("navigate browser webview: {error}"))?;
    set_geometry(&webview, x, y, width, height, visible)
}

#[tauri::command]
pub fn browser_host_update(
    _caller: TrustedMainWebview,
    app: AppHandle,
    label: String,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
    visible: bool,
) -> Result<(), String> {
    validate_label(&label)?;
    let webview = app
        .get_webview(&label)
        .ok_or_else(|| "browser webview is not mounted".to_string())?;
    set_geometry(&webview, x, y, width, height, visible)
}

#[tauri::command]
pub fn browser_host_unmount(
    _caller: TrustedMainWebview,
    app: AppHandle,
    label: String,
) -> Result<(), String> {
    validate_label(&label)?;
    clear_browser_focus_for(&label);
    stop_cookie_sync(&label);
    browser_alerts::remove(&label);
    if let Some(webview) = app.get_webview(&label) {
        let persist_error = persist_cookie_jar(&app, &webview).err();
        let close_error = webview
            .close()
            .err()
            .map(|error| format!("close browser webview: {error}"));
        match (persist_error, close_error) {
            (Some(persist), Some(close)) => return Err(format!("{persist}; {close}")),
            (Some(persist), None) => return Err(persist),
            (None, Some(close)) => return Err(close),
            (None, None) => {}
        }
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn register_focus_handler(webview: &tauri::Webview) -> Result<(), String> {
    use objc2::ffi::{objc_setAssociatedObject, OBJC_ASSOCIATION_RETAIN_NONATOMIC};
    use objc2::rc::Retained;
    use objc2::runtime::ProtocolObject;
    use objc2::{define_class, msg_send, DefinedClass, MainThreadMarker, MainThreadOnly};
    use objc2_foundation::{NSObject, NSObjectProtocol, NSString};
    use objc2_web_kit::{
        WKContentWorld, WKScriptMessage, WKScriptMessageHandler, WKUserContentController,
        WKUserScript, WKUserScriptInjectionTime, WKWebView,
    };

    static BROWSER_HANDLER_KEY: u8 = 0;

    struct BrowserFocusHandlerIvars {
        label: String,
        app: AppHandle,
    }

    unsafe impl Send for BrowserFocusHandlerIvars {}
    unsafe impl Sync for BrowserFocusHandlerIvars {}

    define_class!(
        #[unsafe(super(NSObject))]
        #[thread_kind = MainThreadOnly]
        #[name = "AttnBrowserFocusHandler"]
        #[ivars = BrowserFocusHandlerIvars]
        struct BrowserFocusHandler;

        unsafe impl NSObjectProtocol for BrowserFocusHandler {}

        #[allow(non_snake_case)]
        unsafe impl WKScriptMessageHandler for BrowserFocusHandler {
            #[unsafe(method(userContentController:didReceiveScriptMessage:))]
            fn userContentController_didReceiveScriptMessage(
                &self,
                _user_content_controller: &WKUserContentController,
                message: &WKScriptMessage,
            ) {
                let ivars = self.ivars();
                let name = unsafe { message.name() }.to_string();
                if name == "attnBrowserFocus" {
                    mark_browser_focused(&ivars.label);
                    return;
                }
                if name == "attnBrowserLocation" {
                    if let Some(webview) = ivars.app.get_webview(&ivars.label) {
                        if let Ok(url) = webview.url() {
                            report_location(&ivars.app, &ivars.label, &url);
                        }
                    }
                }
            }
        }
    );

    impl BrowserFocusHandler {
        unsafe fn new(label: String, app: AppHandle) -> Retained<Self> {
            let this = Self::alloc(MainThreadMarker::new_unchecked());
            let this = this.set_ivars(BrowserFocusHandlerIvars { label, app });
            msg_send![super(this), init]
        }
    }

    let label = webview.label().to_string();
    let app = webview.app_handle().clone();
    webview
        .with_webview(move |platform| unsafe {
            let view: &WKWebView = &*platform.inner().cast();
            let handler = BrowserFocusHandler::new(label, app);
            let protocol: Retained<ProtocolObject<dyn WKScriptMessageHandler>> =
                ProtocolObject::from_retained(handler);
            let controller = view.configuration().userContentController();
            let mtm = MainThreadMarker::new_unchecked();
            let world =
                WKContentWorld::worldWithName(&NSString::from_str(BROWSER_CONTENT_WORLD_NAME), mtm);
            controller.addScriptMessageHandler_contentWorld_name(
                &protocol,
                &world,
                &NSString::from_str("attnBrowserFocus"),
            );
            controller.addScriptMessageHandler_contentWorld_name(
                &protocol,
                &world,
                &NSString::from_str("attnBrowserLocation"),
            );
            let user_script =
                WKUserScript::initWithSource_injectionTime_forMainFrameOnly_inContentWorld(
                    WKUserScript::alloc(mtm),
                    &NSString::from_str(&isolated_initialization_script()),
                    WKUserScriptInjectionTime::AtDocumentStart,
                    false,
                    &world,
                );
            controller.addUserScript(&user_script);
            objc_setAssociatedObject(
                std::ptr::from_ref(view).cast_mut().cast(),
                std::ptr::addr_of!(BROWSER_HANDLER_KEY).cast(),
                Retained::into_raw(protocol).cast(),
                OBJC_ASSOCIATION_RETAIN_NONATOMIC,
            );
        })
        .map_err(|error| format!("register browser focus handler: {error}"))
}

#[cfg(not(target_os = "macos"))]
fn register_focus_handler(_webview: &tauri::Webview) -> Result<(), String> {
    Ok(())
}

fn control_params(
    params: Option<String>,
    selector: Option<String>,
    text: Option<String>,
) -> Result<Map<String, Value>, String> {
    let mut value = match params {
        Some(raw) => serde_json::from_str::<Value>(&raw)
            .map_err(|error| format!("decode browser params: {error}"))?,
        None => json!({}),
    };
    let object = value
        .as_object_mut()
        .ok_or_else(|| "browser params must be a JSON object".to_string())?;
    if let Some(selector) = selector {
        object
            .entry("selector".to_string())
            .or_insert(Value::String(selector));
    }
    if let Some(text) = text {
        object
            .entry("text".to_string())
            .or_insert(Value::String(text));
    }
    Ok(object.clone())
}

fn page_action_script(
    action: &str,
    params: &Map<String, Value>,
    result_id: &str,
) -> Result<String, String> {
    let action =
        serde_json::to_string(action).map_err(|error| format!("encode browser action: {error}"))?;
    let params =
        serde_json::to_string(params).map_err(|error| format!("encode browser params: {error}"))?;
    let result_id = serde_json::to_string(result_id)
        .map_err(|error| format!("encode browser result id: {error}"))?;
    Ok(format!(
        r#"(() => {{
  try {{
    if (!window.__attnBrowser) throw new Error('attn browser runtime is unavailable');
    const value = window.__attnBrowser.execute({action}, {params});
    if (value && typeof value.then === 'function') {{
      window.__attnBrowserResults ||= {{}};
      value.then(
        resolved => window.__attnBrowserResults[{result_id}] = JSON.stringify({{ success: true, value: resolved }}),
        error => window.__attnBrowserResults[{result_id}] = JSON.stringify({{ success: false, error: String(error?.message || error) }})
      );
      return JSON.stringify({{ pending: {result_id} }});
    }}
    return JSON.stringify({{ success: true, value }});
  }} catch (error) {{
    return JSON.stringify({{ success: false, error: String(error?.message || error) }});
  }}
}})()"#
    ))
}

fn page_action_timeout(params: &Map<String, Value>) -> Duration {
    let requested = params
        .get("timeout")
        .and_then(Value::as_f64)
        .filter(|milliseconds| *milliseconds >= 0.0)
        .map(|milliseconds| Duration::from_secs_f64(milliseconds / 1_000.0))
        .unwrap_or(CONTROL_TIMEOUT);
    requested
        .min(MAX_PAGE_ACTION_TIMEOUT)
        .saturating_add(PAGE_ACTION_TIMEOUT_MARGIN)
        .max(CONTROL_TIMEOUT)
}

async fn run_page_action(
    webview: tauri::Webview,
    action: &str,
    params: &Map<String, Value>,
) -> Result<String, String> {
    ensure_runtime(webview.clone()).await?;
    let alert_state = browser_alerts::state(webview.label());
    let result_id = format!(
        "attn-result-{}",
        NEXT_RUNTIME_RESULT_ID.fetch_add(1, Ordering::Relaxed)
    );
    let Some(mut raw) = evaluate_isolated_script_with_alert(
        webview.clone(),
        page_action_script(action, params, &result_id)?,
        Some(alert_state.clone()),
    )
    .await?
    else {
        return Ok("null".to_string());
    };
    let deadline = Instant::now() + page_action_timeout(params);
    let envelope = loop {
        let envelope: Value = serde_json::from_str(&raw)
            .map_err(|error| format!("decode browser runtime result: {error}"))?;
        if envelope.get("pending").and_then(Value::as_str).is_none() {
            break envelope;
        }
        if Instant::now() >= deadline {
            return Err("timed out waiting for browser runtime action".to_string());
        }
        std::thread::sleep(Duration::from_millis(50));
        let encoded_id = serde_json::to_string(&result_id)
            .map_err(|error| format!("encode browser result id: {error}"))?;
        let Some(next) = evaluate_isolated_script_with_alert(
            webview.clone(),
            format!(
                r#"(() => {{
  const id = {encoded_id};
  const result = window.__attnBrowserResults?.[id];
  if (!result) return JSON.stringify({{ pending: id }});
  delete window.__attnBrowserResults[id];
  return result;
}})()"#
            ),
            Some(alert_state.clone()),
        )
        .await?
        else {
            return Ok("null".to_string());
        };
        raw = next;
    };
    if envelope.get("success").and_then(Value::as_bool) != Some(true) {
        return Err(envelope
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("browser runtime action failed")
            .to_string());
    }
    serde_json::to_string(envelope.get("value").unwrap_or(&Value::Null))
        .map_err(|error| format!("encode browser runtime result: {error}"))
}

async fn ensure_runtime(webview: tauri::Webview) -> Result<(), String> {
    let installed = evaluate_isolated_script(
        webview.clone(),
        "JSON.stringify(Boolean(window.__attnBrowser))".to_string(),
    )
    .await?;
    if installed == "true" {
        return Ok(());
    }
    let bundle = include_str!("../generated/browser-runtime.js");
    let script = format!(
        r#"(() => {{
  try {{
    {bundle}
    return JSON.stringify({{ success: Boolean(window.__attnBrowser) }});
  }} catch (error) {{
    return JSON.stringify({{ success: false, error: `${{String(error?.message || error)}}\n${{String(error?.stack || '')}}` }});
  }}
}})()"#
    );
    let raw = evaluate_isolated_script(webview, script).await?;
    let result: Value = serde_json::from_str(&raw)
        .map_err(|error| format!("decode browser runtime installation result: {error}"))?;
    if result.get("success").and_then(Value::as_bool) == Some(true) {
        Ok(())
    } else {
        Err(result
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("attn browser runtime failed to install")
            .to_string())
    }
}

#[cfg(target_os = "macos")]
async fn evaluate_isolated_script(
    webview: tauri::Webview,
    script: String,
) -> Result<String, String> {
    evaluate_isolated_script_with_alert(webview, script, None)
        .await?
        .ok_or_else(|| "browser alert interrupted internal script".to_string())
}

#[cfg(target_os = "macos")]
async fn evaluate_isolated_script_with_alert(
    webview: tauri::Webview,
    script: String,
    alert_state: Option<Arc<browser_alerts::AlertState>>,
) -> Result<Option<String>, String> {
    use block2::RcBlock;
    use objc2::runtime::AnyObject;
    use objc2::MainThreadMarker;
    use objc2_foundation::{NSError, NSString};
    use objc2_web_kit::{WKContentWorld, WKWebView};

    let (tx, rx) = mpsc::channel::<Result<String, String>>();
    webview
        .with_webview(move |platform| unsafe {
            let view: &WKWebView = &*platform.inner().cast();
            let world = WKContentWorld::worldWithName(
                &NSString::from_str(BROWSER_CONTENT_WORLD_NAME),
                MainThreadMarker::new_unchecked(),
            );
            let tx = Mutex::new(Some(tx));
            let handler = RcBlock::new(move |value: *mut AnyObject, error: *mut NSError| {
                let result = if !error.is_null() {
                    Err((&*error).localizedDescription().to_string())
                } else if value.is_null() {
                    Err("browser script returned no result".to_string())
                } else {
                    Ok((&*value.cast::<NSString>()).to_string())
                };
                if let Ok(mut sender) = tx.lock() {
                    if let Some(sender) = sender.take() {
                        let _ = sender.send(result);
                    }
                }
            });
            view.evaluateJavaScript_inFrame_inContentWorld_completionHandler(
                &NSString::from_str(&script),
                None,
                &world,
                Some(&handler),
            );
        })
        .map_err(|error| format!("evaluate isolated browser script: {error}"))?;

    tauri::async_runtime::spawn_blocking(move || {
        let deadline = Instant::now() + CONTROL_TIMEOUT;
        loop {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err("timed out waiting for isolated browser script result".to_string());
            }
            match rx.recv_timeout(remaining.min(Duration::from_millis(25))) {
                Ok(result) => return result.map(Some),
                Err(mpsc::RecvTimeoutError::Timeout) => {
                    if alert_state
                        .as_ref()
                        .is_some_and(|state| state.message().is_some())
                    {
                        return Ok(None);
                    }
                }
                Err(mpsc::RecvTimeoutError::Disconnected) => {
                    return Err("isolated browser script result channel closed".to_string());
                }
            }
        }
    })
    .await
    .map_err(|error| format!("join isolated browser script result: {error}"))?
}

#[cfg(not(target_os = "macos"))]
async fn evaluate_isolated_script(
    webview: tauri::Webview,
    script: String,
) -> Result<String, String> {
    evaluate_script(webview, script).await
}

#[cfg(not(target_os = "macos"))]
async fn evaluate_isolated_script_with_alert(
    webview: tauri::Webview,
    script: String,
    _alert_state: Option<Arc<browser_alerts::AlertState>>,
) -> Result<Option<String>, String> {
    evaluate_script(webview, script).await.map(Some)
}

#[cfg(not(target_os = "macos"))]
async fn evaluate_script(webview: tauri::Webview, script: String) -> Result<String, String> {
    let (tx, rx) = mpsc::channel::<Result<String, String>>();
    webview
        .eval_with_callback(script, move |raw| {
            let result = serde_json::from_str::<String>(&raw)
                .map_err(|error| format!("browser script returned an invalid result: {error}"));
            let _ = tx.send(result);
        })
        .map_err(|error| format!("evaluate browser script: {error}"))?;

    tauri::async_runtime::spawn_blocking(move || {
        rx.recv_timeout(CONTROL_TIMEOUT)
            .map_err(|_| "timed out waiting for browser script result".to_string())?
    })
    .await
    .map_err(|error| format!("join browser script result: {error}"))?
}

#[derive(Debug, Deserialize)]
struct SnapshotRect {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[cfg(target_os = "macos")]
async fn screenshot(
    webview: tauri::Webview,
    snapshot_rect: Option<SnapshotRect>,
) -> Result<String, String> {
    use block2::RcBlock;
    use objc2::MainThreadMarker;
    use objc2_app_kit::NSImage;
    use objc2_core_foundation::{CGPoint, CGRect, CGSize};
    use objc2_foundation::NSError;
    use objc2_web_kit::{WKSnapshotConfiguration, WKWebView};
    use std::ptr::NonNull;

    let (tx, rx) = mpsc::channel::<Result<Vec<u8>, String>>();
    webview
        .with_webview(move |platform| unsafe {
            let view: &WKWebView = &*platform.inner().cast();
            let configuration = snapshot_rect.map(|rect| {
                let configuration = WKSnapshotConfiguration::new(MainThreadMarker::new_unchecked());
                configuration.setRect(CGRect::new(
                    CGPoint::new(rect.x, rect.y),
                    CGSize::new(rect.width, rect.height),
                ));
                configuration
            });
            let tx = Mutex::new(Some(tx));
            let handler = RcBlock::new(move |image: *mut NSImage, error: *mut NSError| {
                let result = if !error.is_null() {
                    Err((&*error).localizedDescription().to_string())
                } else if image.is_null() {
                    Err("browser screenshot returned no image".to_string())
                } else if let Some(data) = (&*image).TIFFRepresentation() {
                    let length = data.length();
                    let mut bytes = vec![0_u8; length];
                    if length > 0 {
                        data.getBytes_length(
                            NonNull::new_unchecked(bytes.as_mut_ptr().cast()),
                            length,
                        );
                    }
                    Ok(bytes)
                } else {
                    Err("browser screenshot could not be encoded".to_string())
                };
                if let Some(tx) = tx.lock().ok().and_then(|mut tx| tx.take()) {
                    let _ = tx.send(result);
                }
            });
            view.takeSnapshotWithConfiguration_completionHandler(
                configuration.as_deref(),
                &handler,
            );
        })
        .map_err(|error| format!("capture browser screenshot: {error}"))?;

    let tiff = tauri::async_runtime::spawn_blocking(move || {
        rx.recv_timeout(CONTROL_TIMEOUT)
            .map_err(|_| "timed out waiting for browser screenshot".to_string())?
    })
    .await
    .map_err(|error| format!("join browser screenshot result: {error}"))??;

    tauri::async_runtime::spawn_blocking(move || {
        let image = image::load_from_memory_with_format(&tiff, image::ImageFormat::Tiff)
            .map_err(|error| format!("decode browser screenshot: {error}"))?;
        if image.width() <= 1 || image.height() <= 1 {
            return Err(
                "browser screenshot is unavailable while the browser panel is hidden; select its workspace and retry"
                    .to_string(),
            );
        }
        let mut png = Cursor::new(Vec::new());
        image
            .write_to(&mut png, image::ImageFormat::Png)
            .map_err(|error| format!("encode browser screenshot: {error}"))?;
        Ok(base64::engine::general_purpose::STANDARD.encode(png.into_inner()))
    })
    .await
    .map_err(|error| format!("join browser screenshot encoder: {error}"))?
}

#[cfg(not(target_os = "macos"))]
async fn screenshot(
    _webview: tauri::Webview,
    _snapshot_rect: Option<SnapshotRect>,
) -> Result<String, String> {
    Err("the in-app browser host is only supported on macOS".into())
}

#[cfg(target_os = "macos")]
async fn print_page(
    webview: tauri::Webview,
    params: &Map<String, Value>,
) -> Result<String, String> {
    use block2::RcBlock;
    use objc2::MainThreadMarker;
    use objc2_foundation::{NSData, NSError};
    use objc2_web_kit::{WKPDFConfiguration, WKWebView};

    let page = params.get("page").and_then(Value::as_object);
    let margin = params.get("margin").and_then(Value::as_object);
    let number = |source: Option<&Map<String, Value>>, key: &str, fallback: f64| {
        source
            .and_then(|value| value.get(key))
            .and_then(Value::as_f64)
            .unwrap_or(fallback)
    };
    let width = number(page, "width", 21.0);
    let height = number(page, "height", 29.7);
    let top = number(margin, "top", 1.0);
    let right = number(margin, "right", 1.0);
    let bottom = number(margin, "bottom", 1.0);
    let left = number(margin, "left", 1.0);
    let (page_width, page_height) = page_size(width, height, params);
    let css = format!(
        r#"(() => {{
  document.getElementById('__attn_print_style')?.remove();
  const style = document.createElement('style');
  style.id = '__attn_print_style';
  style.textContent = `@page {{ size: {page_width}cm {page_height}cm; margin: {top}cm {right}cm {bottom}cm {left}cm; }} @media print {{ body {{ -webkit-print-color-adjust: exact; print-color-adjust: exact; }} }}`;
  document.head.appendChild(style);
}})()"#
    );
    webview
        .eval(css)
        .map_err(|error| format!("prepare browser PDF: {error}"))?;

    let (tx, rx) = mpsc::channel::<Result<Vec<u8>, String>>();
    webview
        .with_webview(move |platform| unsafe {
            let view: &WKWebView = &*platform.inner().cast();
            let config = WKPDFConfiguration::new(MainThreadMarker::new_unchecked());
            let tx = Mutex::new(Some(tx));
            let handler = RcBlock::new(move |data: *mut NSData, error: *mut NSError| {
                let result = if !error.is_null() {
                    Err((&*error).localizedDescription().to_string())
                } else if data.is_null() {
                    Err("browser PDF returned no data".to_string())
                } else {
                    Ok((&*data).to_vec())
                };
                if let Some(tx) = tx.lock().ok().and_then(|mut tx| tx.take()) {
                    let _ = tx.send(result);
                }
            });
            view.createPDFWithConfiguration_completionHandler(Some(&config), &handler);
        })
        .map_err(|error| format!("create browser PDF: {error}"))?;

    let result = tauri::async_runtime::spawn_blocking(move || {
        rx.recv_timeout(CONTROL_TIMEOUT)
            .map_err(|_| "timed out waiting for browser PDF".to_string())?
    })
    .await
    .map_err(|error| format!("join browser PDF result: {error}"))?;
    let _ = webview.eval("document.getElementById('__attn_print_style')?.remove()");
    result.map(|bytes| base64::engine::general_purpose::STANDARD.encode(bytes))
}

fn page_size(width: f64, height: f64, params: &Map<String, Value>) -> (f64, f64) {
    if params.get("orientation").and_then(Value::as_str) == Some("landscape") {
        (height, width)
    } else {
        (width, height)
    }
}

#[cfg(not(target_os = "macos"))]
async fn print_page(
    _webview: tauri::Webview,
    _params: &Map<String, Value>,
) -> Result<String, String> {
    Err("browser PDF is only supported on macOS".into())
}

fn cookie_value(cookie: &tauri::webview::Cookie<'_>) -> Value {
    json!({
        "name": cookie.name(),
        "value": cookie.value(),
        "path": cookie.path().unwrap_or("/"),
        "domain": cookie.domain(),
        "secure": cookie.secure().unwrap_or(false),
        "httpOnly": cookie.http_only().unwrap_or(false),
        "sameSite": cookie.same_site().map(|value| format!("{value:?}")),
        "expiry": cookie.expires_datetime().map(|value| value.unix_timestamp()),
    })
}

fn stored_cookie(cookie: &tauri::webview::Cookie<'_>) -> StoredCookie {
    StoredCookie {
        name: cookie.name().to_string(),
        value: cookie.value().to_string(),
        domain: cookie.domain().map(str::to_string),
        path: cookie.path().map(str::to_string),
        secure: cookie.secure().unwrap_or(false),
        http_only: cookie.http_only().unwrap_or(false),
        same_site: cookie.same_site().map(|value| format!("{value:?}")),
        expiry: cookie
            .expires_datetime()
            .map(|value| value.unix_timestamp()),
    }
}

fn cookie_from_stored(stored: StoredCookie) -> Result<tauri::webview::Cookie<'static>, String> {
    let mut cookie = tauri::webview::Cookie::build((stored.name, stored.value));
    if let Some(domain) = stored.domain {
        cookie = cookie.domain(domain);
    }
    if let Some(path) = stored.path {
        cookie = cookie.path(path);
    }
    if stored.secure {
        cookie = cookie.secure(true);
    }
    if stored.http_only {
        cookie = cookie.http_only(true);
    }
    if let Some(same_site) = stored.same_site {
        let same_site = match same_site.to_ascii_lowercase().as_str() {
            "strict" => tauri::webview::cookie::SameSite::Strict,
            "none" => tauri::webview::cookie::SameSite::None,
            _ => tauri::webview::cookie::SameSite::Lax,
        };
        cookie = cookie.same_site(same_site);
    }
    if let Some(expiry) = stored.expiry {
        let expiry = tauri::webview::cookie::time::OffsetDateTime::from_unix_timestamp(expiry)
            .map_err(|error| format!("invalid stored cookie expiry: {error}"))?;
        cookie = cookie.expires(expiry);
    }
    Ok(cookie.build())
}

fn cookie_jar_path(app: &AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_local_data_dir()
        .map(|path| path.join("browser").join("cookies.json"))
        .map_err(|error| format!("resolve browser cookie jar path: {error}"))
}

fn persist_cookie_jar(app: &AppHandle, webview: &tauri::Webview) -> Result<(), String> {
    let _guard = COOKIE_JAR_IO
        .lock()
        .map_err(|_| "browser cookie jar lock poisoned".to_string())?;
    let path = cookie_jar_path(app)?;
    let cookies = webview
        .cookies()
        .map_err(|error| format!("read browser cookie jar: {error}"))?
        .iter()
        .map(stored_cookie)
        .collect::<Vec<_>>();
    let data = serde_json::to_vec_pretty(&cookies)
        .map_err(|error| format!("encode browser cookie jar: {error}"))?;
    let directory = path
        .parent()
        .ok_or_else(|| "browser cookie jar directory is unavailable".to_string())?;
    std::fs::create_dir_all(directory)
        .map_err(|error| format!("create browser cookie jar directory: {error}"))?;
    let temporary = path.with_extension("json.tmp");
    std::fs::write(&temporary, data)
        .map_err(|error| format!("write browser cookie jar: {error}"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&temporary, std::fs::Permissions::from_mode(0o600))
            .map_err(|error| format!("secure browser cookie jar: {error}"))?;
    }
    std::fs::rename(&temporary, &path)
        .map_err(|error| format!("replace browser cookie jar: {error}"))
}

fn restore_cookie_jar(app: &AppHandle, webview: &tauri::Webview) -> Result<(), String> {
    let _guard = COOKIE_JAR_IO
        .lock()
        .map_err(|_| "browser cookie jar lock poisoned".to_string())?;
    let path = cookie_jar_path(app)?;
    let data = match std::fs::read(&path) {
        Ok(data) => data,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(format!("read browser cookie jar: {error}")),
    };
    let stored = match serde_json::from_slice::<Vec<StoredCookie>>(&data) {
        Ok(stored) => stored,
        Err(_) => {
            let _ = std::fs::rename(&path, path.with_extension("json.corrupt"));
            return Ok(());
        }
    };
    for cookie in stored {
        webview
            .set_cookie(cookie_from_stored(cookie)?)
            .map_err(|error| format!("restore browser cookie: {error}"))?;
    }
    Ok(())
}

fn register_cookie_sync_worker(label: &str) -> Result<Arc<AtomicBool>, String> {
    let cancelled = Arc::new(AtomicBool::new(false));
    let mut workers = COOKIE_SYNC_WORKERS
        .lock()
        .map_err(|_| "browser cookie sync lock poisoned".to_string())?;
    if let Some(previous) = workers.insert(label.to_string(), cancelled.clone()) {
        previous.store(true, Ordering::Release);
    }
    Ok(cancelled)
}

fn unregister_cookie_sync_worker(label: &str, cancelled: &Arc<AtomicBool>) {
    let Ok(mut workers) = COOKIE_SYNC_WORKERS.lock() else {
        return;
    };
    if workers
        .get(label)
        .is_some_and(|current| Arc::ptr_eq(current, cancelled))
    {
        workers.remove(label);
    }
}

fn stop_cookie_sync(label: &str) {
    let Ok(mut workers) = COOKIE_SYNC_WORKERS.lock() else {
        return;
    };
    if let Some(cancelled) = workers.remove(label) {
        cancelled.store(true, Ordering::Release);
    }
}

fn start_cookie_sync(app: AppHandle, webview: tauri::Webview) -> Result<(), String> {
    let label = webview.label().to_string();
    let cancelled = register_cookie_sync_worker(&label)?;
    std::thread::spawn(move || {
        loop {
            std::thread::sleep(Duration::from_secs(2));
            if cancelled.load(Ordering::Acquire) {
                break;
            }
            let _ = persist_cookie_jar(&app, &webview);
        }
        unregister_cookie_sync_worker(&label, &cancelled);
    });
    Ok(())
}

fn cookie_domain_matches(host: &str, domain: Option<&str>) -> bool {
    let Some(raw_domain) = domain else {
        return false;
    };
    let host = host.to_ascii_lowercase();
    let domain = raw_domain.trim_start_matches('.').to_ascii_lowercase();
    // cookie::Cookie normalizes away a leading dot, including for cookies
    // converted from WKWebView. Apply RFC domain matching to the normalized
    // value rather than using the dot as the domain-cookie signal.
    !domain.is_empty() && (host == domain || host.ends_with(&format!(".{domain}")))
}

fn cookie_domain_can_be_set_from_host(host: &str, raw_domain: &str) -> bool {
    let host = host.to_ascii_lowercase();
    let domain = raw_domain.trim_start_matches('.').to_ascii_lowercase();
    !domain.is_empty() && (host == domain || host.ends_with(&format!(".{domain}")))
}

fn cookie_path_matches(request_path: &str, cookie_path: Option<&str>) -> bool {
    let cookie_path = cookie_path.filter(|path| !path.is_empty()).unwrap_or("/");
    request_path == cookie_path
        || (request_path.starts_with(cookie_path)
            && (cookie_path.ends_with('/')
                || request_path.as_bytes().get(cookie_path.len()) == Some(&b'/')))
}

fn current_cookies(
    webview: &tauri::Webview,
) -> Result<Vec<tauri::webview::Cookie<'static>>, String> {
    let url = webview
        .url()
        .map_err(|error| format!("read browser URL: {error}"))?;
    let host = url.host_str().unwrap_or_default().to_ascii_lowercase();
    let path = url.path();
    let secure = url.scheme() == "https";
    webview
        .cookies()
        .map(|cookies| {
            cookies
                .into_iter()
                .filter(|cookie| {
                    let domain_matches = cookie_domain_matches(&host, cookie.domain());
                    let path_matches = cookie_path_matches(path, cookie.path());
                    let secure_matches = !cookie.secure().unwrap_or(false) || secure;
                    domain_matches && path_matches && secure_matches
                })
                .collect()
        })
        .map_err(|error| format!("read browser cookies: {error}"))
}

fn string_field<'a>(params: &'a Map<String, Value>, key: &str) -> Result<&'a str, String> {
    params
        .get(key)
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| format!("{key} must be a non-empty string"))
}

fn add_cookie(webview: &tauri::Webview, params: &Map<String, Value>) -> Result<String, String> {
    let source = params
        .get("cookie")
        .and_then(Value::as_object)
        .unwrap_or(params);
    let name = string_field(source, "name")?;
    let value = source.get("value").and_then(Value::as_str).unwrap_or("");
    let current_host = webview
        .url()
        .map_err(|error| format!("read browser URL: {error}"))?
        .host_str()
        .map(str::to_string)
        .ok_or_else(|| "current browser URL has no host".to_string())?;
    let mut cookie = tauri::webview::Cookie::build((name.to_string(), value.to_string()));
    if let Some(domain) = source.get("domain").and_then(Value::as_str) {
        if !cookie_domain_can_be_set_from_host(&current_host, domain) {
            return Err("cookie domain does not match the current browser host".to_string());
        }
        cookie = cookie.domain(domain.to_string());
    } else {
        cookie = cookie.domain(current_host);
    }
    cookie = cookie.path(
        source
            .get("path")
            .and_then(Value::as_str)
            .unwrap_or("/")
            .to_string(),
    );
    if source.get("secure").and_then(Value::as_bool) == Some(true) {
        cookie = cookie.secure(true);
    }
    if source.get("httpOnly").and_then(Value::as_bool) == Some(true) {
        cookie = cookie.http_only(true);
    }
    if let Some(same_site) = source.get("sameSite").and_then(Value::as_str) {
        let same_site = match same_site.to_ascii_lowercase().as_str() {
            "strict" => tauri::webview::cookie::SameSite::Strict,
            "none" => tauri::webview::cookie::SameSite::None,
            _ => tauri::webview::cookie::SameSite::Lax,
        };
        cookie = cookie.same_site(same_site);
    }
    if let Some(expiry) = source.get("expiry").and_then(Value::as_i64) {
        let expiry = tauri::webview::cookie::time::OffsetDateTime::from_unix_timestamp(expiry)
            .map_err(|error| format!("invalid cookie expiry: {error}"))?;
        cookie = cookie.expires(expiry);
    }
    webview
        .set_cookie(cookie.build())
        .map_err(|error| format!("set browser cookie: {error}"))?;
    Ok("null".to_string())
}

fn delete_cookie(webview: &tauri::Webview, name: Option<&str>) -> Result<String, String> {
    for cookie in current_cookies(webview)? {
        if name.is_none_or(|name| cookie.name() == name) {
            webview
                .delete_cookie(cookie)
                .map_err(|error| format!("delete browser cookie: {error}"))?;
        }
    }
    Ok("null".to_string())
}

#[tauri::command]
pub async fn browser_host_control(
    _caller: TrustedMainWebview,
    app: AppHandle,
    label: String,
    action: String,
    params: Option<String>,
    selector: Option<String>,
    text: Option<String>,
) -> Result<String, String> {
    validate_label(&label)?;
    let webview = app
        .get_webview(&label)
        .ok_or_else(|| "browser webview is not mounted".to_string())?;
    let params = control_params(params, selector, text)?;
    let result = match action.as_str() {
        "get_alert_text" => serde_json::to_string(
            &browser_alerts::state(&label)
                .message()
                .ok_or_else(|| "no such alert".to_string())?,
        )
        .map_err(|error| format!("encode browser alert text: {error}")),
        "send_alert_text" => {
            let text = string_field(&params, "text")?;
            if !browser_alerts::state(&label).set_prompt_input(text.to_string()) {
                return Err("no prompt alert is open".to_string());
            }
            Ok("null".to_string())
        }
        "accept_alert" => {
            let state = browser_alerts::state(&label);
            let prompt = state.prompt_input().or_else(|| state.default_text());
            if !state.respond(true, prompt) {
                return Err("no such alert".to_string());
            }
            Ok("null".to_string())
        }
        "dismiss_alert" => {
            if !browser_alerts::state(&label).respond(false, None) {
                return Err("no such alert".to_string());
            }
            Ok("null".to_string())
        }
        "reload" => {
            webview
                .reload()
                .map_err(|error| format!("reload browser webview: {error}"))?;
            Ok(r#"{"success":true}"#.to_string())
        }
        "navigate" => {
            let url = string_field(&params, "url")
                .or_else(|_| string_field(&params, "text"))
                .and_then(parse_url)?;
            webview
                .navigate(url)
                .map_err(|error| format!("navigate browser webview: {error}"))?;
            Ok(r#"{"success":true}"#.to_string())
        }
        "screenshot" => screenshot(webview.clone(), None).await,
        "print_page" => print_page(webview.clone(), &params).await,
        "element_screenshot" => {
            let rect =
                run_page_action(webview.clone(), "get_element_screenshot_rect", &params).await?;
            let rect = serde_json::from_str(&rect)
                .map_err(|error| format!("decode element screenshot rectangle: {error}"))?;
            screenshot(webview.clone(), Some(rect)).await
        }
        "get_all_cookies" => serde_json::to_string(
            &current_cookies(&webview)?
                .iter()
                .map(cookie_value)
                .collect::<Vec<_>>(),
        )
        .map_err(|error| format!("encode browser cookies: {error}")),
        "get_cookie" => {
            let name = string_field(&params, "name")?;
            let cookie = current_cookies(&webview)?
                .iter()
                .find(|cookie| cookie.name() == name)
                .map(cookie_value)
                .unwrap_or(Value::Null);
            serde_json::to_string(&cookie)
                .map_err(|error| format!("encode browser cookie: {error}"))
        }
        "add_cookie" => add_cookie(&webview, &params),
        "delete_cookie" => delete_cookie(&webview, Some(string_field(&params, "name")?)),
        "delete_all_cookies" => delete_cookie(&webview, None),
        "get_window_handle" => serde_json::to_string(&label)
            .map_err(|error| format!("encode browser window handle: {error}")),
        "get_window_handles" => serde_json::to_string(&[label])
            .map_err(|error| format!("encode browser window handles: {error}")),
        _ => {
            let alert_state = browser_alerts::state(&label);
            alert_state.request_capture();
            let result = run_page_action(webview.clone(), &action, &params).await;
            alert_state.clear_capture_if_idle();
            result
        }
    };
    if result.is_ok() {
        if let Err(error) = persist_cookie_jar(&app, &webview) {
            eprintln!("[BrowserHost] Failed to persist cookies after {action}: {error}");
        }
    }
    result
}

#[cfg(test)]
mod tests {
    use super::{
        browser_host_claim_focus, browser_host_clear_focus, clear_browser_focus_for,
        content_layout_inset, cookie_domain_can_be_set_from_host, cookie_domain_matches,
        cookie_from_stored, cookie_path_matches, focused_browser_label,
        isolated_initialization_script, page_action_timeout, page_size,
        register_cookie_sync_worker, single_tab_script, stop_cookie_sync,
        validate_browser_command_caller, StoredCookie, TrustedMainWebview,
    };
    use serde_json::{json, Map};
    use std::sync::atomic::Ordering;
    use std::time::Duration;

    fn session_cookie() -> StoredCookie {
        StoredCookie {
            name: "session".to_string(),
            value: "value".to_string(),
            domain: Some("127.0.0.1".to_string()),
            path: Some("/".to_string()),
            secure: false,
            http_only: false,
            same_site: Some("Lax".to_string()),
            expiry: None,
        }
    }

    #[test]
    fn restored_false_cookie_flags_are_omitted() {
        let cookie = cookie_from_stored(session_cookie()).expect("restore cookie");

        assert_eq!(cookie.secure(), None);
        assert_eq!(cookie.http_only(), None);
        assert_eq!(cookie.expires_datetime(), None);
    }

    #[test]
    fn stored_session_cookie_round_trips_through_json() {
        let encoded = serde_json::to_string(&session_cookie()).expect("encode cookie");
        let decoded: StoredCookie = serde_json::from_str(&encoded).expect("decode cookie");

        assert!(!decoded.secure);
        assert!(!decoded.http_only);
        assert_eq!(decoded.expiry, None);
        assert_eq!(decoded.same_site.as_deref(), Some("Lax"));
    }

    #[test]
    fn normalized_domain_cookies_match_subdomains() {
        let cookie = tauri::webview::Cookie::build(("session", "value"))
            .domain(".example.com")
            .build();
        assert_eq!(cookie.domain(), Some("example.com"));
        assert!(cookie_domain_matches("sub.example.com", cookie.domain()));

        assert!(cookie_domain_matches("example.com", Some("example.com")));
        assert!(cookie_domain_matches(
            "sub.example.com",
            Some("example.com")
        ));
        assert!(cookie_domain_matches(
            "sub.example.com",
            Some(".example.com")
        ));
        assert!(!cookie_domain_matches(
            "notexample.com",
            Some(".example.com")
        ));
        assert!(!cookie_domain_matches("example.com", None));
    }

    #[test]
    fn cookie_mutation_is_limited_to_the_current_domain() {
        assert!(cookie_domain_can_be_set_from_host(
            "sub.example.com",
            "example.com"
        ));
        assert!(cookie_domain_can_be_set_from_host(
            "sub.example.com",
            ".example.com"
        ));
        assert!(!cookie_domain_can_be_set_from_host(
            "notexample.com",
            "example.com"
        ));
        assert!(!cookie_domain_can_be_set_from_host(
            "example.com",
            "other.test"
        ));
        assert!(!cookie_domain_can_be_set_from_host("example.com", ""));
    }

    #[test]
    fn replacing_a_cookie_sync_worker_cancels_the_old_mount() {
        let label = "browser-cookie-sync-worker-test";
        stop_cookie_sync(label);
        let first = register_cookie_sync_worker(label).expect("register first worker");
        let second = register_cookie_sync_worker(label).expect("register replacement worker");

        assert!(first.load(Ordering::Acquire));
        assert!(!second.load(Ordering::Acquire));

        stop_cookie_sync(label);
        assert!(second.load(Ordering::Acquire));
    }

    #[test]
    fn cookie_scope_requires_an_rfc_path_boundary() {
        assert!(cookie_path_matches("/foo", Some("/foo")));
        assert!(cookie_path_matches("/foo/bar", Some("/foo")));
        assert!(!cookie_path_matches("/foobar", Some("/foo")));
        assert!(cookie_path_matches("/anything", Some("/")));
    }

    #[test]
    fn browser_focus_is_owned_by_the_exact_tile() {
        browser_host_clear_focus(TrustedMainWebview);
        browser_host_claim_focus(
            TrustedMainWebview,
            "browser-workspace-one-tile-browser".to_string(),
        )
        .expect("claim browser focus");

        clear_browser_focus_for("browser-workspace-two-tile-browser");
        assert_eq!(
            focused_browser_label().as_deref(),
            Some("browser-workspace-one-tile-browser")
        );

        clear_browser_focus_for("browser-workspace-one-tile-browser");
        assert_eq!(focused_browser_label(), None);
    }

    #[test]
    fn browser_focus_claim_rejects_invalid_labels() {
        browser_host_clear_focus(TrustedMainWebview);

        assert!(browser_host_claim_focus(TrustedMainWebview, "main".to_string()).is_err());
        assert_eq!(focused_browser_label(), None);
    }

    #[test]
    fn browser_commands_reject_child_webview_callers() {
        assert!(validate_browser_command_caller("main").is_ok());
        assert!(validate_browser_command_caller("browser-workspace-one-tile-browser").is_err());
    }

    #[test]
    fn browser_child_webviews_are_excluded_from_tauri_capabilities() {
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/default.json"))
                .expect("parse default capability");
        assert_eq!(capability["webviews"], json!(["main"]));
        assert!(capability.get("remote").is_none());
    }

    #[test]
    fn present_capability_has_a_minimal_permission_surface() {
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/present.json"))
                .expect("parse present capability");
        assert_eq!(capability["windows"], json!(["present"]));
        assert_eq!(capability["webviews"], json!(["present"]));

        let permissions = capability["permissions"]
            .as_array()
            .expect("present capability permissions is an array");
        for permission in permissions {
            let identifier = permission
                .as_str()
                .or_else(|| permission["identifier"].as_str())
                .expect("permission entry has a string or identifier field");
            assert!(
                !identifier.starts_with("fs:")
                    && !identifier.starts_with("shell:")
                    && !identifier.starts_with("dialog:"),
                "present capability must not grant fs/shell/dialog permissions, found {identifier}"
            );
        }
    }

    #[test]
    fn browser_initialization_reports_pointer_focus() {
        assert!(!single_tab_script().contains("attnBrowserFocus"));
        assert!(isolated_initialization_script().contains("attnBrowserFocus"));
        assert!(isolated_initialization_script().contains("pointerdown"));
        assert!(isolated_initialization_script().contains("event.isTrusted"));
    }

    #[test]
    fn browser_initialization_retargets_new_window_forms_in_place() {
        let script = single_tab_script();

        assert!(script.contains("HTMLFormElement"));
        assert!(script.contains("formtarget"));
        assert!(script.contains("originalSubmit.call(this)"));
    }

    #[test]
    fn browser_initialization_reports_history_navigation() {
        assert!(!single_tab_script().contains("attnBrowserLocation"));
        assert!(isolated_initialization_script().contains("attnBrowserLocation"));
        assert!(isolated_initialization_script().contains("setInterval"));
    }

    #[test]
    fn page_world_initialization_does_not_expose_browser_control() {
        assert!(!single_tab_script().contains("__attnBrowser"));
        assert!(!single_tab_script().contains("messageHandlers"));
    }

    #[test]
    fn page_actions_honor_requested_timeouts_with_a_bounded_margin() {
        let params = Map::from_iter([("timeout".to_string(), json!(30_000))]);
        assert_eq!(page_action_timeout(&params), Duration::from_secs(32));

        let excessive = Map::from_iter([("timeout".to_string(), json!(300_000))]);
        assert_eq!(page_action_timeout(&excessive), Duration::from_secs(122));
    }

    #[test]
    fn landscape_pdf_size_swaps_custom_dimensions() {
        let portrait = Map::new();
        assert_eq!(page_size(21.0, 29.7, &portrait), (21.0, 29.7));

        let landscape = Map::from_iter([("orientation".to_string(), json!("landscape"))]);
        assert_eq!(page_size(21.0, 29.7, &landscape), (29.7, 21.0));
    }

    #[test]
    fn content_layout_inset_uses_the_unobscured_appkit_rect() {
        assert_eq!(content_layout_inset(1662.0, 0.0, 0.0, 1630.0), (0.0, 32.0));
        assert_eq!(content_layout_inset(600.0, 0.0, 0.0, 568.0), (0.0, 32.0),);
    }
}
