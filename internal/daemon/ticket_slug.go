package daemon

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/store"
)

// Ticket id allocation. Delegation no longer mints tickets — it binds a seed —
// but the daemon's own ticket_create command still does, and a slug has to be
// unique on a board that keeps every ticket it ever had.

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

// ticketSlugSequentialAttempts bounds the readable base-2, base-3, ... walk
// before random suffixes: past this the random tail keeps allocation effectively
// unbounded, so a crowded base never fails an allocation outright.
const ticketSlugSequentialAttempts = 50

// ticketSlugRandomSuffixLen is the width of the random tail. Fixed width is
// what the fallback test asserts on — the tail is hex, so sometimes all digits.
const ticketSlugRandomSuffixLen = 6

// createTicketWithUniqueSlug inserts template under base, falling back to
// base-2, base-3, ... on collision, then base-<random>. The template's ID is
// ignored — the slug is allocated here.
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
