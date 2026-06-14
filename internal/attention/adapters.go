package attention

import (
	"strings"
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
	return false
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

// WorkflowRunAdapter wraps a protocol.WorkflowRun to implement Source. The
// engine runs out-of-process; the daemon surfaces a finished run the user should
// notice (completed or failed) in the attention aggregator. A still-running run
// needs no notice, and a canceled run was dismissed by the user, so neither
// raises attention.
type WorkflowRunAdapter struct {
	Run *protocol.WorkflowRun
}

func (w WorkflowRunAdapter) AttentionID() string {
	return w.Run.RunID
}

func (w WorkflowRunAdapter) AttentionKind() string {
	return "workflow"
}

func (w WorkflowRunAdapter) AttentionLabel() string {
	if base := workflowScriptBaseName(w.Run.ScriptPath); base != "" {
		return base
	}
	return w.Run.RunID
}

func (w WorkflowRunAdapter) NeedsAttention() bool {
	switch w.Run.Status {
	case protocol.WorkflowRunStatusCompleted, protocol.WorkflowRunStatusFailed:
		return true
	default:
		return false
	}
}

func (w WorkflowRunAdapter) AttentionReason() string {
	if w.NeedsAttention() {
		return string(w.Run.Status)
	}
	return ""
}

func (w WorkflowRunAdapter) AttentionSince() time.Time {
	if w.Run.CompletedAt != nil {
		if t := protocol.Timestamp(*w.Run.CompletedAt).Time(); !t.IsZero() {
			return t
		}
	}
	return protocol.Timestamp(w.Run.UpdatedAt).Time()
}

func (w WorkflowRunAdapter) AttentionMuted() bool {
	return false
}

// workflowScriptBaseName returns the final path segment of a script path, used
// as a short attention label. It avoids importing path/filepath for one trim and
// tolerates both "/" and "\" separators defensively.
func workflowScriptBaseName(scriptPath string) string {
	trimmed := strings.TrimRight(scriptPath, "/\\")
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndexAny(trimmed, "/\\"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}
