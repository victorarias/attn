use std::{
    ffi::{c_char, c_void, CString},
    ptr,
    rc::Rc,
    slice,
    sync::{Arc, Mutex},
};

use async_channel::Sender;
use attn_protocol::{PtyInputMessage, PtyResizeMessage};
use cocoa::{
    appkit::{NSScreen, NSView, NSWindow},
    base::{id, nil},
    foundation::{NSPoint, NSRect, NSSize},
};
use gpui::{Bounds, Pixels, Window};
use objc::{
    declare::ClassDecl,
    msg_send,
    runtime::{Class, Object, Sel},
    sel, sel_impl,
};
use raw_window_handle::{HasWindowHandle, RawWindowHandle};

pub struct GhosttyRuntime {
    raw: GhosttyApp,
}

impl GhosttyRuntime {
    pub fn new() -> Result<Rc<Self>, String> {
        let config = unsafe {
            if ghostty_init(0, ptr::null_mut()) != 0 {
                return Err("ghostty_init failed".into());
            }
            let config = ghostty_config_new();
            if config.is_null() {
                return Err("ghostty_config_new failed".into());
            }
            ghostty_config_load_default_files(config);
            ghostty_config_load_recursive_files(config);
            ghostty_config_finalize(config);
            config
        };
        let runtime_config = GhosttyRuntimeConfig {
            userdata: ptr::null_mut(),
            supports_selection_clipboard: false,
            wakeup_cb: wakeup,
            action_cb: action,
            read_clipboard_cb: read_clipboard,
            confirm_read_clipboard_cb: confirm_read_clipboard,
            write_clipboard_cb: write_clipboard,
            close_surface_cb: Some(close_surface),
        };
        let raw = unsafe { ghostty_app_new(&runtime_config, config) };
        unsafe { ghostty_config_free(config) };
        if raw.is_null() {
            return Err("ghostty_app_new failed".into());
        }
        Ok(Rc::new(Self { raw }))
    }
}

impl Drop for GhosttyRuntime {
    fn drop(&mut self) {
        unsafe { ghostty_app_free(self.raw) };
    }
}

pub struct GhosttySurface {
    raw: GhosttySurfacePtr,
    native_view: id,
    sink: Arc<IoSink>,
    _runtime: Rc<GhosttyRuntime>,
}

impl GhosttySurface {
    pub fn mount(
        runtime: Rc<GhosttyRuntime>,
        runtime_id: String,
        bounds: Bounds<Pixels>,
        window: &Window,
    ) -> Result<Self, String> {
        let parent_view = parent_view(window)?;
        park_background_window(parent_view);
        let native_view = unsafe {
            let view: id = msg_send![render_view_class(), alloc];
            let view: id = msg_send![view, initWithFrame: native_frame(parent_view, bounds)];
            if view == nil {
                return Err("create Ghostty NSView host failed".into());
            }
            parent_view.addSubview_(view);
            view
        };
        let sink = Arc::new(IoSink {
            runtime_id,
            sender: Mutex::new(None),
        });
        let mut config = unsafe { ghostty_surface_config_new() };
        config.platform_tag = GhosttyPlatform::Macos;
        config.platform = GhosttyPlatformValue {
            macos: GhosttyPlatformMacos {
                nsview: native_view.cast(),
            },
        };
        config.scale_factor = window.scale_factor() as f64;
        config.io_mode = GhosttySurfaceIoMode::External;
        config.io_userdata = Arc::as_ptr(&sink).cast_mut().cast();
        config.io_write = Some(io_write);
        config.io_resize = Some(io_resize);
        let raw = unsafe { ghostty_surface_new(runtime.raw, &config) };
        if raw.is_null() {
            unsafe {
                native_view.removeFromSuperview();
                let _: () = msg_send![native_view, release];
            }
            return Err("ghostty_surface_new failed".into());
        }
        let surface = Self {
            raw,
            native_view,
            sink,
            _runtime: runtime,
        };
        surface.update_frame(bounds, window);
        Ok(surface)
    }

