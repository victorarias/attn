package daemon

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

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

// createDelegatedTicket creates and binds the ticket for a chief-delegated session.
// The brief is the description (the delegation prompt); the session id is the
// assignee (its observer identity, so assignee == session is the binding); the
// concrete chief session is the event author for audit, while durable chief-role
// ownership supplies participation and the unread cursor. The ticket starts in
// Working since the agent begins immediately. The slug is derived from the label,
// with a numeric suffix on collision. Returns the created ticket id.
func (d *Daemon) createDelegatedTicket(chiefSessionID string, session *protocol.Session, brief, label, agent string) (string, error) {
	created, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title:       label,
		Description: brief,
		Status:      store.TicketStatusWorking,
		Assignee:    session.ID,
		Cwd:         session.Directory,
		LastAgentID: agent,
	}, ticketSlug(label), chiefSessionID, store.TicketRoleChiefOfStaff, time.Now())
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// createTicketWithUniqueSlug inserts template under base, falling back to base-2,
// base-3, ... on slug collision (up to 50 attempts). The template's ID field is
// ignored — the slug is allocated here. It returns the created ticket, a non-collision
// CreateTicket error verbatim, or a "could not allocate" exhaustion error. Both
// createDelegatedTicket (bound, working) and the standalone ticket_create handler
// (unbound, todo) share this so the auto-suffix behavior is identical.
func (d *Daemon) createTicketWithUniqueSlug(template store.Ticket, base, author, ownerRole string, now time.Time) (*store.Ticket, error) {
	for attempt := 0; attempt < 50; attempt++ {
		template.ID = base
		if attempt > 0 {
			template.ID = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		var created *store.Ticket
		var err error
		if ownerRole == "" {
			created, err = d.store.CreateTicket(template, author, now)
		} else {
			created, err = d.store.CreateRoleOwnedTicket(template, author, ownerRole, now)
		}
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, store.ErrTicketIDTaken) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a unique ticket id from %q", base)
}
