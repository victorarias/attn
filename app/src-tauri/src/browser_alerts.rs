// Adapted from tauri-plugin-webdriver's MIT-licensed macOS alert handler.
use std::collections::HashMap;
use std::sync::{Arc, LazyLock, Mutex};

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum AlertType {
    Alert,
    Confirm,
    Prompt,
}

pub struct AlertResponse {
    pub accepted: bool,
    pub prompt_text: Option<String>,
}

pub struct PendingAlert {
    pub message: String,
    pub default_text: Option<String>,
    pub alert_type: AlertType,
    pub responder: Box<dyn FnOnce(AlertResponse) + Send>,
}

#[derive(Default)]
pub struct AlertState {
    pending: Mutex<Option<PendingAlert>>,
    prompt_input: Mutex<Option<String>>,
    capture_requested: Mutex<bool>,
}

impl AlertState {
    pub fn request_capture(&self) {
        if let Ok(mut requested) = self.capture_requested.lock() {
            *requested = true;
        }
    }

    pub fn should_capture(&self) -> bool {
        self.capture_requested
            .lock()
            .is_ok_and(|requested| *requested)
    }

    pub fn clear_capture_if_idle(&self) {
        let has_pending = self.pending.lock().is_ok_and(|pending| pending.is_some());
        if !has_pending {
            self.clear_capture();
        }
    }

    fn clear_capture(&self) {
        if let Ok(mut requested) = self.capture_requested.lock() {
            *requested = false;
        }
    }

    pub fn set_pending(&self, alert: PendingAlert) {
        if let Ok(mut prompt) = self.prompt_input.lock() {
            *prompt = None;
        }
        if let Ok(mut pending) = self.pending.lock() {
            *pending = Some(alert);
        }
    }

    pub fn message(&self) -> Option<String> {
        self.pending
            .lock()
            .ok()?
            .as_ref()
            .map(|alert| alert.message.clone())
    }

    pub fn alert_type(&self) -> Option<AlertType> {
        self.pending
            .lock()
            .ok()?
            .as_ref()
            .map(|alert| alert.alert_type)
    }

    pub fn default_text(&self) -> Option<String> {
        self.pending
            .lock()
            .ok()?
            .as_ref()
            .and_then(|alert| alert.default_text.clone())
    }

    pub fn prompt_input(&self) -> Option<String> {
        self.prompt_input.lock().ok()?.clone()
    }

    pub fn set_prompt_input(&self, text: String) -> bool {
        if self.alert_type() != Some(AlertType::Prompt) {
            return false;
        }
        self.prompt_input.lock().is_ok_and(|mut prompt| {
            *prompt = Some(text);
            true
        })
    }

    pub fn respond(&self, accepted: bool, prompt_text: Option<String>) -> bool {
        let Some(alert) = self
            .pending
            .lock()
            .ok()
            .and_then(|mut pending| pending.take())
        else {
            return false;
        };
        if let Ok(mut prompt) = self.prompt_input.lock() {
            *prompt = None;
        }
        self.clear_capture();
        (alert.responder)(AlertResponse {
            accepted,
            prompt_text,
        });
        true
    }

    fn reset(&self) {
        if !self.respond(false, None) {
            if let Ok(mut prompt) = self.prompt_input.lock() {
                *prompt = None;
            }
            self.clear_capture();
        }
    }
}

static ALERT_STATES: LazyLock<Mutex<HashMap<String, Arc<AlertState>>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

pub fn state(label: &str) -> Arc<AlertState> {
    ALERT_STATES
        .lock()
        .expect("browser alert state lock poisoned")
        .entry(label.to_string())
        .or_insert_with(|| Arc::new(AlertState::default()))
        .clone()
}

pub fn remove(label: &str) {
    let alert_state = ALERT_STATES
        .lock()
        .ok()
        .and_then(|mut states| states.remove(label));
    if let Some(alert_state) = alert_state {
        alert_state.reset();
    }
}

#[cfg(target_os = "macos")]
mod macos {
    use super::{state, AlertType, PendingAlert};
    use block2::{DynBlock, RcBlock};
    use objc2::ffi::{objc_setAssociatedObject, OBJC_ASSOCIATION_RETAIN_NONATOMIC};
    use objc2::rc::Retained;
    use objc2::runtime::{AnyObject, Bool, ProtocolObject, Sel};
    use objc2::{define_class, msg_send, DefinedClass, MainThreadMarker, MainThreadOnly};
    use objc2_app_kit::{NSAlert, NSAlertFirstButtonReturn, NSTextField};
    use objc2_foundation::{NSObject, NSObjectProtocol, NSPoint, NSRect, NSSize, NSString};
    use objc2_web_kit::{WKFrameInfo, WKUIDelegate, WKWebView};
    use std::sync::Arc;
    use tauri::Manager;

    static DELEGATE_KEY: u8 = 0;

    struct SendAlertBlock(RcBlock<dyn Fn()>);
    unsafe impl Send for SendAlertBlock {}
    impl SendAlertBlock {
        fn complete(self) {
            self.0.call(());
        }
    }