    pub fn update_sender(&self, sender: Option<Sender<String>>) {
        *self.sink.sender.lock().expect("Ghostty IO sender mutex") = sender;
    }

    pub fn update_frame(&self, bounds: Bounds<Pixels>, window: &Window) {
        let Ok(parent_view) = parent_view(window) else {
            return;
        };
        let scale_factor = window.scale_factor();
        let width_px = backing_dimension(f32::from(bounds.size.width), scale_factor);
        let height_px = backing_dimension(f32::from(bounds.size.height), scale_factor);
        unsafe {
            self.native_view
                .setFrameOrigin(native_frame(parent_view, bounds).origin);
            self.native_view
                .setFrameSize(native_frame(parent_view, bounds).size);
            ghostty_surface_set_content_scale(self.raw, scale_factor as f64, scale_factor as f64);
            // The NSView frame is in logical points; Ghostty sizes its Metal
            // framebuffer and grid from backing pixels, matching SurfaceView_AppKit.
            ghostty_surface_set_size(self.raw, width_px, height_px);
        }
    }

    pub fn process_output(&self, bytes: &[u8]) {
        unsafe { ghostty_surface_process_output(self.raw, bytes.as_ptr().cast(), bytes.len()) };
    }

    pub fn process_replay(&self, bytes: &[u8]) {
        unsafe { ghostty_surface_process_replay(self.raw, bytes.as_ptr().cast(), bytes.len()) };
    }

    pub fn send_key(&self, input: &GhosttyKeyInput) -> bool {
        let text = input
            .text
            .as_deref()
            .and_then(|text| CString::new(text).ok());
        let event = GhosttyInputKey {
            action: if input.repeat {
                GhosttyInputAction::Repeat
            } else {
                GhosttyInputAction::Press
            },
            mods: input.mods,
            consumed_mods: 0,
            keycode: input.keycode,
            text: text.as_ref().map_or(ptr::null(), |text| text.as_ptr()),
            unshifted_codepoint: input.unshifted_codepoint,
            composing: false,
        };
        unsafe { ghostty_surface_key(self.raw, event) }
    }

    pub fn draw(&self) {
        unsafe { ghostty_surface_draw(self.raw) };
    }

    pub fn set_focus(&self, focused: bool) {
        unsafe { ghostty_surface_set_focus(self.raw, focused) };
    }

    pub fn size(&self) -> (u16, u16) {
        let size = unsafe { ghostty_surface_size(self.raw) };
        (size.columns, size.rows)
    }

    pub fn viewport_text(&self) -> Option<String> {
        let selection = GhosttySelection {
            top_left: GhosttyPoint::viewport_top_left(),
            bottom_right: GhosttyPoint::viewport_bottom_right(),
            rectangle: false,
        };
        let mut result = GhosttyText::default();
        if !unsafe { ghostty_surface_read_text(self.raw, selection, &mut result) } {
            return None;
        }
        let text = if result.text.is_null() {
            String::new()
        } else {
            String::from_utf8_lossy(unsafe {
                slice::from_raw_parts(result.text.cast(), result.text_len)
            })
            .into_owned()
        };
        unsafe { ghostty_surface_free_text(self.raw, &mut result) };
        Some(text)
    }
}

impl Drop for GhosttySurface {
    fn drop(&mut self) {
        unsafe {
            ghostty_surface_free(self.raw);
            self.native_view.removeFromSuperview();
            let _: () = msg_send![self.native_view, release];
        }
    }
}

/// Keyboard input already translated by GPUI, submitted through Ghostty so
/// terminal modes and key encodings remain Ghostty-owned.
pub struct GhosttyKeyInput {
    pub keycode: u32,
    pub text: Option<String>,
    pub unshifted_codepoint: u32,
    pub mods: i32,
    pub repeat: bool,
}

pub struct GhosttyInputMods;

impl GhosttyInputMods {
    const SHIFT: i32 = 1 << 0;
    const CONTROL: i32 = 1 << 1;
    const ALT: i32 = 1 << 2;
    const SUPER: i32 = 1 << 3;

