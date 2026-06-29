package store

import (
	"database/sql"
	"time"
)

// The ticket event log is the notification substrate for the work tracker (slice
// 2 of docs/plans/2026-06-26-work-tracker.md). It re-homes the dispatch gateway's
// settled mechanics: an append-only log with idempotent (deduped) events, a global
// monotonic seq that doubles as the cursor space, and per-(identity, ticket)
// cursors that express "unread". The decoupled notification handlers live in
// internal/ticketnotify; this file is only the durable substrate.
//
// Cursors are keyed by (identity, ticket), not one global cursor per identity:
// every identity — you, the chief, each agent (the chief is just another agent) —
// has its own bookmark PER ticket. So a ticket newly assigned to an agent is
// delivered from the start (it carries the brief and pre-assignment context),
// and an agent's progress on one ticket never advances its bookmark on another.
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

// scanTicketEventRows scans a ticket_events result set whose columns are, in
// order: seq, ticket_id, kind, author, from_status, to_status, comment, detail,
// created_at. It closes rows.
func scanTicketEventRows(rows *sql.Rows) ([]TicketEvent, error) {
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
	return scanTicketEventRows(rows)
}

// UnreadTicketEvents returns, for an identity, every event it has not yet
// consumed across the tickets it participates in — those currently assigned to it,
// any it has authored a NON-COMMENT event on, plus any it has explicitly subscribed
// to — excluding events it authored itself. Each event is compared against the identity's OWN per-(identity, ticket)
// cursor, so a ticket the identity has never looked at is delivered from the start
// (the brief and all pre-involvement context). Results are ordered by ticket then seq.
//
// Comment authorship is deliberately NOT a participation source: a one-shot
// comment on an arbitrary ticket informs that ticket's participants without
// enrolling the commenter in its future notifications (an agent dropping a note
// shouldn't then be nudged about every later change). Standing interest is opt-in
// via assignment or an explicit subscription instead.
//
// This is the consume query: one statement folds the participant set, the
// per-ticket cursors, and the self-author exclusion together, so a quiet or
// closed ticket the identity has nothing new on costs only an indexed lookup.
func (s *Store) UnreadTicketEvents(identity string) ([]TicketEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || identity == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT e.seq, e.ticket_id, e.kind, e.author, e.from_status, e.to_status, e.comment, e.detail, e.created_at
		FROM ticket_events e
		LEFT JOIN ticket_event_cursors c
			ON c.identity = ? AND c.ticket_id = e.ticket_id
		WHERE e.author != ?
			AND e.seq > COALESCE(c.cursor, 0)
			AND e.ticket_id IN (
				SELECT id FROM tickets WHERE assignee = ?
				UNION
				SELECT DISTINCT ticket_id FROM ticket_events WHERE author = ? AND kind != 'commented'
				UNION
				SELECT ticket_id FROM ticket_subscriptions WHERE identity = ?
			)
		ORDER BY e.ticket_id, e.seq ASC
	`, identity, identity, identity, identity, identity)
	if err != nil {
		return nil, err
	}
	return scanTicketEventRows(rows)
}

// TicketParticipants returns the identities involved with a single ticket — its
// current assignee, everyone who has authored a NON-COMMENT event on it, and
// everyone subscribed to it. This is the inverse of UnreadTicketEvents (identities-
// for-a-ticket, not tickets-for-an-identity): when an event lands, the notifier
// reaches exactly these identities, each of which sees only what it did not author.
// Empty authors/assignees/subscribers are excluded, and comment authorship confers
// no participation (see UnreadTicketEvents) — so a one-shot commenter is not reached
// by later events, but an explicit subscriber is.
func (s *Store) TicketParticipants(ticketID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || ticketID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT assignee FROM tickets WHERE id = ? AND assignee != ''
		UNION
		SELECT DISTINCT author FROM ticket_events WHERE ticket_id = ? AND author != '' AND kind != 'commented'
		UNION
		SELECT identity FROM ticket_subscriptions WHERE ticket_id = ? AND identity != ''
		ORDER BY 1 ASC
	`, ticketID, ticketID, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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

// GetTicketCursor returns an identity's cursor on a single ticket — the seq
// through which it has consumed that ticket's events. An identity that has never
// looked at the ticket starts at 0 (everything on it is unread), which is what
// delivers a freshly-assigned ticket's full history.
func (s *Store) GetTicketCursor(identity, ticketID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var cursor int64
	err := s.db.QueryRow(`SELECT cursor FROM ticket_event_cursors WHERE identity = ? AND ticket_id = ?`, identity, ticketID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return cursor, nil
}

// SetTicketCursor advances an identity's cursor on a single ticket. The write is
// monotonic: it only ever moves the cursor FORWARD (MAX of the stored and proposed
// seq). A cursor named "consumed through here" must never rewind, so a stale or
// overlapping consume that writes a lower seq cannot resurrect already-consumed
// events as unread or double-deliver them.
func (s *Store) SetTicketCursor(identity, ticketID string, cursor int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	return setTicketCursorTx(s.db, identity, ticketID, cursor, now)
}

// ticketExecer is the Exec surface shared by *sql.DB and *sql.Tx, so a cursor write
// can run either standalone or inside an enclosing mutation's transaction (e.g. the
// delegation create that marks an agent's brief consumed atomically with the ticket).
type ticketExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// setTicketCursorTx is the monotonic cursor write. It only moves the cursor FORWARD
// (MAX of stored and proposed), so a stale or overlapping write can never rewind a
// cursor and resurrect already-consumed events as unread.
func setTicketCursorTx(ex ticketExecer, identity, ticketID string, cursor int64, now time.Time) error {
	_, err := ex.Exec(`
		INSERT INTO ticket_event_cursors (identity, ticket_id, cursor, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(identity, ticket_id) DO UPDATE SET
			cursor = MAX(cursor, excluded.cursor),
			updated_at = excluded.updated_at
	`, identity, ticketID, cursor, formatTicketTime(now))
	return err
}
