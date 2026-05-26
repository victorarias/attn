//! Panel geometry snapping for native canvas drag and resize.

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct PanelRect {
    pub x: f32,
    pub y: f32,
    pub width: f32,
    pub height: f32,
}

#[derive(Clone, Copy, Debug)]
pub struct ResizeEdges {
    pub left: bool,
    pub top: bool,
    pub right: bool,
    pub bottom: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SnapAxis {
    X,
    Y,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct SnapLine {
    pub axis: SnapAxis,
    pub position: f32,
    pub start: f32,
    pub end: f32,
}

#[derive(Clone, Debug, PartialEq)]
pub struct SnapResult {
    pub rect: PanelRect,
    pub lines: Vec<SnapLine>,
}

const SNAP_GAP: f32 = 32.0;

impl ResizeEdges {
    pub const fn new(left: bool, top: bool, right: bool, bottom: bool) -> Self {
        Self {
            left,
            top,
            right,
            bottom,
        }
    }
}

pub fn snap_panel_move(rect: PanelRect, targets: &[PanelRect], threshold: f32) -> SnapResult {
    let x_match = best_axis_match(
        &moving_x_anchors(rect),
        &target_x_anchors(targets),
        threshold,
    );
    let y_match = best_axis_match(
        &moving_y_anchors(rect),
        &target_y_anchors(targets),
        threshold,
    );
    let mut snapped = rect;
    let mut lines = Vec::new();

    if let Some(axis_match) = x_match {
        snapped.x += axis_match.delta();
    }
    if let Some(axis_match) = y_match {
        snapped.y += axis_match.delta();
    }
    if let Some(axis_match) = x_match {
        lines.push(axis_match.line(SnapAxis::X, snapped.y, snapped.bottom()));
    }
    if let Some(axis_match) = y_match {
        lines.push(axis_match.line(SnapAxis::Y, snapped.x, snapped.right()));
    }

    SnapResult {
        rect: snapped,
        lines,
    }
}

pub fn snap_panel_resize(
    rect: PanelRect,
    edges: ResizeEdges,
    targets: &[PanelRect],
    min_width: f32,
    min_height: f32,
    threshold: f32,
) -> SnapResult {
    let mut left = rect.x;
    let mut top = rect.y;
    let mut right = rect.x + rect.width;
    let mut bottom = rect.y + rect.height;
    let x_targets = target_x_anchors(targets);
    let y_targets = target_y_anchors(targets);

    if edges.left && right - left < min_width {
        left = right - min_width;
    }
    if edges.right && right - left < min_width {
        right = left + min_width;
    }
    if edges.top && bottom - top < min_height {
        top = bottom - min_height;
    }
    if edges.bottom && bottom - top < min_height {
        bottom = top + min_height;
    }

    let x_match = best_axis_match(
        &resizing_x_anchors(left, right, edges),
        &x_targets,
        threshold,
    );
    if let Some(axis_match) = x_match {
        if edges.left && right - axis_match.target.position >= min_width {
            left = axis_match.target.position;
        } else if edges.right && axis_match.target.position - left >= min_width {
            right = axis_match.target.position;
        }
    }

    let y_match = best_axis_match(
        &resizing_y_anchors(top, bottom, edges),
        &y_targets,
        threshold,
    );
    if let Some(axis_match) = y_match {
        if edges.top && bottom - axis_match.target.position >= min_height {
            top = axis_match.target.position;
        } else if edges.bottom && axis_match.target.position - top >= min_height {
            bottom = axis_match.target.position;
        }
    }

    let rect = PanelRect {
        x: left,
        y: top,
        width: right - left,
        height: bottom - top,
    };
    let mut lines = Vec::new();
    if let Some(axis_match) = x_match {
        let position = if edges.left { left } else { right };
        if (axis_match.target.position - position).abs() < 0.001 {
            lines.push(axis_match.line(SnapAxis::X, rect.y, rect.bottom()));
        }
    }
    if let Some(axis_match) = y_match {
        let position = if edges.top { top } else { bottom };
        if (axis_match.target.position - position).abs() < 0.001 {
            lines.push(axis_match.line(SnapAxis::Y, rect.x, rect.right()));
        }
    }

    SnapResult { rect, lines }
}

#[derive(Clone, Copy, Debug)]
struct MovingAnchor {
    position: f32,
    kind: AnchorKind,
}

#[derive(Clone, Copy, Debug)]
struct TargetAnchor {
    position: f32,
    span_start: f32,
    span_end: f32,
    kind: AnchorKind,
}

#[derive(Clone, Copy, Debug)]
struct AxisMatch {
    moving: MovingAnchor,
    target: TargetAnchor,
    distance: f32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum AnchorKind {
    Start,
    Center,
    End,
}

impl AxisMatch {
    fn delta(&self) -> f32 {
        self.target.position - self.moving.position
    }

    fn line(&self, axis: SnapAxis, active_start: f32, active_end: f32) -> SnapLine {
        SnapLine {
            axis,
            position: self.target.position,
            start: active_start.min(active_end).min(self.target.span_start),
            end: active_start.max(active_end).max(self.target.span_end),
        }
    }
}

fn best_axis_match(
    moving: &[MovingAnchor],
    targets: &[TargetAnchor],
    threshold: f32,
) -> Option<AxisMatch> {
    let mut best: Option<AxisMatch> = None;
    for moving in moving {
        for target in targets {
            if moving.kind != target.kind {
                continue;
            }
            let distance = (moving.position - target.position).abs();
            if distance > threshold {
                continue;
            }
            if best.is_none_or(|candidate| distance < candidate.distance) {
                best = Some(AxisMatch {
                    moving: *moving,
                    target: *target,
                    distance,
                });
            }
        }
    }
    best
}

impl PanelRect {
    fn right(&self) -> f32 {
        self.x + self.width
    }

    fn bottom(&self) -> f32 {
        self.y + self.height
    }

    fn center_x(&self) -> f32 {
        self.x + self.width / 2.0
    }

    fn center_y(&self) -> f32 {
        self.y + self.height / 2.0
    }
}

fn moving_x_anchors(rect: PanelRect) -> [MovingAnchor; 3] {
    [
        MovingAnchor {
            position: rect.x,
            kind: AnchorKind::Start,
        },
        MovingAnchor {
            position: rect.center_x(),
            kind: AnchorKind::Center,
        },
        MovingAnchor {
            position: rect.right(),
            kind: AnchorKind::End,
        },
    ]
}

fn moving_y_anchors(rect: PanelRect) -> [MovingAnchor; 3] {
    [
        MovingAnchor {
            position: rect.y,
            kind: AnchorKind::Start,
        },
        MovingAnchor {
            position: rect.center_y(),
            kind: AnchorKind::Center,
        },
        MovingAnchor {
            position: rect.bottom(),
            kind: AnchorKind::End,
        },
    ]
}

fn resizing_x_anchors(left: f32, right: f32, edges: ResizeEdges) -> Vec<MovingAnchor> {
    let mut anchors = Vec::new();
    if edges.left {
        anchors.push(MovingAnchor {
            position: left,
            kind: AnchorKind::Start,
        });
    }
    if edges.right {
        anchors.push(MovingAnchor {
            position: right,
            kind: AnchorKind::End,
        });
    }
    anchors
}

fn resizing_y_anchors(top: f32, bottom: f32, edges: ResizeEdges) -> Vec<MovingAnchor> {
    let mut anchors = Vec::new();
    if edges.top {
        anchors.push(MovingAnchor {
            position: top,
            kind: AnchorKind::Start,
        });
    }
    if edges.bottom {
        anchors.push(MovingAnchor {
            position: bottom,
            kind: AnchorKind::End,
        });
    }
    anchors
}

fn target_x_anchors(targets: &[PanelRect]) -> Vec<TargetAnchor> {
    let mut anchors = Vec::with_capacity(targets.len() * 5);
    for target in targets {
        anchors.extend([
            TargetAnchor {
                position: target.x,
                span_start: target.y,
                span_end: target.bottom(),
                kind: AnchorKind::Start,
            },
            TargetAnchor {
                position: target.center_x(),
                span_start: target.y,
                span_end: target.bottom(),
                kind: AnchorKind::Center,
            },
            TargetAnchor {
                position: target.right(),
                span_start: target.y,
                span_end: target.bottom(),
                kind: AnchorKind::End,
            },
            TargetAnchor {
                position: target.x - SNAP_GAP,
                span_start: target.y,
                span_end: target.bottom(),
                kind: AnchorKind::End,
            },
            TargetAnchor {
                position: target.right() + SNAP_GAP,
                span_start: target.y,
                span_end: target.bottom(),
                kind: AnchorKind::Start,
            },
        ]);
    }
    anchors
}

fn target_y_anchors(targets: &[PanelRect]) -> Vec<TargetAnchor> {
    let mut anchors = Vec::with_capacity(targets.len() * 5);
    for target in targets {
        anchors.extend([
            TargetAnchor {
                position: target.y,
                span_start: target.x,
                span_end: target.right(),
                kind: AnchorKind::Start,
            },
            TargetAnchor {
                position: target.center_y(),
                span_start: target.x,
                span_end: target.right(),
                kind: AnchorKind::Center,
            },
            TargetAnchor {
                position: target.bottom(),
                span_start: target.x,
                span_end: target.right(),
                kind: AnchorKind::End,
            },
            TargetAnchor {
                position: target.y - SNAP_GAP,
                span_start: target.x,
                span_end: target.right(),
                kind: AnchorKind::End,
            },
            TargetAnchor {
                position: target.bottom() + SNAP_GAP,
                span_start: target.x,
                span_end: target.right(),
                kind: AnchorKind::Start,
            },
        ]);
    }
    anchors
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn move_snaps_edges_to_nearby_panel_edges() {
        let result = snap_panel_move(
            PanelRect {
                x: 329.0,
                y: 126.0,
                width: 200.0,
                height: 120.0,
            },
            &[PanelRect {
                x: 100.0,
                y: 120.0,
                width: 200.0,
                height: 120.0,
            }],
            10.0,
        );

        assert_eq!(
            result.rect,
            PanelRect {
                x: 332.0,
                y: 120.0,
                width: 200.0,
                height: 120.0,
            }
        );
        assert_eq!(
            result.lines,
            vec![
                SnapLine {
                    axis: SnapAxis::X,
                    position: 332.0,
                    start: 120.0,
                    end: 240.0,
                },
                SnapLine {
                    axis: SnapAxis::Y,
                    position: 120.0,
                    start: 100.0,
                    end: 532.0,
                },
            ]
        );
    }

    #[test]
    fn move_snaps_center_to_nearby_panel_center() {
        let result = snap_panel_move(
            PanelRect {
                x: 475.0,
                y: 310.0,
                width: 100.0,
                height: 100.0,
            },
            &[PanelRect {
                x: 400.0,
                y: 100.0,
                width: 240.0,
                height: 160.0,
            }],
            12.0,
        );

        assert_eq!(
            result.rect,
            PanelRect {
                x: 470.0,
                y: 310.0,
                width: 100.0,
                height: 100.0,
            }
        );
        assert_eq!(
            result.lines,
            vec![SnapLine {
                axis: SnapAxis::X,
                position: 520.0,
                start: 100.0,
                end: 410.0,
            }]
        );
    }

    #[test]
    fn move_does_not_snap_without_a_nearby_panel_anchor() {
        let result = snap_panel_move(
            PanelRect {
                x: 321.0,
                y: 276.0,
                width: 200.0,
                height: 120.0,
            },
            &[PanelRect {
                x: 100.0,
                y: 100.0,
                width: 160.0,
                height: 90.0,
            }],
            10.0,
        );

        assert_eq!(
            result.rect,
            PanelRect {
                x: 321.0,
                y: 276.0,
                width: 200.0,
                height: 120.0,
            }
        );
        assert!(result.lines.is_empty());
    }

    #[test]
    fn bottom_right_resize_snaps_moved_edges_to_panel_edges() {
        let result = snap_panel_resize(
            PanelRect {
                x: 100.0,
                y: 100.0,
                width: 177.0,
                height: 146.0,
            },
            ResizeEdges::new(false, false, true, true),
            &[PanelRect {
                x: 310.0,
                y: 280.0,
                width: 180.0,
                height: 120.0,
            }],
            120.0,
            80.0,
            10.0,
        );

        assert_eq!(
            result.rect,
            PanelRect {
                x: 100.0,
                y: 100.0,
                width: 178.0,
                height: 148.0,
            }
        );
        assert_eq!(
            result.lines,
            vec![
                SnapLine {
                    axis: SnapAxis::X,
                    position: 278.0,
                    start: 100.0,
                    end: 400.0,
                },
                SnapLine {
                    axis: SnapAxis::Y,
                    position: 248.0,
                    start: 100.0,
                    end: 490.0,
                },
            ]
        );
    }

    #[test]
    fn top_left_resize_snaps_moved_edges_and_preserves_opposite_corner() {
        let result = snap_panel_resize(
            PanelRect {
                x: 185.0,
                y: 239.0,
                width: 215.0,
                height: 161.0,
            },
            ResizeEdges::new(true, true, false, false),
            &[PanelRect {
                x: 100.0,
                y: 120.0,
                width: 50.0,
                height: 90.0,
            }],
            120.0,
            80.0,
            10.0,
        );

        assert_eq!(
            result.rect,
            PanelRect {
                x: 182.0,
                y: 242.0,
                width: 218.0,
                height: 158.0,
            }
        );
    }

    #[test]
    fn resize_respects_minimum_size_over_nearby_anchor() {
        let result = snap_panel_resize(
            PanelRect {
                x: 100.0,
                y: 100.0,
                width: 21.0,
                height: 19.0,
            },
            ResizeEdges::new(false, false, true, true),
            &[PanelRect {
                x: 120.0,
                y: 80.0,
                width: 40.0,
                height: 40.0,
            }],
            120.0,
            80.0,
            30.0,
        );

        assert_eq!(
            result.rect,
            PanelRect {
                x: 100.0,
                y: 100.0,
                width: 120.0,
                height: 80.0,
            }
        );
        assert!(result.lines.is_empty());
    }
}