    pub fn from_flags(control: bool, alt: bool, shift: bool, platform: bool) -> i32 {
        let mut bits = 0;
        if shift {
            bits |= Self::SHIFT;
        }
        if control {
            bits |= Self::CONTROL;
        }
        if alt {
            bits |= Self::ALT;
        }
        if platform {
            bits |= Self::SUPER;
        }
        bits
    }
}

fn render_view_class() -> &'static Class {
    if let Some(class) = Class::get("AttnGhosttyRenderView") {
        return class;
    }
    let superclass = Class::get("NSView").expect("NSView class");
    let mut class = ClassDecl::new("AttnGhosttyRenderView", superclass)
        .expect("declare AttnGhosttyRenderView once");
    unsafe {
        class.add_method(
            sel!(hitTest:),
            render_view_hit_test as extern "C" fn(&Object, Sel, NSPoint) -> id,
        );
    }
    class.register()
}

extern "C" fn render_view_hit_test(_this: &Object, _selector: Sel, _point: NSPoint) -> id {
    // The Ghostty child is a Metal output host. GPUI owns interaction and
    // focus, so leave mouse hit testing to the pane beneath this native view.
    nil
}

struct IoSink {
    runtime_id: String,
    sender: Mutex<Option<Sender<String>>>,
}

impl IoSink {
    fn send<T: serde::Serialize>(&self, message: &T) {
        let Ok(serialized) = serde_json::to_string(message) else {
            return;
        };
        if let Some(sender) = self
            .sender
            .lock()
            .expect("Ghostty IO sender mutex")
            .as_ref()
        {
            let _ = sender.try_send(serialized);
        }
    }
}

unsafe extern "C" fn io_write(userdata: *mut c_void, bytes: *const c_char, len: usize) {
    if userdata.is_null() || bytes.is_null() {
        return;
    }
    let sink = unsafe { &*(userdata as *const IoSink) };
    crate::adapters::automation::events::record(
        "terminal_input_callback",
        serde_json::json!({"runtime_id": sink.runtime_id, "bytes": len}),
    );
    let text = String::from_utf8_lossy(unsafe { slice::from_raw_parts(bytes.cast(), len) });
    sink.send(&PtyInputMessage::new(
        sink.runtime_id.clone(),
        text.into_owned(),
    ));
}

unsafe extern "C" fn io_resize(
    userdata: *mut c_void,
    columns: u16,
    rows: u16,
    _width_px: u32,
    _height_px: u32,
) {
    if userdata.is_null() {
        return;
    }
    let sink = unsafe { &*(userdata as *const IoSink) };
    sink.send(&PtyResizeMessage::new(
        sink.runtime_id.clone(),
        columns,
        rows,
    ));
}

fn park_background_window(parent_view: id) {
    if !crate::adapters::automation::background_window() {
        return;
    }
    unsafe {
        let window: id = msg_send![parent_view, window];
        let screen = NSScreen::mainScreen(nil);
        if window == nil || screen == nil {
            return;
        }
        let visible = NSScreen::visibleFrame(screen);
        let frame = NSWindow::frame(window);
        let origin = NSPoint::new(
            visible.origin.x + visible.size.width - 20.0,
            visible.origin.y + ((visible.size.height - frame.size.height) / 2.0).max(0.0),
        );
        NSWindow::setFrameOrigin_(window, origin);
    }
}

fn parent_view(window: &Window) -> Result<id, String> {
    let handle = HasWindowHandle::window_handle(window)
        .map_err(|error| format!("access GPUI window handle: {error}"))?
        .as_raw();
    match handle {
        RawWindowHandle::AppKit(handle) => Ok(handle.ns_view.as_ptr().cast()),
        _ => Err("native Ghostty surfaces require an AppKit window".into()),
    }
}

fn native_frame(parent_view: id, bounds: Bounds<Pixels>) -> NSRect {
    let parent_height = unsafe { parent_view.bounds().size.height };
    let x = f64::from(bounds.origin.x);
    let height = f64::from(bounds.size.height);
    let y = parent_height - f64::from(bounds.origin.y) - height;
    NSRect::new(
        NSPoint::new(x, y),
        NSSize::new(f64::from(bounds.size.width), height),
    )
}

