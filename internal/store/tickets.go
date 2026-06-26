package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Tickets are the durable unit of tracked work in attn — independent of any
// session. This file is the store layer for slice 1 of the work tracker (see
// docs/plans/2026-06-26-work-tracker.md): the schema, CRUD, status transitions,
// and the archive / TTL lifecycle. Notification events (slice 2) and the live
// session wiring (slice 3+) layer on top of these primitives; nothing here knows
// about observers or sessions.
//
// Following the workflow_runs convention, the row types are store-local (NOT
// protocol/generated types) — the protocol/wire shape is owned by a later slice.

// TicketStatus is the column a ticket sits in. The board informs, it never gates:
// transitions are permissive (any status may move to any other). The only thing a
// status drives is lifecycle — the terminal statuses age out via the TTL sweep.
type TicketStatus string

const (
	// TicketStatusTodo is the backlog: created, not started.
	TicketStatusTodo TicketStatus = "todo"
	// TicketStatusWorking is actively being worked.
	TicketStatusWorking TicketStatus = "working"
	// TicketStatusBlocked is paused, owing the chief a reply.
	TicketStatusBlocked TicketStatus = "blocked"
	// TicketStatusInReview is done, awaiting a look/approval.
	TicketStatusInReview TicketStatus = "in_review"
	// TicketStatusDone is closed, worked.
	TicketStatusDone TicketStatus = "done"
	// TicketStatusFailed is finished, didn't work.
	TicketStatusFailed TicketStatus = "failed"
	// TicketStatusCrashed is died without reporting — the one transition attn
	// itself writes, since a dead worker can't.
	TicketStatusCrashed TicketStatus = "crashed"
)

// IsValid reports whether st is a known status.
func (st TicketStatus) IsValid() bool {
	switch st {
	case TicketStatusTodo, TicketStatusWorking, TicketStatusBlocked,
		TicketStatusInReview, TicketStatusDone, TicketStatusFailed, TicketStatusCrashed:
		return true
	}
	return false
}

// IsTerminal reports whether st is a settled-and-done status. Terminal tickets
// carry a closed_at timestamp and are the only ones the TTL sweep removes.
func (st TicketStatus) IsTerminal() bool {
	switch st {
	case TicketStatusDone, TicketStatusFailed, TicketStatusCrashed:
		return true
	}
	return false
}

// TicketActivityKind is the type of a history entry. Per the model there are
// exactly two: a status change (the ticket moved column, optionally with a note)
// and a freeform comment. What used to be a dispatch "report" is just a status
// change with a comment.
type TicketActivityKind string

const (
	// TicketActivityStatusChange records a column move (from -> to), optionally
	// with an accompanying comment.
	TicketActivityStatusChange TicketActivityKind = "status_change"
	// TicketActivityComment records a freeform note from either side.
	TicketActivityComment TicketActivityKind = "comment"
)

// Ticket is the durable record. Activity and Attachments are populated by
// GetTicket; list operations leave them nil for cheapness.
type Ticket struct {
	ID          string
	Title       string
	Description string
	Status      TicketStatus
	Assignee    string // agent id, "you", or "" when unassigned
	Cwd         string // last session's working dir (for resume)
	LastAgentID string // last session's agent id (for resume)
	ProjectID   string // future grouping; "" when ungrouped
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClosedAt    *time.Time // set on entering a terminal status; drives the TTL
	ArchivedAt  *time.Time // set when manually cleared from the board

	Activity    []TicketActivity   // populated by GetTicket
	Attachments []TicketAttachment // populated by GetTicket
}

// TicketActivity is one entry in a ticket's history thread.
type TicketActivity struct {
	ID         int64
	TicketID   string
	Kind       TicketActivityKind
	Author     string       // who authored it ("you", a chief, an agent id)
	FromStatus TicketStatus // status_change only
	ToStatus   TicketStatus // status_change only
	Comment    string       // freeform note (may accompany a status change)
	CreatedAt  time.Time
}

