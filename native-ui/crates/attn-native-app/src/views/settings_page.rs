//! Native Settings surface. Kept small for the MVP: it owns only the
//! pixels and delegates state mutations back to `NativeApp`.

use gpui::{
    div, hsla, point, prelude::*, px, BoxShadow, Context, FocusHandle, Focusable, KeyDownEvent,
    MouseButton, ParentElement, Render, SharedString, Window,
};

use crate::theme;

type CloseHandler = dyn Fn(&mut Window, &mut gpui::App) + 'static;
type ToggleSidebarHandler = dyn Fn(&mut Window, &mut gpui::App) -> bool + 'static;

pub struct SettingsPage {
    sidebar_collapsed: bool,
    on_close: Box<CloseHandler>,
    on_toggle_sidebar: Box<ToggleSidebarHandler>,
    focus_handle: FocusHandle,
}

impl SettingsPage {
    pub fn new(
        sidebar_collapsed: bool,
        on_close: impl Fn(&mut Window, &mut gpui::App) + 'static,
        on_toggle_sidebar: impl Fn(&mut Window, &mut gpui::App) -> bool + 'static,
        cx: &mut Context<Self>,
    ) -> Self {
        Self {
            sidebar_collapsed,
            on_close: Box::new(on_close),
            on_toggle_sidebar: Box::new(on_toggle_sidebar),
            focus_handle: cx.focus_handle(),
        }
    }

    fn close(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        (self.on_close)(window, cx);
    }

    pub fn click_close_for_automation(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.close(window, cx);
    }

    fn toggle_sidebar(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.sidebar_collapsed = (self.on_toggle_sidebar)(window, cx);
        cx.notify();
    }

    pub fn click_sidebar_mode_for_automation(
        &mut self,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.toggle_sidebar(window, cx);
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        match event.keystroke.key.as_str() {
            "escape" => {
                cx.stop_propagation();
                self.close(window, cx);
            }
            "b" if event.keystroke.modifiers.platform => {
                cx.stop_propagation();
                self.toggle_sidebar(window, cx);
            }
            _ => {}
        }
    }
}

impl Focusable for SettingsPage {
    fn focus_handle(&self, _cx: &gpui::App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for SettingsPage {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if !self.focus_handle.is_focused(window) {
            self.focus_handle.focus(window);
        }

        let panel = div()
            .w(px(760.0))
            .rounded(px(theme::radius::R2))
            .bg(theme::ink::nocturne())
            .border_1()
            .border_color(theme::line::firm())
            .overflow_hidden()
            .shadow(vec![
                BoxShadow {
                    color: hsla(0.0, 0.0, 0.0, 0.56),
                    offset: point(px(0.0), px(24.0)),
                    blur_radius: px(60.0),
                    spread_radius: px(-8.0),
                },
                BoxShadow {
                    color: hsla(0.0, 0.0, 0.0, 0.35),
                    offset: point(px(0.0), px(2.0)),
                    blur_radius: px(8.0),
                    spread_radius: px(0.0),
                },
            ])
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .child(settings_header().child(close_button().on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| {
                    cx.stop_propagation();
                    this.close(window, cx);
                }),
            )))
            .child(
                div()
                    .px_6()
                    .py_6()
                    .min_h(px(320.0))
                    .flex()
                    .flex_col()
                    .child(interface_card(self.sidebar_collapsed).on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|this, _, window, cx| {
                            cx.stop_propagation();
                            this.toggle_sidebar(window, cx);
                        }),
                    )),
            );

        div()
            .absolute()
            .size_full()
            .bg(theme::ink::veil())
            .flex()
            .items_center()
            .justify_center()
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| this.close(window, cx)),
            )
            .child(panel.on_mouse_down(
                MouseButton::Left,
                cx.listener(|_, _, _, cx| cx.stop_propagation()),
            ))
    }
}

fn settings_header() -> gpui::Div {
    div()
        .w_full()
        .px_5()
        .py_4()
        .border_b_1()
        .border_color(theme::line::weak())
        .bg(theme::ink::shade())
        .flex()
        .items_center()
        .justify_between()
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .w(px(3.0))
                        .h(px(31.0))
                        .rounded(px(theme::radius::R0))
                        .bg(theme::sodium::vapor())
                        .shadow(vec![BoxShadow {
                            color: theme::sodium::glow().into(),
                            offset: point(px(0.0), px(0.0)),
                            blur_radius: px(14.0),
                            spread_radius: px(0.0),
                        }]),
                )
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_color(theme::moon::moonstone())
                                .text_size(px(17.0))
                                .child(SharedString::from("Settings")),
                        )
                        .child(
                            div()
                                .text_color(theme::moon::ash())
                                .text_size(px(10.0))
                                .child(SharedString::from("Native client")),
                        ),
                ),
        )
}