    struct SendConfirmBlock(RcBlock<dyn Fn(Bool)>);
    unsafe impl Send for SendConfirmBlock {}
    impl SendConfirmBlock {
        fn complete(self, accepted: bool) {
            self.0.call((Bool::from(accepted),));
        }
    }

    struct SendPromptBlock(RcBlock<dyn Fn(*mut NSString)>);
    unsafe impl Send for SendPromptBlock {}
    impl SendPromptBlock {
        fn complete(self, value: *mut NSString) {
            self.0.call((value,));
        }
    }

    fn native_alert(message: &NSString) {
        let alert = NSAlert::new(unsafe { MainThreadMarker::new_unchecked() });
        alert.setMessageText(message);
        alert.addButtonWithTitle(&NSString::from_str("OK"));
        alert.runModal();
    }

    fn native_confirm(message: &NSString) -> bool {
        let alert = NSAlert::new(unsafe { MainThreadMarker::new_unchecked() });
        alert.setMessageText(message);
        alert.addButtonWithTitle(&NSString::from_str("OK"));
        alert.addButtonWithTitle(&NSString::from_str("Cancel"));
        alert.runModal() == NSAlertFirstButtonReturn
    }

    fn native_prompt(
        message: &NSString,
        default_text: Option<&NSString>,
    ) -> Option<Retained<NSString>> {
        let marker = unsafe { MainThreadMarker::new_unchecked() };
        let alert = NSAlert::new(marker);
        alert.setMessageText(message);
        alert.addButtonWithTitle(&NSString::from_str("OK"));
        alert.addButtonWithTitle(&NSString::from_str("Cancel"));
        let initial_text = default_text.map(ToString::to_string).unwrap_or_default();
        let input = NSTextField::textFieldWithString(&NSString::from_str(&initial_text), marker);
        input.setFrame(NSRect::new(
            NSPoint::new(0.0, 0.0),
            NSSize::new(320.0, 24.0),
        ));
        alert.setAccessoryView(Some(&input));
        if alert.runModal() == NSAlertFirstButtonReturn {
            Some(input.stringValue())
        } else {
            None
        }
    }

    pub fn register(webview: &tauri::Webview) -> Result<(), String> {
        let alert_state = state(webview.label());
        let app = webview.app_handle().clone();
        webview
            .with_webview(move |platform| unsafe {
                let view: &WKWebView = &*platform.inner().cast();
                let original_delegate = view.UIDelegate();
                let delegate = BrowserUIDelegate::new(alert_state, app, original_delegate);
                let protocol: Retained<ProtocolObject<dyn WKUIDelegate>> =
                    ProtocolObject::from_retained(delegate);
                view.setUIDelegate(Some(&protocol));
                objc_setAssociatedObject(
                    std::ptr::from_ref(view).cast_mut().cast(),
                    std::ptr::addr_of!(DELEGATE_KEY).cast(),
                    Retained::into_raw(protocol).cast(),
                    OBJC_ASSOCIATION_RETAIN_NONATOMIC,
                );
            })
            .map_err(|error| format!("register browser alert handler: {error}"))
    }

    struct BrowserUIDelegateIvars {
        alert_state: Arc<super::AlertState>,
        app: tauri::AppHandle,
        original_delegate: Option<Retained<ProtocolObject<dyn WKUIDelegate>>>,
    }

    unsafe impl Send for BrowserUIDelegateIvars {}
    unsafe impl Sync for BrowserUIDelegateIvars {}

