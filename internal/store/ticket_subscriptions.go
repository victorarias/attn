package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Ticket subscriptions are the third participation source (beside assignment and
// non-comment authorship): an explicit, opt-in standing interest. A subscriber is
// folded into the same participant set the notifier and the unread query derive
// from (see UnreadTicketEvents / TicketParticipants), so it is nudged about a
// ticket's activity and its inbox delivers that ticket — without ever being its
// assignee or author. Subscribing carries NO cursor write: the cursor stays where
// it was (0 if the ticket was never read), so the first inbox after subscribing
// delivers the ticket's history, the same "joining mid-stream sees the backlog"
// rule that take and a fresh assignment follow.

// AddTicketSubscription opts an identity into a ticket's notifications. It is
// idempotent — re-subscribing is a no-op, not an error — but the ticket must
// exist, so a subscription to a phantom id is a clear error the caller can act on
// rather than a silently dropped row. The cursor is deliberately left untouched.
func (s *Store) AddTicketSubscription(identity, ticketID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM tickets WHERE id = ?`, ticketID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", ErrTicketNotFound, ticketID)
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO ticket_subscriptions (identity, ticket_id, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(identity, ticket_id) DO NOTHING
	`, identity, ticketID, formatTicketTime(now))
	return err
}

// RemoveTicketSubscription opts an identity back out. It is a pure idempotent
// removal: removing a subscription that isn't there (never subscribed, already
// unsubscribed, or the ticket was swept) succeeds, because the caller's goal —
// "I am not subscribed" — is already met. So, unlike AddTicketSubscription, it
// does NOT require the ticket to exist.
func (s *Store) RemoveTicketSubscription(identity, ticketID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(
		`DELETE FROM ticket_subscriptions WHERE identity = ? AND ticket_id = ?`,
		identity, ticketID,
	)
	return err
}

// IsTicketSubscribed reports whether an identity is subscribed to a ticket. It
// backs assertions and lets a handler confirm a subscription state without
// recomputing the whole participant set.
func (s *Store) IsTicketSubscribed(identity, ticketID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM ticket_subscriptions WHERE identity = ? AND ticket_id = ?`,
		identity, ticketID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
