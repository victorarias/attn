package daemon

import (
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// Ticket notification identities, and the mapping between them and live sessions.
//
// A session always observes through its own session identity. The session
// currently filling a durable product role ALSO observes through that role's
// identity, whose per-(identity, ticket) cursors survive the role moving to a
// different session — that is the whole point of a role identity.
//
// Three questions get asked of that mapping, in three different places, and they
// must stay mutually consistent:
//
//	forward    which identities does this session observe through   ticketObserversForSession
//	inverse    which live session does this identity deliver to     ticketSessionForIdentity
//	attention  which identity owns this session's interruption clock ticketAttentionKey
//
// They are defined together here so that adding a second durable role is one edit
// rather than three scattered role checks that can silently disagree — a
// disagreement whose symptom is an identity that is never delivered to, with no
// error anywhere.

// ticketRoleIdentitiesForSession returns the durable role identities the session
// currently fills, in the order they take precedence. Empty for an ordinary
// session.
func (d *Daemon) ticketRoleIdentitiesForSession(sessionID string) []string {
	if d.isChiefOfStaffSession(sessionID) {
		return []string{store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)}
	}
	return nil
}

// ticketSessionForIdentity is the inverse: the live session an identity's
// notifications should be delivered to. A session identity is its own target; a
// durable role identity resolves to whichever session currently fills it, which may
// be none (empty).
func (d *Daemon) ticketSessionForIdentity(identity string) string {
	if identity == store.TicketRoleIdentity(store.TicketRoleChiefOfStaff) {
		return d.chiefOfStaffSessionID()
	}
	return identity
}

// ticketObserversForSession builds the effective notification identities for a
// session. AuthorID stays the session on every observer, so self-authored events
// are excluded no matter which identity's cursor is being read; DeliveryID is the
// session, because that is what can actually be nudged.
func (d *Daemon) ticketObserversForSession(sessionID string) []ticketnotify.Observer {
	observers := []ticketnotify.Observer{{ID: sessionID, AuthorID: sessionID, DeliveryID: sessionID}}
	for _, roleIdentity := range d.ticketRoleIdentitiesForSession(sessionID) {
		observers = append(observers, ticketnotify.Observer{
			ID:         roleIdentity,
			AuthorID:   sessionID,
			DeliveryID: sessionID,
		})
	}
	return observers
}

// ticketAttentionKey names the identity whose interruption clock governs this
// session's buffered delivery. A role-filling session uses the role's key, so the
// budget follows the role across a transfer rather than resetting with each new
// session. With more than one role the highest-precedence one wins, matching the
// order ticketRoleIdentitiesForSession returns.
func (d *Daemon) ticketAttentionKey(sessionID string) string {
	if roles := d.ticketRoleIdentitiesForSession(sessionID); len(roles) > 0 {
		return roles[0]
	}
	return sessionID
}
