#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub x: f32,
    pub y: f32,
    pub width: f32,
    pub height: f32,
}

impl Rect {
    pub fn right(&self) -> f32 {
        self.x + self.width
    }

    pub fn bottom(&self) -> f32 {
        self.y + self.height
    }

    fn contains(&self, other: &Rect) -> bool {
        other.x >= self.x
            && other.y >= self.y
            && other.right() <= self.right()
            && other.bottom() <= self.bottom()
    }

    fn overlaps(&self, other: &Rect) -> bool {
        self.x < other.right()
            && self.right() > other.x
            && self.y < other.bottom()
            && self.bottom() > other.y
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct PanelPlacementItem {
    pub id: usize,
    pub rect: Rect,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct PanelSize {
    pub width: f32,
    pub height: f32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AdjacentPanelDirection {
    Right,
    Bottom,
}

const GAP: f32 = 32.0;
const VISIBLE_MARGIN: f32 = 32.0;
const MIN_VISIBLE_WIDTH: f32 = 420.0;
const MIN_VISIBLE_HEIGHT: f32 = 280.0;

pub fn place_panel(
    existing: &[PanelPlacementItem],
    selected_id: Option<usize>,
    visible: Rect,
    default_size: PanelSize,
) -> Rect {
    let size = size_for_visible_rect(default_size, visible);
    if existing.is_empty() {
        return first_panel_rect(visible, size);
    }

    let candidates = clockwise_candidates_from_three_oclock(existing, selected_id, size);
    if let Some(candidate) = first_visible_non_overlapping(&candidates, existing, visible) {
        return candidate;
    }

    let visible_candidates = scan_visible_slots(existing, visible, size);
    if let Some(candidate) = visible_candidates.first().copied() {
        return candidate;
    }

    if let Some(candidate) = closest_non_overlapping(&candidates, existing, visible) {
        return candidate;
    }

    first_panel_rect(visible, size)
}

pub fn place_panel_adjacent(anchor: Rect, direction: AdjacentPanelDirection) -> Rect {
    match direction {
        AdjacentPanelDirection::Right => Rect {
            x: anchor.right() + GAP,
            y: anchor.y,
            width: anchor.width,
            height: anchor.height,
        },
        AdjacentPanelDirection::Bottom => Rect {
            x: anchor.x,
            y: anchor.bottom() + GAP,
            width: anchor.width,
            height: anchor.height,
        },
    }
}

pub fn place_panel_adjacent_avoiding(
    anchor: Rect,
    direction: AdjacentPanelDirection,
    existing: &[PanelPlacementItem],
) -> Rect {
    let mut candidate = place_panel_adjacent(anchor, direction);

    for _ in 0..existing.len() {
        let Some(blocking_edge) = blocking_edge_in_direction(&candidate, direction, existing)
        else {
            break;
        };

        match direction {
            AdjacentPanelDirection::Right => candidate.x = blocking_edge + GAP,
            AdjacentPanelDirection::Bottom => candidate.y = blocking_edge + GAP,
        }
    }

    candidate
}

fn blocking_edge_in_direction(
    candidate: &Rect,
    direction: AdjacentPanelDirection,
    existing: &[PanelPlacementItem],
) -> Option<f32> {
    existing
        .iter()
        .filter(|item| candidate.overlaps(&item.rect))
        .map(|item| match direction {
            AdjacentPanelDirection::Right => item.rect.right(),
            AdjacentPanelDirection::Bottom => item.rect.bottom(),
        })
        .reduce(f32::max)
}

fn size_for_visible_rect(default_size: PanelSize, visible: Rect) -> PanelSize {
    let available_width = visible.width - VISIBLE_MARGIN * 2.0;
    let available_height = visible.height - VISIBLE_MARGIN * 2.0;
    PanelSize {
        width: if available_width >= MIN_VISIBLE_WIDTH {
            default_size.width.min(available_width)
        } else {
            default_size.width
        },
        height: if available_height >= MIN_VISIBLE_HEIGHT {
            default_size.height.min(available_height)
        } else {
            default_size.height
        },
    }
}

fn first_panel_rect(visible: Rect, size: PanelSize) -> Rect {
    let x = if visible.width >= size.width + VISIBLE_MARGIN * 2.0 {
        visible.x + (visible.width - size.width) / 2.0
    } else {
        visible.x + VISIBLE_MARGIN
    };
    let y = if visible.height >= size.height + VISIBLE_MARGIN * 2.0 {
        visible.y + (visible.height - size.height) / 2.0
    } else {
        visible.y + VISIBLE_MARGIN
    };
    Rect {
        x,
        y,
        width: size.width,
        height: size.height,
    }
}

fn clockwise_candidates_from_three_oclock(
    existing: &[PanelPlacementItem],
    selected_id: Option<usize>,
    size: PanelSize,
) -> Vec<Rect> {
    let mut anchors = Vec::with_capacity(existing.len());
    if let Some(selected) = selected_id {
        if let Some(item) = existing.iter().find(|item| item.id == selected) {
            anchors.push(*item);
        }
    }
    for item in existing {
        if anchors
            .iter()
            .any(|anchor: &PanelPlacementItem| anchor.id == item.id)
        {
            continue;
        }
        anchors.push(*item);
    }

    let mut out = Vec::with_capacity(anchors.len() * 4);
    for anchor in anchors {
        let rect = anchor.rect;
        // Clockwise from 3 o'clock: right, down, left, up.
        out.extend([
            Rect {
                x: rect.right() + GAP,
                y: rect.y,
                width: size.width,
                height: size.height,
            },
            Rect {
                x: rect.x,
                y: rect.bottom() + GAP,
                width: size.width,
                height: size.height,
            },
            Rect {
                x: rect.x - size.width - GAP,
                y: rect.y,
                width: size.width,
                height: size.height,
            },
            Rect {
                x: rect.x,
                y: rect.y - size.height - GAP,
                width: size.width,
                height: size.height,
            },
        ]);
    }
    out
}

fn first_visible_non_overlapping(
    candidates: &[Rect],
    existing: &[PanelPlacementItem],
    visible: Rect,
) -> Option<Rect> {
    candidates
        .iter()
        .copied()
        .find(|candidate| visible.contains(candidate) && !overlaps_any(candidate, existing))
}

fn scan_visible_slots(
    existing: &[PanelPlacementItem],
    visible: Rect,
    size: PanelSize,
) -> Vec<Rect> {
    if visible.width < size.width || visible.height < size.height {
        return Vec::new();
    }

    let mut out = Vec::new();
    let min_x = visible.x + VISIBLE_MARGIN;
    let min_y = visible.y + VISIBLE_MARGIN;
    let max_x = (visible.right() - size.width - VISIBLE_MARGIN).max(min_x);
    let max_y = (visible.bottom() - size.height - VISIBLE_MARGIN).max(min_y);
    let step_x = (size.width + GAP).clamp(GAP, 240.0);
    let step_y = (size.height + GAP).clamp(GAP, 180.0);

    let mut y = min_y;
    while y <= max_y + 0.1 {
        let mut x = min_x;
        while x <= max_x + 0.1 {
            let candidate = Rect {
                x,
                y,
                width: size.width,
                height: size.height,
            };
            if !overlaps_any(&candidate, existing) {
                out.push(candidate);
            }
            x += step_x;
        }
        y += step_y;
    }
    out
}

fn closest_non_overlapping(
    candidates: &[Rect],
    existing: &[PanelPlacementItem],
    visible: Rect,
) -> Option<Rect> {
    candidates
        .iter()
        .copied()
        .filter(|candidate| !overlaps_any(candidate, existing))
        .min_by(|a, b| {
            compare_f32(distance_to_rect(*a, visible), distance_to_rect(*b, visible))
                .then_with(|| compare_f32(visible_area(*b, visible), visible_area(*a, visible)))
                .then_with(|| compare_f32(a.y, b.y))
                .then_with(|| compare_f32(a.x, b.x))
        })
}

fn overlaps_any(candidate: &Rect, existing: &[PanelPlacementItem]) -> bool {
    existing.iter().any(|item| candidate.overlaps(&item.rect))
}

fn distance_to_rect(candidate: Rect, visible: Rect) -> f32 {
    let dx = if candidate.right() < visible.x {
        visible.x - candidate.right()
    } else if candidate.x > visible.right() {
        candidate.x - visible.right()
    } else {
        0.0
    };
    let dy = if candidate.bottom() < visible.y {
        visible.y - candidate.bottom()
    } else if candidate.y > visible.bottom() {
        candidate.y - visible.bottom()
    } else {
        0.0
    };
    dx.hypot(dy)
}

fn visible_area(candidate: Rect, visible: Rect) -> f32 {
    let width = (candidate.right().min(visible.right()) - candidate.x.max(visible.x)).max(0.0);
    let height = (candidate.bottom().min(visible.bottom()) - candidate.y.max(visible.y)).max(0.0);
    width * height
}

fn compare_f32(a: f32, b: f32) -> std::cmp::Ordering {
    a.partial_cmp(&b).unwrap_or(std::cmp::Ordering::Equal)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: usize, x: f32, y: f32, width: f32, height: f32) -> PanelPlacementItem {
        PanelPlacementItem {
            id,
            rect: Rect {
                x,
                y,
                width,
                height,
            },
        }
    }

    fn visible() -> Rect {
        Rect {
            x: 0.0,
            y: 0.0,
            width: 1280.0,
            height: 800.0,
        }
    }

    fn size() -> PanelSize {
        PanelSize {
            width: 720.0,
            height: 480.0,
        }
    }

    #[test]
    fn first_panel_centers_in_visible_viewport() {
        let placed = place_panel(&[], None, visible(), size());
        assert_eq!(
            placed,
            Rect {
                x: 280.0,
                y: 160.0,
                width: 720.0,
                height: 480.0,
            }
        );
    }

    #[test]
    fn uses_first_clockwise_visible_slot_around_selected_panel() {
        let existing = [item(1, 40.0, 120.0, 320.0, 240.0)];
        let placed = place_panel(&existing, Some(1), visible(), size());
        assert_eq!(placed.x, 392.0);
        assert_eq!(placed.y, 120.0);
    }

    #[test]
    fn clockwise_candidates_start_at_three_oclock() {
        let existing = [item(1, 100.0, 200.0, 300.0, 150.0)];
        let candidates = clockwise_candidates_from_three_oclock(
            &existing,
            Some(1),
            PanelSize {
                width: 80.0,
                height: 60.0,
            },
        );
        assert_eq!(
            &candidates[..4],
            &[
                Rect {
                    x: 432.0,
                    y: 200.0,
                    width: 80.0,
                    height: 60.0,
                },
                Rect {
                    x: 100.0,
                    y: 382.0,
                    width: 80.0,
                    height: 60.0,
                },
                Rect {
                    x: -12.0,
                    y: 200.0,
                    width: 80.0,
                    height: 60.0,
                },
                Rect {
                    x: 100.0,
                    y: 108.0,
                    width: 80.0,
                    height: 60.0,
                },
            ]
        );
    }

    #[test]
    fn tries_down_after_right_when_right_slot_is_not_visible() {
        let existing = [item(1, 480.0, 40.0, 720.0, 320.0)];
        let placed = place_panel(&existing, Some(1), visible(), size());
        assert_eq!(placed.x, 480.0);
        assert_eq!(placed.y, 392.0);
    }

    #[test]
    fn tries_clockwise_slots_around_other_existing_panels_before_going_offscreen() {
        let existing = [
            item(1, 350.0, 250.0, 200.0, 150.0),
            item(2, 582.0, 250.0, 300.0, 200.0),
            item(3, 350.0, 432.0, 300.0, 200.0),
            item(4, 18.0, 250.0, 300.0, 200.0),
            item(5, 350.0, 18.0, 300.0, 200.0),
        ];
        let placed = place_panel(
            &existing,
            Some(1),
            Rect {
                x: 0.0,
                y: 0.0,
                width: 1000.0,
                height: 700.0,
            },
            PanelSize {
                width: 300.0,
                height: 200.0,
            },
        );
        assert_eq!(placed.x, 18.0);
        assert_eq!(placed.y, 482.0);
    }

    #[test]
    fn falls_back_to_closest_non_overlapping_clockwise_slot_when_view_is_full() {
        let existing = [
            item(1, 0.0, 0.0, 720.0, 480.0),
            item(2, 752.0, 0.0, 720.0, 480.0),
            item(3, 0.0, 512.0, 720.0, 480.0),
        ];
        let placed = place_panel(&existing, Some(1), visible(), size());
        assert_eq!(placed.x, 752.0);
        assert_eq!(placed.y, 512.0);
    }

    #[test]
    fn shrinks_to_fit_visible_view_when_there_is_room_above_minimum() {
        let placed = place_panel(
            &[],
            None,
            Rect {
                x: 100.0,
                y: 200.0,
                width: 620.0,
                height: 420.0,
            },
            size(),
        );
        assert_eq!(placed.width, 556.0);
        assert_eq!(placed.height, 356.0);
        assert_eq!(placed.x, 132.0);
        assert_eq!(placed.y, 232.0);
    }

    #[test]
    fn adjacent_right_preserves_anchor_size() {
        let anchor = Rect {
            x: 100.0,
            y: 200.0,
            width: 640.0,
            height: 420.0,
        };

        assert_eq!(
            place_panel_adjacent(anchor, AdjacentPanelDirection::Right),
            Rect {
                x: 772.0,
                y: 200.0,
                width: 640.0,
                height: 420.0,
            }
        );
    }

    #[test]
    fn adjacent_bottom_preserves_anchor_size() {
        let anchor = Rect {
            x: 100.0,
            y: 200.0,
            width: 640.0,
            height: 420.0,
        };

        assert_eq!(
            place_panel_adjacent(anchor, AdjacentPanelDirection::Bottom),
            Rect {
                x: 100.0,
                y: 652.0,
                width: 640.0,
                height: 420.0,
            }
        );
    }

    #[test]
    fn adjacent_right_skips_occupied_neighbors_in_row() {
        let existing = [
            item(1, 0.0, 120.0, 320.0, 240.0),
            item(2, 352.0, 120.0, 320.0, 240.0),
            item(3, 704.0, 120.0, 320.0, 240.0),
        ];

        assert_eq!(
            place_panel_adjacent_avoiding(
                existing[1].rect,
                AdjacentPanelDirection::Right,
                &existing,
            ),
            Rect {
                x: 1056.0,
                y: 120.0,
                width: 320.0,
                height: 240.0,
            }
        );
    }

    #[test]
    fn adjacent_bottom_skips_occupied_neighbors_in_column() {
        let existing = [
            item(1, 120.0, 0.0, 320.0, 240.0),
            item(2, 120.0, 272.0, 320.0, 240.0),
            item(3, 120.0, 544.0, 320.0, 240.0),
        ];

        assert_eq!(
            place_panel_adjacent_avoiding(
                existing[1].rect,
                AdjacentPanelDirection::Bottom,
                &existing,
            ),
            Rect {
                x: 120.0,
                y: 816.0,
                width: 320.0,
                height: 240.0,
            }
        );
    }

    #[test]
    fn adjacent_right_ignores_vertically_separate_panels() {
        let anchor = Rect {
            x: 352.0,
            y: 120.0,
            width: 320.0,
            height: 240.0,
        };
        let existing = [
            PanelPlacementItem {
                id: 1,
                rect: anchor,
            },
            item(2, 704.0, 520.0, 320.0, 240.0),
        ];

        assert_eq!(
            place_panel_adjacent_avoiding(anchor, AdjacentPanelDirection::Right, &existing),
            Rect {
                x: 704.0,
                y: 120.0,
                width: 320.0,
                height: 240.0,
            }
        );
    }
}
