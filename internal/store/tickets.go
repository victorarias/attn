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

// TicketAuthorAttn is the event author attn uses when it writes a transition on a
// ticket itself rather than on behalf of the user, the chief, or an agent — the
// Crashed status a dead worker could not report, and the crashed→working flip
// when that worker is revived. It is an authoring identity, never an observer,
// so it accrues no cursors and is excluded from nobody's unread feed.
const TicketAuthorAttn = "attn"

// TicketAuthorYou is the identity the human user authors with when acting on a
// ticket directly from the app (changing status, commenting, re-briefing). It is a
// participant like any other — its events notify the assigned agent — but it has no
// session, so it is never itself nudged; the app sees changes through the live
// board broadcast instead.
const TicketAuthorYou = "you"

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

// TicketActivityKind is the type of a human-facing history entry.
type TicketActivityKind string

const (
	// TicketActivityStatusChange records a column move (from -> to), optionally
	// with an accompanying comment.
	TicketActivityStatusChange TicketActivityKind = "status_change"
	// TicketActivityComment records a freeform note from either side.
	TicketActivityComment TicketActivityKind = "comment"
	// TicketActivityAttach records the files and decision context submitted in
	// one durable ticket attach.
	TicketActivityAttach TicketActivityKind = "attach"
)

// Ticket is the durable record. Activity and Attachments are populated by
// GetTicket; list operations leave them nil for cheapness.
type Ticket struct {
	ID              string
	Title           string
	Description     string
	Status          TicketStatus
	Assignee        string // bound session id (delegated work), "you" (human), or "" when unassigned; the session-id form is the resume key
	Cwd             string // last session's working dir (for resume)
	LastAgentID     string // last session's agent id (for resume)
	ProjectID       string // future grouping; "" when ungrouped
	AutomationRunID string // immutable provenance for automation-created work; empty for ordinary tickets
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ClosedAt        *time.Time // set on entering a terminal status; drives the TTL
	ArchivedAt      *time.Time // set when manually cleared from the board
	// ReconciledAt is the machine-reconciliation flag: set (atomically, via
	// ClaimTicketReconciliation) when a dead owning session's outcome was judged
	// by the reconciliation classifier. Provenance + dedupe lock in one; cleared
	// when the ticket is reassigned or its assignee session respawns (re-arm).
	ReconciledAt *time.Time
	// LatestEventSeq is populated by GetTicket for optimistic app mutations.
	// Bare list rows leave it at zero.
	LatestEventSeq int64

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

// TicketAttachment is a file attached to the ticket. Slice 1 stores the
// record; the file-handling ergonomics (copy-into-store) come with slice 4.
type TicketAttachment struct {
	ID        int64
	TicketID  string
	Filename  string
	Path      string
	Note      string
	CreatedAt time.Time
}

// TicketAttachResult is the durable portion of an attachment receipt.
type TicketAttachResult struct {
	EventSeq     int64
	Status       TicketStatus
	Deduplicated bool
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
	return s.createTicket(t, author, "", now)
}

// CreateRoleOwnedTicket creates a ticket whose notification ownership belongs to
// a durable profile role. The concrete author remains on the event for audit.
func (s *Store) CreateRoleOwnedTicket(t Ticket, author, ownerRole string, now time.Time) (*Ticket, error) {
	return s.createTicket(t, author, strings.TrimSpace(ownerRole), now)
}

// EnsureAutomationTicket creates or adopts the unique ticket for an automation run.
func (s *Store) EnsureAutomationTicket(t Ticket, author, ownerRole string, now time.Time) (*Ticket, error) {
	if t.AutomationRunID == "" {
		return nil, errors.New("automation run id required")
	}
	if existing, err := s.GetTicketByAutomationRunID(t.AutomationRunID); err != nil || existing != nil {
		return existing, err
	}
	return s.createTicket(t, author, strings.TrimSpace(ownerRole), now)
}

func (s *Store) createTicket(t Ticket, author, ownerRole string, now time.Time) (*Ticket, error) {
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
	t.ReconciledAt = nil // never born reconciled; the column defaults to ''

	if _, err := tx.Exec(`
		INSERT INTO tickets (
			id, title, description, status, assignee, cwd, last_agent_id,
			project_id, automation_run_id, created_at, updated_at, closed_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		t.ID, t.Title, t.Description, string(t.Status), t.Assignee, t.Cwd, t.LastAgentID,
		t.ProjectID, nullIfEmpty(t.AutomationRunID), formatTicketTime(now), formatTicketTime(now),
		formatTicketTimePtr(t.ClosedAt), formatTicketTimePtr(t.ArchivedAt),
	); err != nil {
		return nil, err
	}
	createdSeq, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: t.ID,
		Kind:     TicketEventCreated,
		Author:   author,
		ToStatus: t.Status,
	}, now)
	if err != nil {
		return nil, err
	}
	if ownerRole != "" {
		if _, err := tx.Exec(`
			INSERT INTO ticket_role_owners (role, ticket_id, created_at)
			VALUES (?, ?, ?)
		`, ownerRole, t.ID, formatTicketTime(now)); err != nil {
			return nil, err
		}
	}
	// A ticket born WITH an assignee is a chief delegation: the brief was already
	// handed to that agent out of band via the spawn prompt, so mark the `created`
	// event consumed for the assignee. Otherwise it lingers unread on the agent's
	// OWN ticket and doorbells it the moment it goes idle — a self-nudge about a
	// brief it already holds. A backlog ticket is born unassigned, so a later pickup
	// still receives the full brief through `attn ticket inbox`.
	//
	// This is a DELEGATION policy living in generic store code: it holds only because
	// delegation is the sole create path that sets an assignee at birth (backlog
	// create is unbound). A future "create a pre-assigned ticket whose brief SHOULD
	// arrive via the inbox" would silently lose its brief here — move this guard up to
	// the delegation caller if that case ever appears.
	if t.Assignee != "" {
		if err := setTicketCursorTx(tx, t.Assignee, t.ID, createdSeq, now); err != nil {
			return nil, err
		}
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
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM ticket_events WHERE ticket_id = ?`, id).Scan(&ticket.LatestEventSeq); err != nil {
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

// ActiveTicketForSession returns the non-terminal ticket currently assigned to a
// session — the delegated work it is running — or nil if none. The delegated
// session's id is the ticket's assignee, so assignee == session is the session ->
// ticket binding (used by the report path and crash detection). Activity and
// attachments are not loaded.
func (s *Store) ActiveTicketForSession(sessionID string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ticketSelect+` WHERE assignee = ? ORDER BY created_at DESC, id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		if !ticket.Status.IsTerminal() {
			return ticket, nil
		}
	}
	return nil, rows.Err()
}

// ActiveTicketsForSession returns ALL non-terminal tickets currently assigned to
// a session, newest first — the session-end reconciliation seam needs every one
// (a session can hold several via `attn ticket take` plus its delegation), where
// ActiveTicketForSession's newest-only answer suffices for the report path.
// Activity and attachments are not loaded.
func (s *Store) ActiveTicketsForSession(sessionID string) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil || sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ticketSelect+` WHERE assignee = ? ORDER BY created_at DESC, id DESC`, sessionID)
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
		if !ticket.Status.IsTerminal() {
			tickets = append(tickets, ticket)
		}
	}
	return tickets, rows.Err()
}

// CrashedTicketsForAssignee returns every non-archived ticket sitting in the
// Crashed column bound to a session id, newest first — the revival seam
// (a crashed session respawned/registered/adopted back to live) flips exactly
// these back to Working. Archived tickets stay archived: the user dismissed
// them from the board, so a revival must not resurrect them. Activity and
// attachments are not loaded.
func (s *Store) CrashedTicketsForAssignee(assignee string) ([]*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		ticketSelect+` WHERE assignee = ? AND status = ? AND archived_at = '' ORDER BY created_at DESC, id DESC`,
		assignee, string(TicketStatusCrashed),
	)
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

// ClaimTicketReconciliation atomically claims the machine-reconciliation flag
// (set-if-unset). Returns true when this caller won the claim; false when the
// flag was already set — another path (death-hook vs sweep, or a double-fired
// session-end seam) owns this verdict. Purely internal bookkeeping: it does NOT
// bump updated_at or emit an event — the verdict comment that follows is the
// visible artifact; the flag is provenance + lock.
func (s *Store) ClaimTicketReconciliation(id string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	res, err := s.db.Exec(
		`UPDATE tickets SET reconciled_at = ? WHERE id = ? AND reconciled_at = ''`,
		formatTicketTime(now), id,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ClearTicketReconciliationForAssignee re-arms reconciliation for every ticket
// bound to a session that just came back to life (a ticket resume respawning the
// assignee): the claimed flag means "this death was judged once", so a live
// owner must clear it for the NEXT death to be judged again. Like the claim, it
// does not bump updated_at or emit an event. A no-op when the session has no
// flagged tickets.
func (s *Store) ClearTicketReconciliationForAssignee(assignee string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE tickets SET reconciled_at = '' WHERE assignee = ? AND reconciled_at != ''`,
		assignee,
	)
	return err
}

// SetTicketStatus moves a ticket to a new column and records the change in the
// activity thread (from -> to, with an optional comment). Transitions are
// permissive. Entering a terminal status stamps closed_at; leaving one clears it
// AND un-archives the ticket — reopening to an open status makes it durable and
// visible on the board again, never a hidden zombie immune to the TTL sweep.
// Returns the updated ticket row.
func (s *Store) SetTicketStatus(id string, to TicketStatus, author, comment string, now time.Time) (*Ticket, error) {
	updated, _, err := s.SetTicketStatusWithOptions(id, to, author, comment, TicketMutationOptions{}, now)
	return updated, err
}

func (s *Store) SetTicketStatusWithOptions(
	id string,
	to TicketStatus,
	author, comment string,
	options TicketMutationOptions,
	now time.Time,
) (*Ticket, TicketMutationOutcome, error) {
	if !to.IsValid() {
		return nil, TicketMutationOutcome{}, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, to)
	}
	var updated *Ticket
	outcome, err := s.withTicketMutation(id, options, now, func(tx *sql.Tx) error {
		var mutationErr error
		updated, mutationErr = setTicketStatusTx(tx, id, to, author, comment, now)
		return mutationErr
	})
	return updated, outcome, err
}

func setTicketStatusTx(tx *sql.Tx, id string, to TicketStatus, author, comment string, now time.Time) (*Ticket, error) {
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

	current.Status = to
	current.ClosedAt = closedAt
	current.ArchivedAt = archivedAt
	current.UpdatedAt = now
	return current, nil
}

// AddTicketComment appends a freeform comment to the activity thread and bumps
// the ticket's updated_at. Returns the new activity entry.
func (s *Store) AddTicketComment(id, author, comment string, now time.Time) (*TicketActivity, error) {
	activity, _, err := s.AddTicketCommentWithOptions(id, author, comment, TicketMutationOptions{}, now)
	return activity, err
}

func addTicketCommentTx(tx *sql.Tx, id, author, comment string, now time.Time) (*TicketActivity, error) {
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
	return &TicketActivity{
		ID:        activityID,
		TicketID:  id,
		Kind:      TicketActivityComment,
		Author:    author,
		Comment:   comment,
		CreatedAt: now,
	}, nil
}

func (s *Store) AddTicketCommentWithOptions(
	id, author, comment string,
	options TicketMutationOptions,
	now time.Time,
) (*TicketActivity, TicketMutationOutcome, error) {
	var activity *TicketActivity
	outcome, err := s.withTicketMutation(id, options, now, func(tx *sql.Tx) error {
		var mutationErr error
		activity, mutationErr = addTicketCommentTx(tx, id, author, comment, now)
		return mutationErr
	})
	return activity, outcome, err
}

// EditTicketDescription replaces the ticket's brief, bumps updated_at, and emits a
// description_edited event authored by author. Detail carries the new brief so the
// event is self-describing AND so the dedup signature distinguishes one re-brief
// from another — without it, two consecutive edits would look identical and the
// second (a real re-brief / steer) would be silently deduped away.
func (s *Store) EditTicketDescription(id, description, author string, now time.Time) error {
	_, err := s.EditTicketDescriptionWithOptions(id, description, author, TicketMutationOptions{}, now)
	return err
}

func (s *Store) EditTicketDescriptionWithOptions(
	id, description, author string,
	options TicketMutationOptions,
	now time.Time,
) (TicketMutationOutcome, error) {
	evt := TicketEvent{
		TicketID: id,
		Kind:     TicketEventDescriptionEdited,
		Author:   author,
		Detail:   description,
	}
	return s.withTicketMutation(id, options, now, func(tx *sql.Tx) error {
		return updateTicketFieldWithEventTx(tx, id, "description", description, evt, now)
	})
}

// AssignTicket sets (or clears, with "") the assignee, bumps updated_at, and emits
// an assigned event (Detail = the new assignee) authored by author. It also clears
// the machine-reconciliation flag: reassignment gives the ticket a new (or
// renewed) owner, so a future death of that owner deserves a fresh verdict — the
// re-arm rule of orphaned-ticket reconciliation.
func (s *Store) AssignTicket(id, assignee, author string, now time.Time) error {
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
		`UPDATE tickets SET assignee = ?, reconciled_at = '', updated_at = ? WHERE id = ?`,
		assignee, formatTicketTime(now), id,
	)
	if err != nil {
		return err
	}
	if err := ticketUpdateResult(res, id); err != nil {
		return err
	}
	if _, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: id,
		Kind:     TicketEventAssigned,
		Author:   author,
		Detail:   assignee,
	}, now); err != nil {
		return err
	}
	return tx.Commit()
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