// TicketAttachment is a file handed over with the work. Slice 1 stores the
// record; the file-handling ergonomics (copy-into-store) come with slice 4.
type TicketAttachment struct {
	ID        int64
	TicketID  string
	Filename  string
	Path      string
	Note      string
	CreatedAt time.Time
}

// TicketListFilter narrows ListTickets. The zero value lists every non-archived
// ticket, newest first.
type TicketListFilter struct {
	Status          TicketStatus // "" matches any status
	IncludeArchived bool         // false hides archived tickets (the default board)
}

// Ticket lifecycle errors. Callers match these with errors.Is; the error string
// is also surfaced to agents, so it carries the actionable guidance directly.
var (
	// ErrTicketIDTaken means a ticket already exists with that slug.
	ErrTicketIDTaken = errors.New("ticket id already in use")
	// ErrTicketNotFound means no ticket exists with that id.
	ErrTicketNotFound = errors.New("ticket not found")
	// ErrInvalidTicketID means the slug isn't a well-formed handle.
	ErrInvalidTicketID = errors.New("invalid ticket id")
	// ErrInvalidTicketStatus means the status isn't one of the known columns.
	ErrInvalidTicketStatus = errors.New("invalid ticket status")
	// ErrTicketTitleRequired means a ticket was created without a title.
	ErrTicketTitleRequired = errors.New("ticket title required")
	// ErrTicketNotClosed means an open ticket can't be archived — open tickets
	// are durable and never leave the board until they settle.
	ErrTicketNotClosed = errors.New("ticket is not closed")
)

// ticketIDPattern is a human-friendly slug: lowercase alphanumerics and hyphens,
// starting with an alphanumeric. Speakable, typeable, distinctive.
var ticketIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ticketScanner abstracts *sql.Row and *sql.Rows so one scan func serves both.
type ticketScanner interface {
	Scan(dest ...any) error
}

// ValidateTicketID reports whether id is a well-formed slug.
func ValidateTicketID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is empty", ErrInvalidTicketID)
	}
	if !ticketIDPattern.MatchString(id) {
		return fmt.Errorf("%w: %q must be lowercase letters, digits, and hyphens (e.g. store-migration)", ErrInvalidTicketID, id)
	}
	return nil
}

