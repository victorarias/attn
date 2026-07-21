package automation

import "time"

// ticketSweptReleaseReason mirrors store.AutomationBindingReleasedTicketSwept.
// The automation package cannot import internal/store (store is the
// dependency-inverted caller here, adapting *store.Store to BindingStore), so
// the reason string is duplicated rather than shared — see BindingStore.
const ticketSweptReleaseReason = "ticket_swept"

// Binding is a continuity thread's stable identity, as durably recorded by
// store.AutomationContinuityBinding. It carries only what a continuation
// decision needs; ResolveContinuation deliberately learns nothing else about
// the definition, ticket, or session.
type Binding struct {
	TicketID, SessionID, WorkspaceID, PaneID string
}

// Continuation is ResolveContinuation's decision: either deliver fresh
// (Fresh, no Binding) or continue the given Binding's thread. If a dangling
// active binding was found (its ticket gone) and released to make Fresh
// delivery safe, SelfHealedDanglingBinding records that so the caller can log
// it — the caller has more context (definition, continuity key) to make a
// useful warning than this package does.
type Continuation struct {
	Fresh                     bool
	Binding                   *Binding
	SelfHealedDanglingBinding bool
}

// BindingStore is the durable-state seam ResolveContinuation depends on
// instead of touching tickets, git, or PTYs directly. The daemon adapts
// *store.Store to it; internal/automation never imports internal/store or
// internal/daemon, keeping this package's continuation logic testable against
// an in-memory fake.
type BindingStore interface {
	// GetActiveContinuityBinding returns the active binding for
	// (definitionID, continuityKey), or (nil, nil) when there is none.
	GetActiveContinuityBinding(definitionID, continuityKey string) (*Binding, error)
	// ReleaseContinuityBinding releases the active binding for
	// (definitionID, continuityKey) with reason, or is a no-op if there is no
	// active binding.
	ReleaseContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error
	// TicketExists reports whether ticketID still exists.
	TicketExists(ticketID string) (bool, error)
}

// ResolveContinuation decides whether a claimed automation occurrence should
// continue an existing thread or start fresh, from binding status alone.
// ownTicketID is the calling run's own reserved ticket ID (WorkRequest.IDs.
// TicketID) — the caller always knows this before its ticket exists, since
// ticket IDs are stable-reserved at claim time, before ticket creation.
//
//   - no active binding: Fresh delivery — either the first occurrence under
//     this continuity key, or a previously active binding was already
//     released (contract rotation, explicit ticket sweep, definition delete).
//   - an active binding whose ticket is ownTicketID: Fresh delivery, and the
//     binding is NOT released. The claim that reserved this run's IDs already
//     created this binding pointing at its own not-yet-created ticket — the
//     thread is being born right now, not dangling. Treating "ticket doesn't
//     exist yet" as "ticket is gone" here would self-heal (release) a binding
//     the instant it is born, breaking continuity for every next occurrence.
//   - an active binding whose ticket exists (and is not ownTicketID): continue
//     that thread.
//   - an active binding whose ticket is gone (e.g. store.SweepExpiredTickets
//     removed it without going through the ordinary release paths) and is not
//     ownTicketID: this is a genuinely dangling binding. ResolveContinuation
//     self-heals it — releases the binding with reason ticket_swept, then
//     reports Fresh delivery — rather than refusing, since there is nothing
//     left to continue.
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