fn backing_dimension(points: f32, scale_factor: f32) -> u32 {
    (points.max(1.0) * scale_factor.max(1.0)).round().max(1.0) as u32
}

unsafe extern "C" fn wakeup(_userdata: *mut c_void) {}
unsafe extern "C" fn action(
    _app: GhosttyApp,
    _target: GhosttyTarget,
    _action: GhosttyAction,
) -> bool {
    false
}
unsafe extern "C" fn read_clipboard(
    _userdata: *mut c_void,
    _clipboard: i32,
    _state: *mut c_void,
) -> bool {
    false
}
unsafe extern "C" fn confirm_read_clipboard(
    _userdata: *mut c_void,
    _text: *const c_char,
    _state: *mut c_void,
    _request: i32,
) {
}
unsafe extern "C" fn write_clipboard(
    _userdata: *mut c_void,
    _clipboard: i32,
    _content: *const GhosttyClipboardContent,
    _len: usize,
    _confirm: bool,
) {
}
unsafe extern "C" fn close_surface(_userdata: *mut c_void, _process_alive: bool) {}

type GhosttyApp = *mut c_void;
type GhosttyConfig = *mut c_void;
type GhosttySurfacePtr = *mut c_void;

#[repr(C)]
struct GhosttyRuntimeConfig {
    userdata: *mut c_void,
    supports_selection_clipboard: bool,
    wakeup_cb: unsafe extern "C" fn(*mut c_void),
    action_cb: unsafe extern "C" fn(GhosttyApp, GhosttyTarget, GhosttyAction) -> bool,
    read_clipboard_cb: unsafe extern "C" fn(*mut c_void, i32, *mut c_void) -> bool,
    confirm_read_clipboard_cb: unsafe extern "C" fn(*mut c_void, *const c_char, *mut c_void, i32),
    write_clipboard_cb:
        unsafe extern "C" fn(*mut c_void, i32, *const GhosttyClipboardContent, usize, bool),
    close_surface_cb: Option<unsafe extern "C" fn(*mut c_void, bool)>,
}

#[repr(C)]
struct GhosttyClipboardContent {
    mime: *const c_char,
    data: *const c_char,
}

#[repr(C)]
#[derive(Clone, Copy)]
struct GhosttyTarget {
    tag: i32,
    value: GhosttyTargetValue,
}

#[repr(C)]
#[derive(Clone, Copy)]
union GhosttyTargetValue {
    surface: GhosttySurfacePtr,
}

#[repr(C)]
#[derive(Clone, Copy)]
struct GhosttyAction {
    tag: i32,
    value: GhosttyActionValue,
}

#[repr(C)]
#[derive(Clone, Copy)]
union GhosttyActionValue {
    words: [u64; 3],
}

#[repr(i32)]
#[allow(dead_code)]
enum GhosttyPlatform {
    Invalid = 0,
    Macos = 1,
    Ios = 2,
}

#[repr(C)]
union GhosttyPlatformValue {
    macos: GhosttyPlatformMacos,
}

#[repr(C)]
#[derive(Clone, Copy)]
struct GhosttyPlatformMacos {
    nsview: *mut c_void,
}

#[repr(i32)]
#[allow(dead_code)]
enum GhosttySurfaceContext {
    Window = 0,
    Tab = 1,
    Split = 2,
}

#[repr(i32)]
#[allow(dead_code)]
enum GhosttySurfaceIoMode {
    Exec = 0,
    External = 1,
}

#[repr(C)]
struct GhosttySurfaceConfig {
    platform_tag: GhosttyPlatform,
    platform: GhosttyPlatformValue,
    userdata: *mut c_void,
    scale_factor: f64,
    font_size: f32,
    working_directory: *const c_char,
    command: *const c_char,
    env_vars: *mut c_void,
    env_var_count: usize,
    initial_input: *const c_char,
    wait_after_command: bool,
    context: GhosttySurfaceContext,
    io_mode: GhosttySurfaceIoMode,
    io_userdata: *mut c_void,
    io_write: Option<unsafe extern "C" fn(*mut c_void, *const c_char, usize)>,
    io_resize: Option<unsafe extern "C" fn(*mut c_void, u16, u16, u32, u32)>,
}

