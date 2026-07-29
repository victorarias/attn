package daemon

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// ticketSlugStrip collapses every run of non-slug characters to a single dash.
var ticketSlugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// ticketSlug derives a human-friendly slug id from a label (e.g. "Migrate store
// to X" -> "migrate-store-to-x"). Delegation creates the ticket before the agent
// runs, so attn names it from the label rather than the agent. The result is
// always a non-empty, bounded slug; collisions are resolved by the caller.
func ticketSlug(label string) string {
	s := ticketSlugStrip.ReplaceAllString(strings.ToLower(strings.TrimSpace(label)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "ticket"
	}
	return s
}

// createDelegatedTicket creates and binds the ticket for a delegated session —
// every delegation, not only the chief's. The brief is the description (the
// delegation prompt); the session id is the assignee (its observer identity, so
// assignee == session is the binding); the delegating session is the event author.
// The ticket starts in Working since the agent begins immediately. The slug is
// derived from the label, with a numeric suffix on collision. Returns the created
// ticket id.
//
// Three identities end up on the ticket, each exactly once (see
// store.TicketParticipants): the assignee by binding, the creator by an explicit
// subscription, and the chief of staff — always, so its organizational overview
// covers every delegation in the system whoever started it.
//
// The chief reaches the ticket through its durable ROLE identity, either as owner
// or as subscriber, so its view survives the session filling the role changing:
//
//   - chief-initiated delegation: the chief role OWNS the ticket. Ownership is
//     also what marks the delegated session `delegated_from_chief` in the sidebar
//     (see delegatedFromChiefSessionIDs), so it stays reserved for work the chief
//     actually started.
//   - ordinary delegation: the chief role SUBSCRIBES. Same routing guarantee, no
//     claim of ownership over work another session started.
//
// When the creator IS the chief, it holds both its session subscription and the
// role identity; ticketnotify.ConsumeAll merges the two queues by event seq, so
// the activity is still delivered once. No extra guard is needed for that case.
func (d *Daemon) createDelegatedTicket(creatorSessionID string, ownedByChiefRole bool, session *protocol.Session, brief, label, agent string) (string, error) {
	ownerRole := ""
	chiefRoleIdentity := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	subscribers := []string{creatorSessionID}
	if ownedByChiefRole {
		ownerRole = store.TicketRoleChiefOfStaff
	} else {
		subscribers = append(subscribers, chiefRoleIdentity)
	}
	created, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title:       label,
		Description: brief,
		Status:      store.TicketStatusWorking,
		Assignee:    session.ID,
		Cwd:         session.Directory,
		LastAgentID: agent,
	}, ticketSlug(label), creatorSessionID, ownerRole, subscribers, time.Now())
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// ticketSlugSequentialAttempts bounds the readable base-2, base-3, ... walk before
// the allocator switches to random suffixes. Now that EVERY delegation creates a
// ticket, one popular base (a repo's directory basename, the common default label)
// accumulates ids fast, and a full board must never be able to fail a delegation
// that has already spawned a session. The sequential walk keeps ids readable while
// the board is small; past that the random tail keeps allocation effectively
// unbounded at the cost of a less pretty id.
const ticketSlugSequentialAttempts = 50

// createTicketWithUniqueSlug inserts template under base, falling back to base-2,
// base-3, ... on slug collision, then to base-<random> once the sequential range is
// exhausted. The template's ID field is ignored — the slug is allocated here. It
// returns the created ticket, a non-collision CreateTicket error verbatim, or a
// "could not allocate" error only if even the random suffixes collide. Both
// createDelegatedTicket (bound, working) and the standalone ticket_create handler
// (unbound, todo) share this so the auto-suffix behavior is identical.
func (d *Daemon) createTicketWithUniqueSlug(template store.Ticket, base, author, ownerRole string, subscribers []string, now time.Time) (*store.Ticket, error) {
	for attempt := 0; attempt < ticketSlugSequentialAttempts+5; attempt++ {
		switch {
		case attempt == 0:
			template.ID = base
		case attempt < ticketSlugSequentialAttempts:
			template.ID = fmt.Sprintf("%s-%d", base, attempt+1)
		default:
			template.ID = fmt.Sprintf("%s-%s", base, strings.ToLower(uuid.NewString()[:6]))
		}
		created, err := d.store.CreateTicketWithSubscribers(template, author, ownerRole, subscribers, now)
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, store.ErrTicketIDTaken) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a unique ticket id from %q", base)
}
