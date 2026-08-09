package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
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

// TicketMutationOutcome reports unread activity the mutation consumed. CatchUp is
// owed to the caller either way; Blocked says whether reading it also cost the
// caller its write — true means the transaction advanced this ticket's applicable
// cursors and deliberately did not execute the mutation callback.
type TicketMutationOutcome struct {
	CatchUp []TicketEvent
	Blocked bool
}

// blocksTicketMutation reports whether an unread event must be read before the
// caller may write. Only another participant's word does. attn's own bookkeeping
// (the crash stamp, the crashed→working flip, a reconciliation verdict) records
// what happened TO a session while it was dead, so refusing on it made a revived
// agent's first report fail every time — on activity describing its own death.
func blocksTicketMutation(event TicketEvent) bool {
	return strings.TrimSpace(event.Author) != TicketAuthorAttn
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

	var outcome TicketMutationOutcome
	if options.ExpectedEventSeq != nil {
		actual, err := latestTicketEventSeqTx(tx, ticketID)
		if err != nil {
			return TicketMutationOutcome{}, err
		}
		if actual != *options.ExpectedEventSeq {
			return TicketMutationOutcome{}, fmt.Errorf("%w: expected event %d, latest is %d", ErrStaleTicketEventSeq, *options.ExpectedEventSeq, actual)
		}
	} else if len(options.Observers) > 0 {
		consumed, err := consumeTargetTicketEventsTx(tx, ticketID, options.Observers, now)
		if err != nil {
			return TicketMutationOutcome{}, err
		}
		if len(consumed) > 0 {
			if key := strings.TrimSpace(options.AttentionKey); key != "" {
				if err := setTicketDeliveryAttentionTx(tx, key, now); err != nil {
					return TicketMutationOutcome{}, err
				}
			}
			if slices.ContainsFunc(consumed, blocksTicketMutation) {
				if err := tx.Commit(); err != nil {
					return TicketMutationOutcome{}, err
				}
				return TicketMutationOutcome{CatchUp: consumed, Blocked: true}, nil
			}
			outcome.CatchUp = consumed
		}
	}

	if err := mutate(tx); err != nil {
		return TicketMutationOutcome{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketMutationOutcome{}, err
	}
	return outcome, nil
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
			AND EXISTS (
				SELECT 1 FROM ticket_participants p
				WHERE p.ticket_id = e.ticket_id AND p.identity = ?
			)
		ORDER BY e.seq ASC
	`, cursorIdentity, ticketID, authorIdentity, cursorIdentity)
	if err != nil {
		return nil, err
	}
	return scanTicketEventRows(rows)
}
