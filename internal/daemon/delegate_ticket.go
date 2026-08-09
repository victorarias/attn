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

// ticketSlug derives a slug id from a label ("Migrate store to X" ->
// "migrate-store-to-x"). Always non-empty and bounded; collisions are the
// caller's to resolve.
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

// createDelegatedTicket creates and binds the ticket for every delegated
// session, starting in Working. The chief of staff is always a participant via
// its durable ROLE identity — owner when it initiated (which is also what marks
// the session `delegated_from_chief`), subscriber otherwise — so its view
// survives the session filling the role changing.
func (d *Daemon) createDelegatedTicket(creatorSessionID string, ownedByChiefRole bool, session *protocol.Session, brief, label, agent string) (string, error) {
	if d.delegationTicketCreateHook != nil {
		if err := d.delegationTicketCreateHook(); err != nil {
			return "", err
		}
	}
	ownerRole, subscribers := delegationTicketAttachment(creatorSessionID, ownedByChiefRole)
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

// adoptDelegatedTicket binds an existing ticket instead of minting a duplicate.
// The store advances the new assignee's cursor past the adoption events (the
// description already went out in the spawn prompt); the previous assignee stays
// subscribed.
func (d *Daemon) adoptDelegatedTicket(creatorSessionID string, ownedByChiefRole bool, session *protocol.Session, ticketID, agent string, confirm bool) (string, error) {
	ownerRole, subscribers := delegationTicketAttachment(creatorSessionID, ownedByChiefRole)
	adopted, err := d.store.AdoptTicketForDelegation(
		ticketID, session.ID, session.Directory, agent, creatorSessionID,
		ownerRole, subscribers, confirm, time.Now(),
	)
	if err != nil {
		return "", err
	}
	return adopted.ID, nil
}

// delegationTicketAttachment decides who the delegating side attaches as. A chief
// delegation attaches the ROLE and nothing else: the role owner row is the
// attachment, and the events the acting session writes carry that role, so the
// attachment moves with the role instead of stranding a personal subscription on
// the session that happened to hold it. Any other delegator attaches personally
// and pulls the chief role in as a subscriber, because the chief follows every
// delegation whether or not it started it.
func delegationTicketAttachment(creatorSessionID string, ownedByChiefRole bool) (ownerRole string, subscribers []string) {
	if ownedByChiefRole {
		return store.TicketRoleChiefOfStaff, nil
	}
	return "", []string{creatorSessionID, store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)}
}

// ticketSlugSequentialAttempts bounds the readable base-2, base-3, ... walk
// before random suffixes: a full board must never fail a delegation that has
// already spawned a session, so past this the random tail keeps allocation
// effectively unbounded.
const ticketSlugSequentialAttempts = 50

// ticketSlugRandomSuffixLen is the width of the random tail. Fixed width is
// what the fallback test asserts on — the tail is hex, so sometimes all digits.
const ticketSlugRandomSuffixLen = 6

// createTicketWithUniqueSlug inserts template under base, falling back to
// base-2, base-3, ... on collision, then base-<random>. The template's ID is
// ignored — the slug is allocated here. Shared by createDelegatedTicket and the
// standalone ticket_create handler so auto-suffix behavior is identical.
func (d *Daemon) createTicketWithUniqueSlug(template store.Ticket, base, author, ownerRole string, subscribers []string, now time.Time) (*store.Ticket, error) {
	for attempt := 0; attempt < ticketSlugSequentialAttempts+5; attempt++ {
		switch {
		case attempt == 0:
			template.ID = base
		case attempt < ticketSlugSequentialAttempts:
			template.ID = fmt.Sprintf("%s-%d", base, attempt+1)
		default:
			template.ID = fmt.Sprintf("%s-%s", base, strings.ToLower(uuid.NewString()[:ticketSlugRandomSuffixLen]))
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