#[repr(C)]
struct GhosttySurfaceSize {
    columns: u16,
    rows: u16,
    width_px: u32,
    height_px: u32,
    cell_width_px: u32,
    cell_height_px: u32,
}

#[repr(i32)]
#[derive(Clone, Copy)]
enum GhosttyInputAction {
    Press = 1,
    Repeat = 2,
}

#[repr(C)]
struct GhosttyInputKey {
    action: GhosttyInputAction,
    mods: i32,
    consumed_mods: i32,
    keycode: u32,
    text: *const c_char,
    unshifted_codepoint: u32,
    composing: bool,
}

#[repr(C)]
struct GhosttyPoint {
    tag: i32,
    coord: i32,
    x: u32,
    y: u32,
}

impl GhosttyPoint {
    fn viewport_top_left() -> Self {
        Self {
            tag: 1,
            coord: 1,
            x: 0,
            y: 0,
        }
    }

    fn viewport_bottom_right() -> Self {
        Self {
            tag: 1,
            coord: 2,
            x: 0,
            y: 0,
        }
    }
}

#[repr(C)]
struct GhosttySelection {
    top_left: GhosttyPoint,
    bottom_right: GhosttyPoint,
    rectangle: bool,
}

#[repr(C)]
#[derive(Default)]
struct GhosttyText {
    tl_px_x: f64,
    tl_px_y: f64,
    offset_start: u32,
    offset_len: u32,
    text: *const c_char,
    text_len: usize,
}

extern "C" {
    fn ghostty_init(argc: usize, argv: *mut *mut c_char) -> i32;
    fn ghostty_config_new() -> GhosttyConfig;
    fn ghostty_config_free(config: GhosttyConfig);
    fn ghostty_config_load_default_files(config: GhosttyConfig);
    fn ghostty_config_load_recursive_files(config: GhosttyConfig);
    fn ghostty_config_finalize(config: GhosttyConfig);
    fn ghostty_app_new(config: *const GhosttyRuntimeConfig, ghostty: GhosttyConfig) -> GhosttyApp;
    fn ghostty_app_free(app: GhosttyApp);
    fn ghostty_surface_config_new() -> GhosttySurfaceConfig;
    fn ghostty_surface_new(
        app: GhosttyApp,
        config: *const GhosttySurfaceConfig,
    ) -> GhosttySurfacePtr;
    fn ghostty_surface_free(surface: GhosttySurfacePtr);
    fn ghostty_surface_set_content_scale(surface: GhosttySurfacePtr, x: f64, y: f64);
    fn ghostty_surface_set_size(surface: GhosttySurfacePtr, width: u32, height: u32);
    fn ghostty_surface_size(surface: GhosttySurfacePtr) -> GhosttySurfaceSize;
    fn ghostty_surface_set_focus(surface: GhosttySurfacePtr, focused: bool);
    fn ghostty_surface_draw(surface: GhosttySurfacePtr);
    fn ghostty_surface_process_output(surface: GhosttySurfacePtr, bytes: *const c_char, len: usize);
    fn ghostty_surface_process_replay(surface: GhosttySurfacePtr, bytes: *const c_char, len: usize);
    fn ghostty_surface_key(surface: GhosttySurfacePtr, event: GhosttyInputKey) -> bool;
    fn ghostty_surface_read_text(
        surface: GhosttySurfacePtr,
        selection: GhosttySelection,
        result: *mut GhosttyText,
    ) -> bool;
    fn ghostty_surface_free_text(surface: GhosttySurfacePtr, result: *mut GhosttyText);
}

#[cfg(test)]
mod tests {
    use super::backing_dimension;

    #[test]
    fn converts_logical_surface_extent_to_retina_backing_pixels() {
        assert_eq!(backing_dimension(1170.0, 2.0), 2340);
        assert_eq!(backing_dimension(640.5, 2.0), 1281);
        assert_eq!(backing_dimension(0.0, 2.0), 2);
    }
}