// SetTicketResumeSessionID mirrors the bound session's agent-native resume id onto
// every ticket assigned to that session. The session row (and its own
// resume_session_id) is deleted on close, so this durable copy on the ticket is
// what lets Resume reattach the prior conversation directly. Purely internal
// bookkeeping: it does NOT bump updated_at or emit an event, so it never churns
// the board. A no-op (zero rows) when the session has no bound ticket.
func (s *Store) SetTicketResumeSessionID(assignee, resumeSessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE tickets SET resume_session_id = ? WHERE assignee = ?`,
		strings.TrimSpace(resumeSessionID), assignee,
	)
	return err
}

// GetTicketResumeSessionID returns the stored agent-native resume id for the most
// recent ticket bound to assignee, or "" when none has one. Used at spawn time to
// resume a ticket whose session row has already been removed.
func (s *Store) GetTicketResumeSessionID(assignee string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return ""
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return ""
	}
	var resumeSessionID string
	err := s.db.QueryRow(
		`SELECT resume_session_id FROM tickets
		   WHERE assignee = ? AND resume_session_id != ''
		   ORDER BY updated_at DESC, id DESC LIMIT 1`,
		assignee,
	).Scan(&resumeSessionID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(resumeSessionID)
}

// AddTicketAttachment records an attached file on the ticket, bumps updated_at, and
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

// SubmitTicketAttach records one durable attachment receipt and optionally moves
// the ticket in the same transaction. The fingerprint prefix in detail makes a
// lost-response retry discoverable even when a status event followed the attach.
func (s *Store) SubmitTicketAttach(
	ticketID, author, fingerprint, detail, activityComment string,
	status *TicketStatus,
	now time.Time,
) (*TicketAttachResult, error) {
	result, _, err := s.SubmitTicketAttachWithOptions(
		ticketID, author, fingerprint, detail, activityComment, status,
		TicketMutationOptions{}, now,
	)
	return result, err
}

func (s *Store) SubmitTicketAttachWithOptions(
	ticketID, author, fingerprint, detail, activityComment string,
	status *TicketStatus,
	options TicketMutationOptions,
	now time.Time,
) (*TicketAttachResult, TicketMutationOutcome, error) {
	if strings.TrimSpace(fingerprint) == "" {
		return nil, TicketMutationOutcome{}, errors.New("attach fingerprint required")
	}
	if status != nil && !status.IsValid() {
		return nil, TicketMutationOutcome{}, fmt.Errorf("%w: %q", ErrInvalidTicketStatus, *status)
	}
	var result *TicketAttachResult
	outcome, err := s.withTicketMutation(ticketID, options, now, func(tx *sql.Tx) error {
		var mutationErr error
		result, mutationErr = submitTicketAttachTx(tx, ticketID, author, fingerprint, detail, activityComment, status, now)
		return mutationErr
	})
	return result, outcome, err
}

func submitTicketAttachTx(
	tx *sql.Tx,
	ticketID, author, fingerprint, detail, activityComment string,
	status *TicketStatus,
	now time.Time,
) (*TicketAttachResult, error) {
	var existingSeq int64
	var existingStatus string
	err := tx.QueryRow(`
		SELECT seq, to_status FROM ticket_events
		WHERE ticket_id = ? AND kind = ? AND detail LIKE ?
		ORDER BY seq DESC LIMIT 1
	`, ticketID, string(TicketEventAttachSubmitted), fingerprint+"\n%").Scan(&existingSeq, &existingStatus)
	if err == nil {
		return &TicketAttachResult{EventSeq: existingSeq, Status: TicketStatus(existingStatus), Deduplicated: true}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	current, err := scanTicket(tx.QueryRow(ticketSelect+` WHERE id = ?`, ticketID))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %q", ErrTicketNotFound, ticketID)
	}
	if err != nil {
		return nil, err
	}
	if err := touchTicketTx(tx, ticketID, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_activity (ticket_id, kind, author, comment, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, ticketID, string(TicketActivityAttach), author, activityComment, formatTicketTime(now)); err != nil {
		return nil, err
	}
	resultStatus := current.Status
	if status != nil {
		resultStatus = *status
	}
	eventSeq, _, err := appendTicketEventTx(tx, TicketEvent{
		TicketID: ticketID,
		Kind:     TicketEventAttachSubmitted,
		Author:   author,
		Comment:  activityComment,
		Detail:   detail,
		ToStatus: resultStatus,
	}, now)
	if err != nil {
		return nil, err
	}
	if status != nil {
		updated, updateErr := setTicketStatusTx(tx, ticketID, *status, author, "", now)
		if updateErr != nil {
			return nil, updateErr
		}
		resultStatus = updated.Status
	}
	return &TicketAttachResult{EventSeq: eventSeq, Status: resultStatus}, nil
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
// now-ttl, cascading to their activity, attachments, events, and event cursors.
// Any still-active automation continuity binding the ticket documents is
// released (status=released, reason=ticket_swept — see the
// automation_continuity_bindings update below), not deleted: binding rows are
// append-only history in v2. Open tickets (a durable backlog) are never swept.
// Returns the number of tickets removed. The caller
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
	if _, err := tx.Exec(`DELETE FROM ticket_event_cursors WHERE ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`, cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ticket_subscriptions WHERE ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`, cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM automation_ticket_occurrence_events WHERE ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`, cutoff); err != nil {
		return 0, err
	}
	// A continuity binding's ticket_id pins it to one thread's documenting
	// ticket; once that ticket is swept there is nothing left to resume, so
	// release the binding atomically with it. Bindings are append-only (v2's
	// Data Model invariant, docs/plans/2026-07-21-automations-v2-simplification.md):
	// this releases the row (status/released_reason/released_at), it never
	// deletes it, so a swept thread still leaves a released row as history —
	// exactly the reason the ticket_swept release reason exists. Already-released
	// rows are left untouched (their own reason/timestamps stand); only a row
	// still active when its ticket sweeps is affected. This is what actually
	// bounds a bound thread's worktree lifetime (see
	// AutomationSessionHasContinuityBinding and ListPrunableAutomationRuns): the
	// per-subject reap in ReconcileAutomationReviewRequests only fires once, at
	// the moment a review request's edge goes inactive, and no-ops if the ticket
	// is still open at that instant — it does not revisit an edge that stays
	// inactive while its ticket later ages out. Leave that reap alone; it still
	// covers the case where the ticket was already gone at withdraw time.
	if _, err := tx.Exec(
		`UPDATE automation_continuity_bindings SET status=?,released_reason=?,released_at=?,updated_at=? WHERE status=? AND ticket_id IN (SELECT id FROM tickets WHERE `+expired+`)`,
		AutomationBindingStatusReleased, AutomationBindingReleasedTicketSwept, formatTicketTime(now), formatTicketTime(now), AutomationBindingStatusActive, cutoff,
	); err != nil {
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
		project_id, automation_run_id, created_at, updated_at, closed_at, archived_at, reconciled_at
	FROM tickets`

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

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

	if err := updateTicketFieldWithEventTx(tx, id, column, value, evt, now); err != nil {
		return err
	}
	return tx.Commit()
}

func updateTicketFieldWithEventTx(tx *sql.Tx, id, column, value string, evt TicketEvent, now time.Time) error {
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
	return nil
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
		t               Ticket
		status          string
		createdAt       string
		updatedAt       string
		closedAt        string
		archivedAt      string
		reconciledAt    string
		automationRunID sql.NullString
	)
	if err := scanner.Scan(
		&t.ID, &t.Title, &t.Description, &status, &t.Assignee, &t.Cwd, &t.LastAgentID,
		&t.ProjectID, &automationRunID, &createdAt, &updatedAt, &closedAt, &archivedAt, &reconciledAt,
	); err != nil {
		return nil, err
	}
	t.Status = TicketStatus(status)
	t.AutomationRunID = automationRunID.String
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
	if reconciledAt != "" {
		ts := parseTicketTime(reconciledAt)
		t.ReconciledAt = &ts
	}
	return &t, nil
}

func (s *Store) GetTicketByAutomationRunID(runID string) (*Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	t, err := scanTicket(s.db.QueryRow(ticketSelect+` WHERE automation_run_id = ?`, runID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
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
