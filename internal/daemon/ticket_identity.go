package daemon

import (
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// Ticket notification identities, and the mapping between them and live sessions.
//
// An ordinary session observes through its own identity. A session filling a
// durable profile role ALSO observes through that role. A crew day instead acts
// through the member identity: its session id is disposable, while the member's
// per-ticket cursors survive every turnover. The concrete session remains only
// the delivery target and, where applicable, the ticket assignee.
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

// ticketDurableIdentitiesForSession returns the durable identities the session
// currently fills, in the order they take precedence. A member comes first: its
// subscriptions, cursor and attention clock belong to the member across days.
func (d *Daemon) ticketDurableIdentitiesForSession(sessionID string) []string {
	var identities []string
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		identities = append(identities, store.TicketMemberIdentity(member))
	}
	if d.isChiefOfStaffSession(sessionID) {
		identities = append(identities, store.TicketRoleIdentity(store.TicketRoleChiefOfStaff))
	}
	return identities
}

// ticketSessionForIdentity is the inverse: the live session an identity's
// notifications should be delivered to. A session identity is its own target; a
// durable role identity resolves to whichever session currently fills it, which may
// be none (empty).
func (d *Daemon) ticketSessionForIdentity(identity string) string {
	if identity == store.TicketRoleIdentity(store.TicketRoleChiefOfStaff) {
		return d.chiefOfStaffSessionID()
	}
	if memberID, ok := store.ParseTicketMemberIdentity(identity); ok {
		member, _, err := d.crewMember(memberID)
		if err != nil || !d.crewBindingLive(member) {
			return ""
		}
		return member.BindingSession
	}
	return identity
}

// ticketActorIdentity is how a session-bound ticket action is attributed. A
// member acts as the member, not as today's disposable session; ordinary and
// chief sessions keep their existing concrete-session attribution.
func (d *Daemon) ticketActorIdentity(sessionID string) string {
	for _, identity := range d.ticketDurableIdentitiesForSession(sessionID) {
		if _, ok := store.ParseTicketMemberIdentity(identity); ok {
			return identity
		}
	}
	return sessionID
}

// ticketObserversForSession builds the effective notification identities for a
// session. A member reads only through durable identities; other role fillers
// retain their concrete-session observer too. AuthorID is the acting identity so
// a member never receives its own event through another observer. DeliveryID is
// always the concrete session, because that is what can actually be nudged.
func (d *Daemon) ticketObserversForSession(sessionID string) []ticketnotify.Observer {
	authorID := d.ticketActorIdentity(sessionID)
	durable := d.ticketDurableIdentitiesForSession(sessionID)
	observers := make([]ticketnotify.Observer, 0, len(durable)+1)
	if _, member := store.ParseTicketMemberIdentity(authorID); !member {
		observers = append(observers, ticketnotify.Observer{ID: sessionID, AuthorID: authorID, DeliveryID: sessionID})
	}
	for _, roleIdentity := range durable {
		observers = append(observers, ticketnotify.Observer{
			ID:         roleIdentity,
			AuthorID:   authorID,
			DeliveryID: sessionID,
		})
	}
	return observers
}

// ticketAttentionKey names the identity whose interruption clock governs this
// session's buffered delivery. A role-filling session uses the role's key, so the
// budget follows the role across a transfer rather than resetting with each new
// session. With more than one role the highest-precedence one wins, matching the
// order ticketDurableIdentitiesForSession returns.
func (d *Daemon) ticketAttentionKey(sessionID string) string {
	if roles := d.ticketDurableIdentitiesForSession(sessionID); len(roles) > 0 {
		return roles[0]
	}
	return sessionID
}
