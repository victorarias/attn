package automation

import "time"

// ticketSweptReleaseReason mirrors store.AutomationBindingReleasedTicketSwept;
// duplicated because this package cannot import internal/store (see BindingStore).
const ticketSweptReleaseReason = "ticket_swept"

// Binding is a continuity thread's stable identity, as durably recorded by
// store.AutomationContinuityBinding; only what a continuation decision needs.
type Binding struct {
	TicketID, SessionID, WorkspaceID, PaneID string
}

// Continuation is ResolveContinuation's decision: deliver fresh (Fresh, no
// Binding) or continue the given Binding's thread. SelfHealedDanglingBinding
// records a released dangling binding so the caller can log it with context.
type Continuation struct {
	Fresh                     bool
	Binding                   *Binding
	SelfHealedDanglingBinding bool
}

// BindingStore is ResolveContinuation's durable-state seam. The daemon adapts
// *store.Store to it; internal/automation never imports internal/store or
// internal/daemon, keeping the logic testable against an in-memory fake.
type BindingStore interface {
	// GetActiveContinuityBinding returns the active binding for
	// (definitionID, continuityKey), or (nil, nil) when there is none.
	GetActiveContinuityBinding(definitionID, continuityKey string) (*Binding, error)
	// ReleaseContinuityBinding releases the active binding for
	// (definitionID, continuityKey) with reason; no-op when none is active.
	ReleaseContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error
	// TicketExists reports whether ticketID still exists.
	TicketExists(ticketID string) (bool, error)
}

// ResolveContinuation decides whether a claimed automation occurrence
// continues an existing thread or starts fresh, from binding status alone.
// ownTicketID is the run's own claim-time reserved ticket ID (known before the
// ticket exists). A binding pointing at ownTicketID is a thread being born,
// NOT dangling — releasing it would break continuity at birth. A binding whose
// ticket exists is continued; one whose ticket is genuinely gone (swept) is
// self-healed: released with reason ticket_swept, then Fresh.
func ResolveContinuation(s BindingStore, definitionID, continuityKey, ownTicketID string, now time.Time) (Continuation, error) {
	binding, err := s.GetActiveContinuityBinding(definitionID, continuityKey)
	if err != nil {
		return Continuation{}, err
	}
	if binding == nil {
		return Continuation{Fresh: true}, nil
	}
	if binding.TicketID == ownTicketID {
		return Continuation{Fresh: true}, nil
	}
	exists, err := s.TicketExists(binding.TicketID)
	if err != nil {
		return Continuation{}, err
	}
	if exists {
		return Continuation{Binding: binding}, nil
	}
	if err := s.ReleaseContinuityBinding(definitionID, continuityKey, ticketSweptReleaseReason, now); err != nil {
		return Continuation{}, err
	}
	return Continuation{Fresh: true, SelfHealedDanglingBinding: true}, nil
}
