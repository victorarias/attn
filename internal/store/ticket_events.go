package store

import (
	"database/sql"
	"time"
)

// The ticket event log is the notification substrate for the work tracker (slice
// 2 of docs/plans/2026-06-26-work-tracker.md). It re-homes the dispatch gateway's
// settled mechanics: an append-only log with idempotent (deduped) events, a global
// monotonic seq that doubles as the cursor space, and per-observer cursors that
// express "unread". The decoupled notification handlers live in internal/ticketnotify;
// this file is only the durable substrate.
//
// Events are a SUPERSET of the display activity thread (slice 1): activity holds
// the two human-facing kinds (status_change, comment); the event log carries all
// six domain events the chief observes. Mutators append events transactionally —
// they emit, but know nothing about who listens.

// TicketEventKind is a domain event on a ticket.
type TicketEventKind string

const (
	// TicketEventCreated fires when a ticket is created.
	TicketEventCreated TicketEventKind = "created"
	// TicketEventStatusChanged fires when a ticket moves column (from -> to).
	TicketEventStatusChanged TicketEventKind = "status_changed"
	// TicketEventCommented fires on a freeform comment.
	TicketEventCommented TicketEventKind = "commented"
	// TicketEventAssigned fires when the assignee changes (Detail = new assignee).
	TicketEventAssigned TicketEventKind = "assigned"
	// TicketEventDescriptionEdited fires when the brief is edited.
	TicketEventDescriptionEdited TicketEventKind = "description_edited"
	// TicketEventAttachmentAdded fires when a file is attached (Detail = filename).
	TicketEventAttachmentAdded TicketEventKind = "attachment_added"
)

// TicketEvent is one entry in the append-only event log. Seq is the global
// monotonic id (the cursor space). The payload columns are kind-specific:
// FromStatus/ToStatus for status changes, Comment for notes, Detail for the
// kind's salient value (new assignee, filename).
type TicketEvent struct {
	Seq        int64
	TicketID   string
	Kind       TicketEventKind
	Author     string
	FromStatus TicketStatus
	ToStatus   TicketStatus
	Comment    string
	Detail     string
	CreatedAt  time.Time
}

// signature is the dedup key: two events with the same signature on the same
// ticket back-to-back are the same logical event (a retry), so only the first is
// appended.
func (e TicketEvent) signature() string {
	return string(e.Kind) + "\x00" + string(e.FromStatus) + "\x00" + string(e.ToStatus) +
		"\x00" + e.Comment + "\x00" + e.Detail + "\x00" + e.Author
}

// AppendTicketEvent appends an event to the log, deduped against the ticket's
// most recent event. It returns the event's seq and whether a new row was written
// (false when deduped). Most callers append within a mutator's transaction via
// appendTicketEventTx; this is the standalone entry point.
func (s *Store) AppendTicketEvent(e TicketEvent, now time.Time) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	seq, appended, err := appendTicketEventTx(tx, e, now)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return seq, appended, nil
}

// appendTicketEventTx is the transactional core, shared by ticket mutators so the
// event lands atomically with the mutation that produced it.
func appendTicketEventTx(tx *sql.Tx, e TicketEvent, now time.Time) (int64, bool, error) {
	var (
		lastSeq                                    int64
		lk, lfrom, lto, lcomment, ldetail, lauthor string
	)
	err := tx.QueryRow(`
		SELECT seq, kind, from_status, to_status, comment, detail, author
		FROM ticket_events WHERE ticket_id = ? ORDER BY seq DESC LIMIT 1
	`, e.TicketID).Scan(&lastSeq, &lk, &lfrom, &lto, &lcomment, &ldetail, &lauthor)
	switch err {
	case nil:
		prev := TicketEvent{
			Kind:       TicketEventKind(lk),
			FromStatus: TicketStatus(lfrom),
			ToStatus:   TicketStatus(lto),
			Comment:    lcomment,
			Detail:     ldetail,
			Author:     lauthor,
		}
		if prev.signature() == e.signature() {
			return lastSeq, false, nil // idempotent: identical to the previous event
		}
	case sql.ErrNoRows:
		// first event for this ticket
	default:
		return 0, false, err
	}

	res, err := tx.Exec(`
		INSERT INTO ticket_events (ticket_id, kind, author, from_status, to_status, comment, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, e.TicketID, string(e.Kind), e.Author, string(e.FromStatus), string(e.ToStatus), e.Comment, e.Detail, formatTicketTime(now))
	if err != nil {
		return 0, false, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

// TicketEventsSince returns every event with seq greater than the given cursor,
// in seq (chronological) order. A cursor of 0 returns the whole log.
func (s *Store) TicketEventsSince(cursor int64) ([]TicketEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT seq, ticket_id, kind, author, from_status, to_status, comment, detail, created_at
		FROM ticket_events WHERE seq > ? ORDER BY seq ASC
	`, cursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []TicketEvent
	for rows.Next() {
		var (
			e         TicketEvent
			kind      string
			from, to  string
			createdAt string
		)
		if err := rows.Scan(&e.Seq, &e.TicketID, &kind, &e.Author, &from, &to, &e.Comment, &e.Detail, &createdAt); err != nil {
			return nil, err
		}
		e.Kind = TicketEventKind(kind)
		e.FromStatus = TicketStatus(from)
		e.ToStatus = TicketStatus(to)
		e.CreatedAt = parseTicketTime(createdAt)
		events = append(events, e)
	}
	return events, rows.Err()
}

// LatestTicketEventSeq returns the highest event seq, or 0 when the log is empty.
func (s *Store) LatestTicketEventSeq() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var seq sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM ticket_events`).Scan(&seq); err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

// GetObserverCursor returns an observer's cursor — the seq through which it has
// consumed. A never-seen observer starts at 0 (everything is unread).
func (s *Store) GetObserverCursor(observerID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var cursor int64
	err := s.db.QueryRow(`SELECT cursor FROM ticket_event_cursors WHERE observer_id = ?`, observerID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return cursor, nil
}

// SetObserverCursor advances (upserts) an observer's cursor.
func (s *Store) SetObserverCursor(observerID string, cursor int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO ticket_event_cursors (observer_id, cursor, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(observer_id) DO UPDATE SET cursor = excluded.cursor, updated_at = excluded.updated_at
	`, observerID, cursor, formatTicketTime(now))
	return err
}