fn interface_card(collapsed: bool) -> gpui::Div {
    div()
        .w_full()
        .flex_1()
        .rounded(px(theme::radius::R1))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(theme::line::mild())
        .p_5()
        .flex()
        .items_center()
        .justify_between()
        .gap_6()
        .child(
            div()
                .flex_1()
                .min_w(px(0.0))
                .flex()
                .flex_col()
                .gap_4()
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .gap_2()
                        .child(
                            div()
                                .text_color(theme::moon::moonstone())
                                .text_size(px(16.0))
                                .child(SharedString::from("Workspace rail")),
                        )
                        .child(
                            div()
                                .text_color(theme::moon::ash())
                                .text_size(px(11.0))
                                .child(SharedString::from(if collapsed {
                                    "Narrow"
                                } else {
                                    "Wide"
                                })),
                        ),
                )
                .child(sidebar_mode_control(collapsed)),
        )
        .child(sidebar_preview(collapsed))
}

fn sidebar_preview(collapsed: bool) -> gpui::Div {
    let rail_w = if collapsed { 48.0 } else { 146.0 };
    div()
        .w(px(220.0))
        .h(px(176.0))
        .rounded(px(theme::radius::R1))
        .bg(theme::ink::midnight())
        .border_1()
        .border_color(theme::line::weak())
        .p_2()
        .flex()
        .gap_2()
        .child(
            div()
                .w(px(rail_w))
                .h_full()
                .rounded(px(theme::radius::R0))
                .bg(theme::ink::nocturne())
                .border_1()
                .border_color(theme::line::mild())
                .p_2()
                .flex()
                .flex_col()
                .gap_2()
                .child(preview_row(collapsed, theme::state::working(), true))
                .child(preview_row(collapsed, theme::state::waiting(), false))
                .child(preview_row(collapsed, theme::state::approval(), false)),
        )
        .child(
            div()
                .flex_1()
                .h_full()
                .rounded(px(theme::radius::R0))
                .bg(theme::ink::void())
                .border_1()
                .border_color(theme::line::weak()),
        )
}

fn preview_row(collapsed: bool, color: gpui::Rgba, selected: bool) -> gpui::Div {
    let mut row = div()
        .w_full()
        .h(px(28.0))
        .rounded(px(theme::radius::R0))
        .bg(if selected {
            theme::surface::selected_row()
        } else {
            theme::ink::shade()
        })
        .border_l_2()
        .border_color(if selected {
            theme::sodium::vapor()
        } else {
            theme::ink::shade()
        })
        .flex()
        .items_center()
        .gap_2()
        .px_2()
        .child(div().w(px(7.0)).h(px(7.0)).rounded_full().bg(color));

    if !collapsed {
        row = row.child(
            div()
                .w(px(58.0))
                .h(px(5.0))
                .rounded(px(theme::radius::R0))
                .bg(if selected {
                    theme::moon::bone()
                } else {
                    theme::moon::cinder()
                }),
        );
    }

    row
}

fn close_button() -> gpui::Div {
    div()
        .w(px(28.0))
        .h(px(28.0))
        .flex()
        .items_center()
        .justify_center()
        .rounded(px(theme::radius::R0))
        .border_1()
        .border_color(theme::line::mild())
        .text_color(theme::moon::bone())
        .text_size(px(14.0))
        .child(SharedString::from("x"))
}

fn sidebar_mode_control(collapsed: bool) -> gpui::Div {
    div()
        .rounded(px(theme::radius::R1))
        .border_1()
        .border_color(theme::line::mild())
        .bg(theme::ink::midnight())
        .p_1()
        .flex()
        .items_center()
        .gap_1()
        .child(mode_segment("Wide", !collapsed))
        .child(mode_segment("Narrow", collapsed))
}

fn mode_segment(label: &'static str, active: bool) -> gpui::Div {
    let mut segment = div()
        .h(px(28.0))
        .px_3p5()
        .flex()
        .items_center()
        .justify_center()
        .rounded(px(theme::radius::R0))
        .text_size(px(11.0))
        .text_color(if active {
            theme::moon::moonstone()
        } else {
            theme::moon::ash()
        })
        .child(SharedString::from(label));

    if active {
        segment = segment
            .bg(theme::surface::selected_row())
            .border_1()
            .border_color(theme::sodium::deep());
    }

    segment
}
