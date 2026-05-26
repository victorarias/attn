use attn_protocol::{SessionState, WorkspaceStatus};
use gpui::{rgb, rgba, Rgba};

pub mod ink {
    use super::*;
    pub fn midnight() -> Rgba {
        rgb(0x0a0e16)
    }
    pub fn nocturne() -> Rgba {
        rgb(0x10151f)
    }
    pub fn shade() -> Rgba {
        rgb(0x161d2a)
    }
    pub fn border() -> Rgba {
        rgb(0x1f2837)
    }
    pub fn firm() -> Rgba {
        rgb(0x2a3548)
    }
}

pub mod moon {
    use super::*;
    pub fn primary() -> Rgba {
        rgb(0xf0ede0)
    }
    pub fn secondary() -> Rgba {
        rgb(0xcdc8b6)
    }
    pub fn dim() -> Rgba {
        rgb(0x8a8678)
    }
    pub fn ash() -> Rgba {
        rgb(0x545040)
    }
}

pub mod sodium {
    use super::*;
    pub fn vapor() -> Rgba {
        rgb(0xff8a3d)
    }
    pub fn soft() -> Rgba {
        rgba(0xff8a3d24)
    }
}

pub fn session_state_color(state: SessionState) -> Rgba {
    match state {
        SessionState::Launching => rgb(0x5bd5e8),
        SessionState::Working => rgb(0x62e08e),
        SessionState::WaitingInput => rgb(0xffd75e),
        SessionState::PendingApproval => rgb(0xff7a59),
        SessionState::Idle | SessionState::Unknown => moon::ash(),
    }
}

pub fn workspace_state_color(state: WorkspaceStatus) -> Rgba {
    match state {
        WorkspaceStatus::Launching => rgb(0x5bd5e8),
        WorkspaceStatus::Working => rgb(0x62e08e),
        WorkspaceStatus::WaitingInput => rgb(0xffd75e),
        WorkspaceStatus::PendingApproval => rgb(0xff7a59),
        WorkspaceStatus::Idle => moon::ash(),
    }
}