// CreateTicket inserts a new ticket. The id is an agent-chosen memorable slug; on
// collision it fails with ErrTicketIDTaken and actionable guidance. An empty
// status defaults to Todo. author records who created it (for the emitted event).
// The supplied now stamps created_at/updated_at (and closed_at, in the unusual
// case of creating directly into a terminal status).
func (s *Store) CreateTicket(t Ticket, author string, now time.Time) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}

	t.ID = strings.TrimSpace(t.ID)
	if err := ValidateTicketID(t.ID); err != nil {
		return nil, err
	}
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" {
		return nil, ErrTicketTitleRequired
	}
	if t.Status == "" {
		t.Status = TicketStatusTodo
	}
	if !t.Status.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, t.Status)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var exists int
	switch err := tx.QueryRow(`SELECT 1 FROM tickets WHERE id = ?`, t.ID).Scan(&exists); err {
	case nil:
		return nil, fmt.Errorf("%w: %q is already taken — pick a new name, or append a number (e.g. %q)", ErrTicketIDTaken, t.ID, t.ID+"-2")
	case sql.ErrNoRows:
		// free to use
	default:
		return nil, err
	}

	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Status.IsTerminal() {
		closed := now
		t.ClosedAt = &closed
	} else {
		t.ClosedAt = nil
	}
	t.ArchivedAt = nil

	if _, err := tx.Exec(`
		INSERT INTO tickets (
			id, title, description, status, assignee, cwd, last_agent_id,
			project_id, created_at, updated_at, closed_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		t.ID, t.Title, t.Description, string(t.Status), t.Assignee, t.Cwd, t.LastAgentID,
		t.ProjectID, formatTicketTime(now), formatTicketTime(now),
		formatTicketTimePtr(t.ClosedAt), formatTicketTimePtr(t.ArchivedAt),
	); err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: t.ID,
		Kind:     TicketEventCreated,
		Author:   author,
		ToStatus: t.Status,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTicket returns a ticket with its full activity thread and attachments, or
// (nil, nil) if it doesn't exist.
func (s *Store) GetTicket(id string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	ticket, err := scanTicket(s.db.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if ticket.Activity, err = s.ticketActivity(id); err != nil {
		return nil, err
	}
	if ticket.Attachments, err = s.ticketAttachments(id); err != nil {
		return nil, err
	}
	return ticket, nil
}

// ListTickets returns tickets matching the filter, newest first. Activity and
// attachments are not loaded (use GetTicket for the full record).
func (s *Store) ListTickets(filter TicketListFilter) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}

	where := []string{}
	args := []any{}
	if !filter.IncludeArchived {
		where = append(where, `archived_at = ''`)
	}
	if filter.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, string(filter.Status))
	}
	query := ticketSelect
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	return tickets, rows.Err()
}

// SetTicketStatus moves a ticket to a new column and records the change in the
// activity thread (from -> to, with an optional comment). Transitions are
// permissive. Entering a terminal status stamps closed_at; leaving one clears it
// AND un-archives the ticket — reopening to an open status makes it durable and
// visible on the board again, never a hidden zombie immune to the TTL sweep.
// Returns the updated ticket row.
func (s *Store) SetTicketStatus(id string, to TicketStatus, author, comment string, now time.Time) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}
	if !to.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, to)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	if err != nil {
		return nil, err
	}

	from := current.Status
	closedAt := current.ClosedAt
	archivedAt := current.ArchivedAt
	switch {
	case to.IsTerminal() && !from.IsTerminal():
		closed := now
		closedAt = &closed
	case !to.IsTerminal():
		// Reopening to an open status: clear closed_at AND un-archive, so the
		// ticket returns to the durable, visible, sweepable board. Otherwise an
		// archived-then-reopened ticket becomes an invisible zombie.
		closedAt = nil
		archivedAt = nil
	}

	if _, err := tx.Exec(`
		UPDATE tickets SET status = ?, updated_at = ?, closed_at = ?, archived_at = ? WHERE id = ?
	`, string(to), formatTicketTime(now), formatTicketTimePtr(closedAt), formatTicketTimePtr(archivedAt), id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_activity (ticket_id, kind, author, from_status, to_status, comment, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, string(TicketActivityStatusChange), author, string(from), string(to), comment, formatTicketTime(now)); err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID:   id,
		Kind:       TicketEventStatusChanged,
		Author:     author,
		FromStatus: from,
		ToStatus:   to,
		Comment:    comment,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	current.Status = to
	current.ClosedAt = closedAt
	current.ArchivedAt = archivedAt
	current.UpdatedAt = now
	return current, nil
}

// AddTicketComment appends a freeform comment to the activity thread and bumps
// the ticket's updated_at. Returns the new activity entry.
func (s *Store) AddTicketComment(id, author, comment string, now time.Time) (*TicketActivity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := touchTicketTx(tx, id, now); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`
		INSERT INTO ticket_activity (ticket_id, kind, author, comment, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, string(TicketActivityComment), author, comment, formatTicketTime(now))
	if err != nil {
		return nil, err
	}
	activityID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: id,
		Kind:     TicketEventCommented,
		Author:   author,
		Comment:  comment,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TicketActivity{
		ID:        activityID,
		TicketID:  id,
		Kind:      TicketActivityComment,
		Author:    author,
		Comment:   comment,
		CreatedAt: now,
	}, nil
}

