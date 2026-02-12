package attention

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// SessionAdapter wraps a protocol.Session to implement Source.
type SessionAdapter struct {
	Session *protocol.Session
}

func (s SessionAdapter) AttentionID() string {
	return s.Session.ID
}

func (s SessionAdapter) AttentionKind() string {
	return "session"
}

func (s SessionAdapter) AttentionLabel() string {
	return s.Session.Label
}

func (s SessionAdapter) NeedsAttention() bool {
	if s.Session.Muted {
		return false
	}
	return s.Session.State == protocol.SessionStateWaitingInput ||
		s.Session.State == protocol.SessionStatePendingApproval ||
		s.Session.State == protocol.SessionStateUnknown
}

func (s SessionAdapter) AttentionReason() string {
	if s.Session.State == protocol.SessionStateWaitingInput {
		return "waiting_input"
	}
	if s.Session.State == protocol.SessionStatePendingApproval {
		return "pending_approval"
	}
	if s.Session.State == protocol.SessionStateUnknown {
		return "unknown"
	}
	return ""
}

func (s SessionAdapter) AttentionSince() time.Time {
	return protocol.Timestamp(s.Session.StateSince).Time()
}

func (s SessionAdapter) AttentionMuted() bool {
	return s.Session.Muted
}

// PRAdapter wraps a protocol.PR to implement Source.
type PRAdapter struct {
	PR          *protocol.PR
	RepoMuted   bool // Whether the PR's repo is muted
	AuthorMuted bool // Whether the PR's author is muted
}

func (p PRAdapter) AttentionID() string {
	return p.PR.ID
}

func (p PRAdapter) AttentionKind() string {
	return "pr"
}

func (p PRAdapter) AttentionLabel() string {
	return p.PR.Title
}

func (p PRAdapter) NeedsAttention() bool {
	return p.PR.State == protocol.PRStateWaiting && !p.PR.Muted && !p.RepoMuted && !p.AuthorMuted
}

func (p PRAdapter) AttentionReason() string {
	if p.PR.State == protocol.PRStateWaiting {
		return p.PR.Reason
	}
	return ""
}

func (p PRAdapter) AttentionSince() time.Time {
	return protocol.Timestamp(p.PR.LastUpdated).Time()
}

func (p PRAdapter) AttentionMuted() bool {
	return p.PR.Muted || p.RepoMuted || p.AuthorMuted
}