    define_class!(
        #[unsafe(super(NSObject))]
        #[thread_kind = MainThreadOnly]
        #[name = "AttnBrowserUIDelegate"]
        #[ivars = BrowserUIDelegateIvars]
        struct BrowserUIDelegate;

        impl BrowserUIDelegate {
            #[unsafe(method(respondsToSelector:))]
            fn responds_to_selector(&self, selector: Sel) -> bool {
                let responds: bool = unsafe {
                    msg_send![super(self), respondsToSelector: selector]
                };
                responds || self.ivars().original_delegate.as_ref().is_some_and(
                    |delegate| delegate.respondsToSelector(selector),
                )
            }

            #[unsafe(method(forwardingTargetForSelector:))]
            fn forwarding_target_for_selector(&self, selector: Sel) -> Option<&AnyObject> {
                self.ivars()
                    .original_delegate
                    .as_ref()
                    .filter(|delegate| delegate.respondsToSelector(selector))
                    .map(|delegate| {
                        <ProtocolObject<dyn WKUIDelegate> as AsRef<AnyObject>>::as_ref(&**delegate)
                    })
            }
        }

        unsafe impl NSObjectProtocol for BrowserUIDelegate {}

        #[allow(non_snake_case)]
        unsafe impl WKUIDelegate for BrowserUIDelegate {
            #[unsafe(method(webView:runJavaScriptAlertPanelWithMessage:initiatedByFrame:completionHandler:))]
            fn webView_runJavaScriptAlertPanelWithMessage_initiatedByFrame_completionHandler(
                &self,
                _webview: &WKWebView,
                message: &NSString,
                _frame: &WKFrameInfo,
                completion: &DynBlock<dyn Fn()>,
            ) {
                if !self.ivars().alert_state.should_capture() {
                    native_alert(message);
                    completion.call(());
                    return;
                }
                let completion = SendAlertBlock(unsafe {
                    RcBlock::copy(completion as *const _ as *mut _)
                        .expect("copy alert completion block")
                });
                let app = self.ivars().app.clone();
                self.ivars().alert_state.set_pending(PendingAlert {
                    message: message.to_string(),
                    default_text: None,
                    alert_type: AlertType::Alert,
                    responder: Box::new(move |_response| {
                        let _ = app.run_on_main_thread(move || completion.complete());
                    }),
                });
            }

            #[unsafe(method(webView:runJavaScriptConfirmPanelWithMessage:initiatedByFrame:completionHandler:))]
            fn webView_runJavaScriptConfirmPanelWithMessage_initiatedByFrame_completionHandler(
                &self,
                _webview: &WKWebView,
                message: &NSString,
                _frame: &WKFrameInfo,
                completion: &DynBlock<dyn Fn(Bool)>,
            ) {
                if !self.ivars().alert_state.should_capture() {
                    completion.call((Bool::from(native_confirm(message)),));
                    return;
                }
                let completion = SendConfirmBlock(unsafe {
                    RcBlock::copy(completion as *const _ as *mut _)
                        .expect("copy confirm completion block")
                });
                let app = self.ivars().app.clone();
                self.ivars().alert_state.set_pending(PendingAlert {
                    message: message.to_string(),
                    default_text: None,
                    alert_type: AlertType::Confirm,
                    responder: Box::new(move |response| {
                        let _ =
                            app.run_on_main_thread(move || completion.complete(response.accepted));
                    }),
                });
            }

            #[unsafe(method(webView:runJavaScriptTextInputPanelWithPrompt:defaultText:initiatedByFrame:completionHandler:))]
            fn webView_runJavaScriptTextInputPanelWithPrompt_defaultText_initiatedByFrame_completionHandler(
                &self,
                _webview: &WKWebView,
                prompt: &NSString,
                default_text: Option<&NSString>,
                _frame: &WKFrameInfo,
                completion: &DynBlock<dyn Fn(*mut NSString)>,
            ) {
                if !self.ivars().alert_state.should_capture() {
                    let value = native_prompt(prompt, default_text)
                        .map(Retained::into_raw)
                        .unwrap_or(std::ptr::null_mut());
                    completion.call((value,));
                    return;
                }
                let default_text = default_text.map(ToString::to_string);
                let completion = SendPromptBlock(unsafe {
                    RcBlock::copy(completion as *const _ as *mut _)
                        .expect("copy prompt completion block")
                });
                let app = self.ivars().app.clone();
                self.ivars().alert_state.set_pending(PendingAlert {
                    message: prompt.to_string(),
                    default_text: default_text.clone(),
                    alert_type: AlertType::Prompt,
                    responder: Box::new(move |response| {
                        let _ = app.run_on_main_thread(move || {
                            let value = if response.accepted {
                                Retained::into_raw(NSString::from_str(
                                    &response.prompt_text.or(default_text).unwrap_or_default(),
                                ))
                            } else {
                                std::ptr::null_mut()
                            };
                            completion.complete(value);
                        });
                    }),
                });
            }
        }
    );

    impl BrowserUIDelegate {
        unsafe fn new(
            alert_state: Arc<super::AlertState>,
            app: tauri::AppHandle,
            original_delegate: Option<Retained<ProtocolObject<dyn WKUIDelegate>>>,
        ) -> Retained<Self> {
            let this = Self::alloc(MainThreadMarker::new_unchecked());
            let this = this.set_ivars(BrowserUIDelegateIvars {
                alert_state,
                app,
                original_delegate,
            });
            msg_send![super(this), init]
        }
    }
}

#[cfg(target_os = "macos")]
pub use macos::register;

#[cfg(not(target_os = "macos"))]
pub fn register(_webview: &tauri::Webview) -> Result<(), String> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{remove, state, AlertState, AlertType, PendingAlert};
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;

    #[test]
    fn capture_is_only_armed_for_agent_actions() {
        let state = AlertState::default();
        assert!(!state.should_capture());

        state.request_capture();
        assert!(state.should_capture());

        state.clear_capture_if_idle();
        assert!(!state.should_capture());
    }

    #[test]
    fn removing_alert_state_dismisses_pending_dialogs() {
        let label = "browser-alert-remove-test";
        remove(label);
        let dismissed = Arc::new(AtomicBool::new(false));
        let dismissed_for_responder = dismissed.clone();
        let alert_state = state(label);
        alert_state.request_capture();
        alert_state.set_pending(PendingAlert {
            message: "Confirm?".to_string(),
            default_text: None,
            alert_type: AlertType::Confirm,
            responder: Box::new(move |response| {
                dismissed_for_responder.store(!response.accepted, Ordering::Release);
            }),
        });

        remove(label);

        assert!(dismissed.load(Ordering::Acquire));
        let replacement = state(label);
        assert!(replacement.message().is_none());
        assert!(!replacement.should_capture());
        remove(label);
    }
}