// EditTicketDescription replaces the ticket's brief, bumps updated_at, and emits a
// description_edited event authored by author.
func (s *Store) EditTicketDescription(id, description, author string, now time.Time) error {
	return s.updateTicketFieldWithEvent(id, "description", description, TicketEvent{
		TicketID: id,
		Kind:     TicketEventDescriptionEdited,
		Author:   author,
	}, now)
}

// AssignTicket sets (or clears, with "") the assignee, bumps updated_at, and emits
// an assigned event (Detail = the new assignee) authored by author.
func (s *Store) AssignTicket(id, assignee, author string, now time.Time) error {
	return s.updateTicketFieldWithEvent(id, "assignee", assignee, TicketEvent{
		TicketID: id,
		Kind:     TicketEventAssigned,
		Author:   author,
		Detail:   assignee,
	}, now)
}

// SetTicketSession records the last session's working dir and agent id, which the
// Resume affordance (slice 4) reloads from.
func (s *Store) SetTicketSession(id, cwd, lastAgentID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	res, err := s.db.Exec(`
		UPDATE tickets SET cwd = ?, last_agent_id = ?, updated_at = ? WHERE id = ?
	`, cwd, lastAgentID, formatTicketTime(now), id)
	if err != nil {
		return err
	}
	return ticketUpdateResult(res, id)
}

// AddTicketAttachment records a handover file on the ticket, bumps updated_at, and
// emits an attachment_added event (Detail = filename) authored by author. Returns
// the stored attachment (with its assigned id).
func (s *Store) AddTicketAttachment(att TicketAttachment, author string, now time.Time) (*TicketAttachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, nil
	}
	att.Filename = strings.TrimSpace(att.Filename)
	if att.Filename == "" {
		return nil, errors.New("attachment filename required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := touchTicketTx(tx, att.TicketID, now); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`
		INSERT INTO ticket_attachments (ticket_id, filename, path, note, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, att.TicketID, att.Filename, att.Path, att.Note, formatTicketTime(now))
	if err != nil {
		return nil, err
	}
	attachmentID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: att.TicketID,
		Kind:     TicketEventAttachmentAdded,
		Author:   author,
		Detail:   att.Filename,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	att.ID = attachmentID
	att.CreatedAt = now
	return &att, nil
}

// ArchiveTicket clears a closed ticket from the active board. Only terminal
// tickets may be archived — open tickets are durable and stay until they settle.
func (s *Store) ArchiveTicket(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	if err != nil {
		return err
	}
	if !current.Status.IsTerminal() {
		return fmt.Errorf("%w: %q is %s", ErrTicketNotClosed, id, current.Status)
	}

	if _, err := tx.Exec(`
		UPDATE tickets SET archived_at = ?, updated_at = ? WHERE id = ?
	`, formatTicketTime(now), formatTicketTime(now), id); err != nil {
		return err
	}
	return tx.Commit()
}

// SweepExpiredTickets hard-deletes terminal tickets whose closed_at is older than
// now-ttl, cascading to their activity and attachments. Open tickets (a durable
// backlog) are never swept. Returns the number of tickets removed. The caller
// passes now and the TTL (production: time.Now() and 30 days); tests inject both.
func (s *Store) SweepExpiredTickets(now time.Time, ttl time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}

	cutoff := formatTicketTime(now.Add(-ttl))
	// closed_at is a fixed-width RFC3339 UTC string, so a lexical compare is a
	// chronological compare.
	const expired = `status IN ('done','failed','crashed') AND closed_at != '' AND closed_at < ?`

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM ticket_activity WHERE ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`, cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_attachments WHERE ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`, cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_events WHERE ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM tickets WHERE `+expired, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// --- internal helpers ---

const ticketSelect = `
	SELECT id, title, description, status, assignee, cwd, last_agent_id,
		project_id, created_at, updated_at, closed_at, archived_at
	FROM tickets`

// updateTicketFieldWithEvent sets a single text column plus updated_at and emits
// evt, atomically. column is a trusted internal literal, never caller input.
func (s *Store) updateTicketFieldWithEvent(id, column, value string, evt TicketEvent, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE tickets SET `+column+` = ?, updated_at = ? WHERE id = ?`,
		value, formatTicketTime(now), id,
	)
	if err != nil {
		return err
	}
	if err := ticketUpdateResult(res, id); err != nil {
		return err
	}
	if _, _, err := appendTicketEventTx(tx, evt, now); err != nil {
		return err
	}
	return tx.Commit()
}

