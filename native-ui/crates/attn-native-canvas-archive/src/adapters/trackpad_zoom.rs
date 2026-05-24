#[derive(Clone, Copy, Debug)]
pub struct TrackpadZoomEvent {
    pub window_x: f32,
    pub window_y: f32,
    pub magnification: f32,
}

#[cfg(target_os = "macos")]
mod platform {
    use super::TrackpadZoomEvent;
    use async_channel::Sender;
    use block::{ConcreteBlock, RcBlock};
    use cocoa::appkit::{NSEvent, NSEventMask, NSEventType, NSView, NSWindow};
    use cocoa::base::{id, nil};
    use objc::{class, msg_send, sel, sel_impl};

    pub struct Handle {
        token: id,
        _block: RcBlock<(id,), id>,
    }

    impl Drop for Handle {
        fn drop(&mut self) {
            unsafe {
                let _: () = msg_send![class!(NSEvent), removeMonitor: self.token];
            }
        }
    }

    pub fn install(tx: Sender<TrackpadZoomEvent>) -> Option<Handle> {
        unsafe {
            let block = ConcreteBlock::new(move |event: id| -> id {
                let event_type: NSEventType = msg_send![event, type];
                if event_type == NSEventType::NSEventTypeMagnify {
                    let window = event.window();
                    if window != nil {
                        let content_view = window.contentView();
                        if content_view != nil {
                            let point = event.locationInWindow();
                            let bounds = content_view.bounds();
                            let magnification = event.magnification() as f32;
                            if magnification.is_finite() && magnification.abs() > f32::EPSILON {
                                let _ = tx.try_send(TrackpadZoomEvent {
                                    window_x: point.x as f32,
                                    window_y: bounds.size.height as f32 - point.y as f32,
                                    magnification,
                                });
                            }
                        }
                    }
                }
                event
            })
            .copy();

            let mask = NSEventMask::from_type(NSEventType::NSEventTypeMagnify);
            let token: id = msg_send![
                class!(NSEvent),
                addLocalMonitorForEventsMatchingMask: mask.bits()
                handler: &*block
            ];
            if token == nil {
                None
            } else {
                Some(Handle {
                    token,
                    _block: block,
                })
            }
        }
    }
}

#[cfg(not(target_os = "macos"))]
mod platform {
    use super::TrackpadZoomEvent;
    use async_channel::Sender;

    pub struct Handle;

    pub fn install(_tx: Sender<TrackpadZoomEvent>) -> Option<Handle> {
        None
    }
}

pub use platform::{install, Handle};
