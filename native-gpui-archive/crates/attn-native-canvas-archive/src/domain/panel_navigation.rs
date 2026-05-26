use std::cmp::Ordering;

const DIAGONAL_FALLBACK_MAX_SLOPE: f32 = 2.15;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum NavigationDirection {
    Next,
    Previous,
    Up,
    Down,
    Left,
    Right,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct PanelNavItem {
    pub id: usize,
    pub world_x: f32,
    pub world_y: f32,
    pub width: f32,
    pub height: f32,
}

impl PanelNavItem {
    fn center(self) -> (f32, f32) {
        (
            self.world_x + self.width / 2.0,
            self.world_y + self.height / 2.0,
        )
    }
}

pub fn navigate_panel(
    panels: &[PanelNavItem],
    current_id: Option<usize>,
    direction: NavigationDirection,
) -> Option<usize> {
    if panels.is_empty() {
        return None;
    }
    if current_id
        .and_then(|id| panels.iter().find(|p| p.id == id))
        .is_none()
    {
        return navigate_reading_order(panels, None, NavigationDirection::Next);
    }
    match direction {
        NavigationDirection::Next | NavigationDirection::Previous => {
            navigate_reading_order(panels, current_id, direction)
        }
        NavigationDirection::Up
        | NavigationDirection::Down
        | NavigationDirection::Left
        | NavigationDirection::Right => navigate_spatial(panels, current_id, direction),
    }
}

fn navigate_reading_order(
    panels: &[PanelNavItem],
    current_id: Option<usize>,
    direction: NavigationDirection,
) -> Option<usize> {
    let mut ordered = panels.to_vec();
    ordered.sort_by(compare_reading_order);

    let current_idx = current_id.and_then(|id| ordered.iter().position(|p| p.id == id));
    let next_idx = match (current_idx, direction) {
        (Some(idx), NavigationDirection::Next) => (idx + 1) % ordered.len(),
        (Some(0), NavigationDirection::Previous) => ordered.len() - 1,
        (Some(idx), NavigationDirection::Previous) => idx - 1,
        (None, _) => 0,
        (_, _) => return None,
    };
    ordered.get(next_idx).map(|p| p.id)
}

fn navigate_spatial(
    panels: &[PanelNavItem],
    current_id: Option<usize>,
    direction: NavigationDirection,
) -> Option<usize> {
    let current = current_id
        .and_then(|id| panels.iter().find(|p| p.id == id).copied())
        .expect("navigate_panel validates current_id before spatial navigation");
    let candidates: Vec<SpatialCandidate> = panels
        .iter()
        .copied()
        .filter(|p| p.id != current.id)
        .filter_map(|candidate| spatial_candidate(current, candidate, direction))
        .collect();

    let has_beam_candidates = candidates.iter().any(|candidate| candidate.in_beam);
    candidates
        .into_iter()
        .filter(|candidate| {
            if has_beam_candidates {
                candidate.in_beam
            } else {
                candidate.cross / candidate.primary.max(1.0) <= DIAGONAL_FALLBACK_MAX_SLOPE
            }
        })
        .min_by(compare_spatial_candidate)
        .map(|candidate| candidate.panel.id)
}

#[derive(Clone, Copy, Debug)]
struct SpatialCandidate {
    panel: PanelNavItem,
    primary: f32,
    cross: f32,
    overlap: f32,
    in_beam: bool,
}

fn spatial_candidate(
    current: PanelNavItem,
    candidate: PanelNavItem,
    direction: NavigationDirection,
) -> Option<SpatialCandidate> {
    let (cx, cy) = current.center();
    let (x, y) = candidate.center();
    let (primary, cross) = match direction {
        NavigationDirection::Up if y < cy => (
            (current.world_y - candidate.bottom()).max(0.0),
            interval_gap(
                current.world_x,
                current.right(),
                candidate.world_x,
                candidate.right(),
            ),
        ),
        NavigationDirection::Down if y > cy => (
            (candidate.world_y - current.bottom()).max(0.0),
            interval_gap(
                current.world_x,
                current.right(),
                candidate.world_x,
                candidate.right(),
            ),
        ),
        NavigationDirection::Left if x < cx => (
            (current.world_x - candidate.right()).max(0.0),
            interval_gap(
                current.world_y,
                current.bottom(),
                candidate.world_y,
                candidate.bottom(),
            ),
        ),
        NavigationDirection::Right if x > cx => (
            (candidate.world_x - current.right()).max(0.0),
            interval_gap(
                current.world_y,
                current.bottom(),
                candidate.world_y,
                candidate.bottom(),
            ),
        ),
        _ => return None,
    };
    Some(SpatialCandidate {
        panel: candidate,
        primary,
        cross,
        overlap: perpendicular_overlap(current, candidate, direction),
        in_beam: cross == 0.0,
    })
}

impl PanelNavItem {
    fn right(self) -> f32 {
        self.world_x + self.width
    }

    fn bottom(self) -> f32 {
        self.world_y + self.height
    }
}

fn interval_gap(a_start: f32, a_end: f32, b_start: f32, b_end: f32) -> f32 {
    if a_end < b_start {
        b_start - a_end
    } else if b_end < a_start {
        a_start - b_end
    } else {
        0.0
    }
}

fn perpendicular_overlap(
    current: PanelNavItem,
    candidate: PanelNavItem,
    direction: NavigationDirection,
) -> f32 {
    match direction {
        NavigationDirection::Up | NavigationDirection::Down => interval_overlap(
            current.world_x,
            current.right(),
            candidate.world_x,
            candidate.right(),
        ),
        NavigationDirection::Left | NavigationDirection::Right => interval_overlap(
            current.world_y,
            current.bottom(),
            candidate.world_y,
            candidate.bottom(),
        ),
        NavigationDirection::Next | NavigationDirection::Previous => 0.0,
    }
}

fn interval_overlap(a_start: f32, a_end: f32, b_start: f32, b_end: f32) -> f32 {
    (a_end.min(b_end) - a_start.max(b_start)).max(0.0)
}

fn compare_spatial_candidate(a: &SpatialCandidate, b: &SpatialCandidate) -> Ordering {
    compare_f32(a.primary, b.primary)
        .then_with(|| compare_f32(b.overlap, a.overlap))
        .then_with(|| compare_f32(a.cross, b.cross))
        .then_with(|| compare_reading_order(&a.panel, &b.panel))
}

fn compare_reading_order(a: &PanelNavItem, b: &PanelNavItem) -> Ordering {
    compare_f32(a.world_y, b.world_y)
        .then_with(|| compare_f32(a.world_x, b.world_x))
        .then_with(|| a.id.cmp(&b.id))
}

fn compare_f32(a: f32, b: f32) -> Ordering {
    a.partial_cmp(&b).unwrap_or(Ordering::Equal)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn panel(id: usize, world_x: f32, world_y: f32) -> PanelNavItem {
        PanelNavItem {
            id,
            world_x,
            world_y,
            width: 100.0,
            height: 80.0,
        }
    }

    #[test]
    fn next_and_previous_cycle_in_reading_order() {
        let panels = [
            panel(3, 200.0, 100.0),
            panel(1, 0.0, 0.0),
            panel(2, 200.0, 0.0),
        ];

        assert_eq!(
            navigate_panel(&panels, Some(1), NavigationDirection::Next),
            Some(2)
        );
        assert_eq!(
            navigate_panel(&panels, Some(2), NavigationDirection::Next),
            Some(3)
        );
        assert_eq!(
            navigate_panel(&panels, Some(1), NavigationDirection::Previous),
            Some(3)
        );
    }

    #[test]
    fn spatial_navigation_picks_nearest_candidate_in_direction() {
        let panels = [
            panel(1, 100.0, 100.0),
            panel(2, 240.0, 100.0),
            panel(3, 100.0, 230.0),
            panel(4, 500.0, 100.0),
        ];

        assert_eq!(
            navigate_panel(&panels, Some(1), NavigationDirection::Right),
            Some(2)
        );
        assert_eq!(
            navigate_panel(&panels, Some(1), NavigationDirection::Down),
            Some(3)
        );
    }

    #[test]
    fn missing_current_selects_first_panel_in_reading_order() {
        let panels = [panel(2, 200.0, 0.0), panel(1, 0.0, 0.0)];

        assert_eq!(
            navigate_panel(&panels, None, NavigationDirection::Right),
            Some(1)
        );
        assert_eq!(
            navigate_panel(&panels, None, NavigationDirection::Next),
            Some(1)
        );
    }

    #[test]
    fn spatial_navigation_does_not_wrap_at_edges() {
        let panels = [panel(1, 0.0, 0.0), panel(2, 200.0, 0.0)];

        assert_eq!(
            navigate_panel(&panels, Some(2), NavigationDirection::Right),
            None
        );
    }

    #[test]
    fn screenshot_layout_prefers_straight_ahead_beam() {
        let panels = screenshot_layout();

        assert_eq!(
            navigate_panel(&panels, Some(2), NavigationDirection::Right),
            Some(3),
            "B right should go to C, not the lower F panel"
        );
        assert_eq!(
            navigate_panel(&panels, Some(3), NavigationDirection::Down),
            Some(6),
            "C down should go to F because F is in C's vertical beam"
        );
        assert_eq!(
            navigate_panel(&panels, Some(6), NavigationDirection::Up),
            Some(3),
            "F up should return to C"
        );
        assert_eq!(
            navigate_panel(&panels, Some(2), NavigationDirection::Down),
            Some(6),
            "B down should go to F because the panel rectangles overlap horizontally"
        );
    }

    fn screenshot_layout() -> [PanelNavItem; 6] {
        [
            PanelNavItem {
                id: 1,
                world_x: 340.0,
                world_y: 84.0,
                width: 205.0,
                height: 129.0,
            },
            PanelNavItem {
                id: 2,
                world_x: 562.0,
                world_y: 84.0,
                width: 205.0,
                height: 129.0,
            },
            PanelNavItem {
                id: 3,
                world_x: 784.0,
                world_y: 84.0,
                width: 205.0,
                height: 129.0,
            },
            PanelNavItem {
                id: 4,
                world_x: 110.0,
                world_y: 232.0,
                width: 205.0,
                height: 129.0,
            },
            PanelNavItem {
                id: 5,
                world_x: 340.0,
                world_y: 232.0,
                width: 205.0,
                height: 129.0,
            },
            PanelNavItem {
                id: 6,
                world_x: 738.0,
                world_y: 446.0,
                width: 205.0,
                height: 129.0,
            },
        ]
    }
}