// touchTicketTx bumps updated_at within a transaction, returning ErrTicketNotFound
// when the ticket is absent (so an activity/attachment can't orphan onto nothing).
func touchTicketTx(tx *sql.Tx, id string, now time.Time) error {
	res, err := tx.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, formatTicketTime(now), id)
	if err != nil {
		return err
	}
	return ticketUpdateResult(res, id)
}

func ticketUpdateResult(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %q", ErrTicketNotFound, id)
	}
	return nil
}

func (s *Store) ticketActivity(ticketID string) ([]TicketActivity, error) {
	rows, err := s.db.Query(`
		SELECT id, ticket_id, kind, author, from_status, to_status, comment, created_at
		FROM ticket_activity WHERE ticket_id = ? ORDER BY id ASC
	`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activity []TicketActivity
	for rows.Next() {
		var (
			a         TicketActivity
			kind      string
			from, to  string
			createdAt string
		)
		if err := rows.Scan(&a.ID, &a.TicketID, &kind, &a.Author, &from, &to, &a.Comment, &createdAt); err != nil {
			return nil, err
		}
		a.Kind = TicketActivityKind(kind)
		a.FromStatus = TicketStatus(from)
		a.ToStatus = TicketStatus(to)
		a.CreatedAt = parseTicketTime(createdAt)
		activity = append(activity, a)
	}
	return activity, rows.Err()
}

func (s *Store) ticketAttachments(ticketID string) ([]TicketAttachment, error) {
	rows, err := s.db.Query(`
		SELECT id, ticket_id, filename, path, note, created_at
		FROM ticket_attachments WHERE ticket_id = ? ORDER BY id ASC
	`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attachments []TicketAttachment
	for rows.Next() {
		var (
			att       TicketAttachment
			createdAt string
		)
		if err := rows.Scan(&att.ID, &att.TicketID, &att.Filename, &att.Path, &att.Note, &createdAt); err != nil {
			return nil, err
		}
		att.CreatedAt = parseTicketTime(createdAt)
		attachments = append(attachments, att)
	}
	return attachments, rows.Err()
}

func scanTicket(scanner ticketScanner) (*Ticket, error) {
	var (
		t          Ticket
		status     string
		createdAt  string
		updatedAt  string
		closedAt   string
		archivedAt string
	)
	if err := scanner.Scan(
		&t.ID, &t.Title, &t.Description, &status, &t.Assignee, &t.Cwd, &t.LastAgentID,
		&t.ProjectID, &createdAt, &updatedAt, &closedAt, &archivedAt,
	); err != nil {
		return nil, err
	}
	t.Status = TicketStatus(status)
	t.CreatedAt = parseTicketTime(createdAt)
	t.UpdatedAt = parseTicketTime(updatedAt)
	if closedAt != "" {
		ts := parseTicketTime(closedAt)
		t.ClosedAt = &ts
	}
	if archivedAt != "" {
		ts := parseTicketTime(archivedAt)
		t.ArchivedAt = &ts
	}
	return &t, nil
}

// formatTicketTime renders a timestamp as a fixed-width RFC3339 UTC string, so
// stored timestamps sort lexically (which the TTL sweep relies on).
func formatTicketTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatTicketTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTicketTime(*t)
}

// parseTicketTime is the inverse of formatTicketTime; an unparseable value yields
// the zero time rather than an error, since timestamps are store-written.
func parseTicketTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
