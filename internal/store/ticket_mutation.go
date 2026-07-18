package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrStaleTicketEventSeq means an app action was based on a ticket detail that
// no longer represents the latest event. The caller must refresh and retry.
var ErrStaleTicketEventSeq = errors.New("ticket changed since it was opened")

// TicketMutationObserver is one effective unread view for the updater. Ordinary
// sessions use the same value for both fields; durable roles keep their cursor
// while excluding events authored by the concrete session holding the role.
type TicketMutationObserver struct {
	CursorIdentity string
	AuthorIdentity string
}

// TicketMutationOptions selects exactly one precondition for a content
// mutation: CLI callers supply Observers for consume-or-mutate, while app callers
// supply ExpectedEventSeq for optimistic concurrency.
type TicketMutationOptions struct {
	Observers        []TicketMutationObserver
	AttentionKey     string
	ExpectedEventSeq *int64
}

// TicketMutationOutcome reports a CLI catch-up conflict. A non-empty slice means
// the transaction advanced only this ticket's applicable cursors and deliberately
// did not execute the mutation callback.
type TicketMutationOutcome struct {
	ConflictEvents []TicketEvent
}

func (s *Store) withTicketMutation(
	ticketID string,
	options TicketMutationOptions,
	now time.Time,
	mutate func(*sql.Tx) error,
) (TicketMutationOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return TicketMutationOutcome{}, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TicketMutationOutcome{}, err
	}
	defer tx.Rollback()

	if options.ExpectedEventSeq != nil {
		actual, err := latestTicketEventSeqTx(tx, ticketID)
		if err != nil {
			return TicketMutationOutcome{}, err
		}
		if actual != *options.ExpectedEventSeq {
			return TicketMutationOutcome{}, fmt.Errorf("%w: expected event %d, latest is %d", ErrStaleTicketEventSeq, *options.ExpectedEventSeq, actual)
		}
	} else if len(options.Observers) > 0 {
		conflicts, err := consumeTargetTicketEventsTx(tx, ticketID, options.Observers, now)
		if err != nil {
			return TicketMutationOutcome{}, err
		}
		if len(conflicts) > 0 {
			if key := strings.TrimSpace(options.AttentionKey); key != "" {
				if err := setTicketDeliveryAttentionTx(tx, key, now); err != nil {
					return TicketMutationOutcome{}, err
				}
			}
			if err := tx.Commit(); err != nil {
				return TicketMutationOutcome{}, err
			}
			return TicketMutationOutcome{ConflictEvents: conflicts}, nil
		}
	}

	if err := mutate(tx); err != nil {
		return TicketMutationOutcome{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketMutationOutcome{}, err
	}
	return TicketMutationOutcome{}, nil
}

func latestTicketEventSeqTx(tx *sql.Tx, ticketID string) (int64, error) {
	var seq sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM ticket_events WHERE ticket_id = ?`, ticketID).Scan(&seq); err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

func consumeTargetTicketEventsTx(
	tx *sql.Tx,
	ticketID string,
	observers []TicketMutationObserver,
	now time.Time,
) ([]TicketEvent, error) {
	merged := make(map[int64]TicketEvent)
	for _, observer := range observers {
		cursorIdentity := strings.TrimSpace(observer.CursorIdentity)
		if cursorIdentity == "" {
			continue
		}
		authorIdentity := strings.TrimSpace(observer.AuthorIdentity)
		if authorIdentity == "" {
			authorIdentity = cursorIdentity
		}
		events, err := unreadTargetTicketEventsTx(tx, ticketID, cursorIdentity, authorIdentity)
		if err != nil {
			return nil, err
		}
		var cursor int64
		for _, event := range events {
			merged[event.Seq] = event
			if event.Seq > cursor {
				cursor = event.Seq
			}
		}
		if cursor > 0 {
			if err := setTicketCursorTx(tx, cursorIdentity, ticketID, cursor, now); err != nil {
				return nil, err
			}
		}
	}
	conflicts := make([]TicketEvent, 0, len(merged))
	for _, event := range merged {
		conflicts = append(conflicts, event)
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Seq < conflicts[j].Seq })
	return conflicts, nil
}

func unreadTargetTicketEventsTx(
	tx *sql.Tx,
	ticketID, cursorIdentity, authorIdentity string,
) ([]TicketEvent, error) {
	rows, err := tx.Query(`
		SELECT e.seq, e.ticket_id, e.kind, e.author, e.from_status, e.to_status, e.comment, e.detail, e.created_at
		FROM ticket_events e
		LEFT JOIN ticket_event_cursors c
			ON c.identity = ? AND c.ticket_id = e.ticket_id
		WHERE e.ticket_id = ?
			AND e.author != ?
			AND e.seq > COALESCE(c.cursor, 0)
			AND (
				EXISTS (SELECT 1 FROM tickets t WHERE t.id = e.ticket_id AND t.assignee = ?)
				OR EXISTS (
					SELECT 1 FROM ticket_events e2
					WHERE e2.ticket_id = e.ticket_id AND e2.author = ? AND e2.kind != 'commented'
						AND NOT (
							e2.kind = 'created' AND EXISTS (
								SELECT 1 FROM ticket_role_owners ro WHERE ro.ticket_id = e2.ticket_id
							)
						)
				)
				OR EXISTS (
					SELECT 1 FROM ticket_subscriptions sub
					WHERE sub.ticket_id = e.ticket_id AND sub.identity = ?
				)
				OR EXISTS (
					SELECT 1 FROM ticket_role_owners ro
					WHERE ro.ticket_id = e.ticket_id AND ? = ('role:' || ro.role)
				)
			)
		ORDER BY e.seq ASC
	`, cursorIdentity, ticketID, authorIdentity, cursorIdentity, cursorIdentity, cursorIdentity, cursorIdentity)
	if err != nil {
		return nil, err
	}
	return scanTicketEventRows(rows)
}
