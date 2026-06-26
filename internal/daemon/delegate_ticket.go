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
// chief is the author (so the created event reads as "assigned to you" for the
// agent and is self-authored for the chief). The ticket starts in Working since the
// agent begins immediately. The slug is derived from the label, with a numeric
// suffix on collision. Returns the created ticket id.
func (d *Daemon) createDelegatedTicket(chiefSessionID string, session *protocol.Session, brief, label, agent string) (string, error) {
	base := ticketSlug(label)
	now := time.Now()
	for attempt := 0; attempt < 50; attempt++ {
		id := base
		if attempt > 0 {
			id = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		_, err := d.store.CreateTicket(store.Ticket{
			ID:          id,
			Title:       label,
			Description: brief,
			Status:      store.TicketStatusWorking,
			Assignee:    session.ID,
			Cwd:         session.Directory,
			LastAgentID: agent,
		}, chiefSessionID, now)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, store.ErrTicketIDTaken) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate a unique ticket id from %q", base)
}
