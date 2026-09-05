package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Every other closed_by is the id of the session that closed this one.
const SessionClosedByUser = "user"

// SessionLedgerDefaultLimit is one terminal page: 20 rows plus a header and the
// omitted notice fit an 80x24 window without scrolling.
const SessionLedgerDefaultLimit = 20

// A tripwire, not a budget: the busiest measured afternoon closed 10 sessions in
// 3.5 hours, so a heavy month stays under 3000 and --before reaches the rest.
const SessionLedgerMaxLimit = 1000

// ErrSessionClosed refuses a write that would run an already closed session.
var ErrSessionClosed = errors.New("session is closed")

// SessionClose says who closed a session and why. Only agent closes carry a reason.
type SessionClose struct {
	By     string
	Reason string
}

type SessionLedgerScope string

const (
	SessionLedgerLive   SessionLedgerScope = "live"
	SessionLedgerClosed SessionLedgerScope = "closed"
	SessionLedgerAll    SessionLedgerScope = "all"
)

// Before is the previous page's last row. Filters combine with AND and apply
// before paging, so a page is the newest matching rows, not a filtered page.
type SessionLedgerQuery struct {
	Scope       SessionLedgerScope
	Limit       int
	Before      string
	WorkspaceID string
	// A repository as a local path, matched exactly.
	Repository string
	// Half-open over the row's instant: Since inclusive, Until exclusive.
	Since time.Time
	Until time.Time
	// Facets costs two extra GROUP BYs; a page fetched with Before skips them
	// because the choices belong to the query, not to the page.
	Facets bool
}

type SessionLedgerPage struct {
	Entries []protocol.SessionLedgerEntry
	// Omitted counts the rows this page left out, all older than its last entry.
	Omitted    int
	NextBefore string
	// Nil unless the query asked for them.
	Facets *protocol.SessionLedgerFacets
}

// ErrUnknownLedgerCursor names the id a stale --before failed on.
type ErrUnknownLedgerCursor struct{ ID string }

func (e *ErrUnknownLedgerCursor) Error() string {
	return fmt.Sprintf("no session %q in the ledger to page from", e.ID)
}

type ErrLedgerLimitTooLarge struct{ Asked, Max int }

func (e *ErrLedgerLimitTooLarge) Error() string {
	return fmt.Sprintf("asked for %d rows but one page holds at most %d; ask for %d or fewer and paginate with the last row's id",
		e.Asked, e.Max, e.Max)
}

// CloseSession returns false when no live session carries the id.
func (s *Store) CloseSession(id string, closed SessionClose, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	by := strings.TrimSpace(closed.By)
	if by == "" {
		by = SessionClosedByUser
	}
	at := now.UTC().Format(time.RFC3339Nano)

	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false, nil
		}
		if s.sessionCloses == nil {
			s.sessionCloses = make(map[string]sessionCloseMark)
		}
		if _, already := s.sessionCloses[id]; already {
			return false, nil
		}
		delete(s.sessions, id)
		s.sessionCloses[id] = sessionCloseMark{At: at, By: by, Reason: strings.TrimSpace(closed.Reason), session: session}
		if cost, tracked := s.sessionCosts[id]; tracked {
			finalizeSessionCost(&cost)
			s.sessionCosts[id] = cost
		}
		return true, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("close session %s: %w", id, err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`UPDATE sessions SET closed_at = ?, closed_by = ?, close_reason = ?
		WHERE id = ? AND closed_at = ''`, at, by, strings.TrimSpace(closed.Reason), id)
	if err != nil {
		return false, fmt.Errorf("close session %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("close session %s: %w", id, err)
	}
	if affected != 1 {
		return false, nil
	}
	if err := finalizeSessionCostTx(tx, id); err != nil {
		return false, fmt.Errorf("close session %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("close session %s: %w", id, err)
	}
	return true, nil
}

// The per-observation usage is the row's whole size: a measured 109 KB mean
// against about 14 KB for the ids alone (receipt on s-rxx9kp).
func finalizeSessionCostTx(tx *sql.Tx, id string) error {
	var raw string
	if err := tx.QueryRow("SELECT session_cost_json FROM sessions WHERE id = ?", id).Scan(&raw); err != nil {
		return err
	}
	state, err := decodeSessionCostState(raw)
	if err != nil {
		return err
	}
	if len(state.Observations) == 0 {
		return nil
	}
	finalizeSessionCost(&state)
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", string(encoded), id)
	return err
}

// SessionCloseRecord is a close as the ledger holds it, timestamp included, so a
// lifted close can go back exactly as it was.
type SessionCloseRecord struct {
	At     string
	By     string
	Reason string
}

// ReopenSession hands back the close it lifted; ok is false when the id names no
// closed session. Pass the record to RestoreSessionClose to undo the reopen.
func (s *Store) ReopenSession(id string) (SessionCloseRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		mark, closed := s.sessionCloses[id]
		if !closed {
			return SessionCloseRecord{}, false, nil
		}
		s.sessions[id] = mark.session
		delete(s.sessionCloses, id)
		return SessionCloseRecord{At: mark.At, By: mark.By, Reason: mark.Reason}, true, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return SessionCloseRecord{}, false, fmt.Errorf("reopen session %s: %w", id, err)
	}
	defer tx.Rollback()

	var lifted SessionCloseRecord
	err = tx.QueryRow("SELECT closed_at, closed_by, close_reason FROM sessions WHERE id = ? AND closed_at <> ''", id).
		Scan(&lifted.At, &lifted.By, &lifted.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionCloseRecord{}, false, nil
	}
	if err != nil {
		return SessionCloseRecord{}, false, fmt.Errorf("reopen session %s: %w", id, err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET closed_at = '', closed_by = '', close_reason = ''
		WHERE id = ?`, id); err != nil {
		return SessionCloseRecord{}, false, fmt.Errorf("reopen session %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return SessionCloseRecord{}, false, fmt.Errorf("reopen session %s: %w", id, err)
	}
	return lifted, true, nil
}

// RestoreSessionClose puts back a close ReopenSession lifted. The cost the close
// finalized stayed finalized through the reopen, so only the mark returns.
func (s *Store) RestoreSessionClose(id string, closed SessionCloseRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		session := s.sessions[id]
		if session == nil {
			return false, nil
		}
		delete(s.sessions, id)
		s.sessionCloses[id] = sessionCloseMark{At: closed.At, By: closed.By, Reason: closed.Reason, session: session}
		return true, nil
	}

	result, err := s.db.Exec(`UPDATE sessions SET closed_at = ?, closed_by = ?, close_reason = ?
		WHERE id = ? AND closed_at = ''`, closed.At, closed.By, closed.Reason, id)
	if err != nil {
		return false, fmt.Errorf("restore the close of session %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("restore the close of session %s: %w", id, err)
	}
	return affected == 1, nil
}

func (s *Store) SessionClosed(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionClosedLocked(id)
}

func (s *Store) sessionClosedLocked(id string) bool {
	if s.db == nil {
		_, closed := s.sessionCloses[id]
		return closed
	}
	var closedAt string
	err := s.db.QueryRow("SELECT closed_at FROM sessions WHERE id = ?", id).Scan(&closedAt)
	if err != nil {
		return false
	}
	return closedAt != ""
}

func (s *Store) SessionLedgerEntry(id string) *protocol.SessionLedgerEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return s.ledgerEntryMemoryLocked(id)
	}

	row := s.db.QueryRow(ledgerSelect+" FROM sessions WHERE id = ?", id)
	entry, err := scanLedgerEntry(row)
	if err != nil {
		return nil
	}
	return &entry
}

// SessionLedger reads a page newest first, by close or by last sighting.
func (s *Store) SessionLedger(query SessionLedgerQuery) (SessionLedgerPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = SessionLedgerDefaultLimit
	}
	if limit > SessionLedgerMaxLimit {
		return SessionLedgerPage{}, &ErrLedgerLimitTooLarge{Asked: limit, Max: SessionLedgerMaxLimit}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return s.sessionLedgerMemory(query, limit)
	}
	return s.sessionLedgerDB(query, limit)
}

const ledgerSelect = `SELECT id, label, agent, directory, workspace_id, branch, is_worktree, main_repo,
	repository, state, last_seen, closed_at, closed_by, close_reason`

const ledgerAt = `CASE WHEN closed_at <> '' THEN closed_at ELSE last_seen END`

// Filters that belong to the whole query: the facets are counted over these
// alone, so choosing a repository never empties the workspace choices.
func (q SessionLedgerQuery) scopeAndWindow() ([]string, []any) {
	var where []string
	var args []any
	switch q.Scope {
	case SessionLedgerClosed:
		where = append(where, "closed_at <> ''")
	case SessionLedgerAll:
	default:
		where = append(where, "closed_at = ''")
	}
	if !q.Since.IsZero() {
		where = append(where, ledgerAt+" >= ?")
		args = append(args, q.Since.UTC().Format(time.RFC3339Nano))
	}
	if !q.Until.IsZero() {
		where = append(where, ledgerAt+" < ?")
		args = append(args, q.Until.UTC().Format(time.RFC3339Nano))
	}
	return where, args
}

func (q SessionLedgerQuery) selection() ([]string, []any) {
	where, args := q.scopeAndWindow()
	if workspace := strings.TrimSpace(q.WorkspaceID); workspace != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, workspace)
	}
	if repository := strings.TrimSpace(q.Repository); repository != "" {
		where = append(where, "repository = ?")
		args = append(args, repository)
	}
	return where, args
}

func whereClause(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(where, " AND ")
}

func (s *Store) sessionLedgerDB(query SessionLedgerQuery, limit int) (SessionLedgerPage, error) {
	where, args := query.selection()

	if cursor := strings.TrimSpace(query.Before); cursor != "" {
		var at string
		err := s.db.QueryRow("SELECT "+ledgerAt+" FROM sessions WHERE id = ?", cursor).Scan(&at)
		if errors.Is(err, sql.ErrNoRows) {
			return SessionLedgerPage{}, &ErrUnknownLedgerCursor{ID: cursor}
		}
		if err != nil {
			return SessionLedgerPage{}, fmt.Errorf("read ledger cursor %s: %w", cursor, err)
		}
		where = append(where, "("+ledgerAt+" < ? OR ("+ledgerAt+" = ? AND id < ?))")
		args = append(args, at, at, cursor)
	}

	clause := whereClause(where)

	var matching int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions"+clause, args...).Scan(&matching); err != nil {
		return SessionLedgerPage{}, fmt.Errorf("count ledger rows: %w", err)
	}

	rows, err := s.db.Query(ledgerSelect+" FROM sessions"+clause+
		" ORDER BY "+ledgerAt+" DESC, id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return SessionLedgerPage{}, fmt.Errorf("read ledger rows: %w", err)
	}
	defer rows.Close()

	page := SessionLedgerPage{}
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return SessionLedgerPage{}, fmt.Errorf("read ledger row: %w", err)
		}
		page.Entries = append(page.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return SessionLedgerPage{}, fmt.Errorf("read ledger rows: %w", err)
	}
	page = finishLedgerPage(page, matching)

	if query.Facets {
		facets, err := s.ledgerFacetsDB(query)
		if err != nil {
			return SessionLedgerPage{}, err
		}
		page.Facets = facets
	}
	return page, nil
}

func (s *Store) ledgerFacetsDB(query SessionLedgerQuery) (*protocol.SessionLedgerFacets, error) {
	where, args := query.scopeAndWindow()
	clause := whereClause(where)

	count := func(column string) ([]protocol.SessionLedgerFacet, error) {
		rows, err := s.db.Query("SELECT "+column+", COUNT(*) FROM sessions"+clause+
			" GROUP BY "+column+" ORDER BY "+column, args...)
		if err != nil {
			return nil, fmt.Errorf("count ledger %s: %w", column, err)
		}
		defer rows.Close()
		facets := []protocol.SessionLedgerFacet{}
		for rows.Next() {
			var value sql.NullString
			var total int
			if err := rows.Scan(&value, &total); err != nil {
				return nil, fmt.Errorf("count ledger %s: %w", column, err)
			}
			if !value.Valid || value.String == "" {
				continue
			}
			facets = append(facets, protocol.SessionLedgerFacet{Value: value.String, Count: total})
		}
		return facets, rows.Err()
	}

	repositories, err := count("repository")
	if err != nil {
		return nil, err
	}
	workspaces, err := count("workspace_id")
	if err != nil {
		return nil, err
	}
	return &protocol.SessionLedgerFacets{Repositories: repositories, Workspaces: workspaces}, nil
}

func (q SessionLedgerQuery) matches(entry protocol.SessionLedgerEntry) bool {
	if workspace := strings.TrimSpace(q.WorkspaceID); workspace != "" && entry.WorkspaceID != workspace {
		return false
	}
	if repository := strings.TrimSpace(q.Repository); repository != "" && protocol.Deref(entry.Repository) != repository {
		return false
	}
	return q.withinWindow(entry)
}

func (q SessionLedgerQuery) withinWindow(entry protocol.SessionLedgerEntry) bool {
	at := ledgerInstant(entry)
	if !q.Since.IsZero() && at < q.Since.UTC().Format(time.RFC3339Nano) {
		return false
	}
	if !q.Until.IsZero() && at >= q.Until.UTC().Format(time.RFC3339Nano) {
		return false
	}
	return true
}

func (s *Store) sessionLedgerMemory(query SessionLedgerQuery, limit int) (SessionLedgerPage, error) {
	scoped := make([]protocol.SessionLedgerEntry, 0, len(s.sessions)+len(s.sessionCloses))
	if query.Scope != SessionLedgerClosed {
		for _, session := range s.sessions {
			scoped = append(scoped, ledgerEntryFromSession(session, sessionCloseMark{}))
		}
	}
	if query.Scope == SessionLedgerClosed || query.Scope == SessionLedgerAll {
		for _, mark := range s.sessionCloses {
			scoped = append(scoped, ledgerEntryFromSession(mark.session, mark))
		}
	}

	windowed := scoped[:0]
	for _, entry := range scoped {
		if query.withinWindow(entry) {
			windowed = append(windowed, entry)
		}
	}

	entries := make([]protocol.SessionLedgerEntry, 0, len(windowed))
	for _, entry := range windowed {
		if query.matches(entry) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := ledgerInstant(entries[i]), ledgerInstant(entries[j])
		if left == right {
			return entries[i].ID > entries[j].ID
		}
		return left > right
	})

	if cursor := strings.TrimSpace(query.Before); cursor != "" {
		entry := s.ledgerEntryMemoryLocked(cursor)
		if entry == nil {
			return SessionLedgerPage{}, &ErrUnknownLedgerCursor{ID: cursor}
		}
		at := ledgerInstant(*entry)
		kept := entries[:0]
		for _, entry := range entries {
			instant := ledgerInstant(entry)
			if instant < at || (instant == at && entry.ID < cursor) {
				kept = append(kept, entry)
			}
		}
		entries = kept
	}

	matching := len(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	page := finishLedgerPage(SessionLedgerPage{Entries: entries}, matching)
	if query.Facets {
		page.Facets = ledgerFacetsMemory(windowed)
	}
	return page, nil
}

func ledgerFacetsMemory(entries []protocol.SessionLedgerEntry) *protocol.SessionLedgerFacets {
	repositories := map[string]int{}
	workspaces := map[string]int{}
	for _, entry := range entries {
		if repository := protocol.Deref(entry.Repository); repository != "" {
			repositories[repository]++
		}
		if entry.WorkspaceID != "" {
			workspaces[entry.WorkspaceID]++
		}
	}
	return &protocol.SessionLedgerFacets{
		Repositories: sortedFacets(repositories),
		Workspaces:   sortedFacets(workspaces),
	}
}

func sortedFacets(counts map[string]int) []protocol.SessionLedgerFacet {
	facets := make([]protocol.SessionLedgerFacet, 0, len(counts))
	for value, count := range counts {
		facets = append(facets, protocol.SessionLedgerFacet{Value: value, Count: count})
	}
	sort.Slice(facets, func(i, j int) bool { return facets[i].Value < facets[j].Value })
	return facets
}

// The in-memory mirror of the writers' closed_at predicate: closing takes the row
// out of s.sessions, so the maps keyed beside it stop taking writes with it.
func (s *Store) sessionIsLiveLocked(id string) bool {
	_, live := s.sessions[id]
	return live
}

func (s *Store) ledgerEntryMemoryLocked(id string) *protocol.SessionLedgerEntry {
	if mark, closed := s.sessionCloses[id]; closed {
		entry := ledgerEntryFromSession(mark.session, mark)
		return &entry
	}
	session := s.sessions[id]
	if session == nil {
		return nil
	}
	entry := ledgerEntryFromSession(session, sessionCloseMark{})
	return &entry
}

func finishLedgerPage(page SessionLedgerPage, matching int) SessionLedgerPage {
	page.Omitted = matching - len(page.Entries)
	if page.Omitted > 0 && len(page.Entries) > 0 {
		page.NextBefore = page.Entries[len(page.Entries)-1].ID
	}
	return page
}

func ledgerInstant(entry protocol.SessionLedgerEntry) string {
	if closedAt := protocol.Deref(entry.ClosedAt); closedAt != "" {
		return closedAt
	}
	return entry.LastSeen
}

// The closed session leaves s.sessions and lives here instead, so every
// in-memory reader and writer stops finding it exactly as SQL's predicate does.
type sessionCloseMark struct {
	At      string
	By      string
	Reason  string
	session *protocol.Session
}

func ledgerEntryFromSession(session *protocol.Session, mark sessionCloseMark) protocol.SessionLedgerEntry {
	entry := protocol.SessionLedgerEntry{
		ID:          session.ID,
		Label:       session.Label,
		Agent:       string(session.Agent),
		Directory:   session.Directory,
		WorkspaceID: session.WorkspaceID,
		Branch:      session.Branch,
		IsWorktree:  session.IsWorktree,
		MainRepo:    session.MainRepo,
		Repository:  session.Repository,
		State:       session.State,
		LastSeen:    session.LastSeen,
	}
	if mark.At != "" {
		entry.ClosedAt = protocol.Ptr(mark.At)
		entry.ClosedBy = protocol.Ptr(mark.By)
		if mark.Reason != "" {
			entry.CloseReason = protocol.Ptr(mark.Reason)
		}
	}
	return entry
}

type ledgerScanner interface {
	Scan(dest ...any) error
}

func scanLedgerEntry(row ledgerScanner) (protocol.SessionLedgerEntry, error) {
	var entry protocol.SessionLedgerEntry
	var isWorktree int
	var branch, mainRepo, repository, workspaceID sql.NullString
	var closedAt, closedBy, closeReason string

	err := row.Scan(
		&entry.ID,
		&entry.Label,
		&entry.Agent,
		&entry.Directory,
		&workspaceID,
		&branch,
		&isWorktree,
		&mainRepo,
		&repository,
		&entry.State,
		&entry.LastSeen,
		&closedAt,
		&closedBy,
		&closeReason,
	)
	if err != nil {
		return protocol.SessionLedgerEntry{}, err
	}
	if workspaceID.Valid {
		entry.WorkspaceID = workspaceID.String
	}
	if branch.Valid && branch.String != "" {
		entry.Branch = protocol.Ptr(branch.String)
	}
	if isWorktree == 1 {
		entry.IsWorktree = protocol.Ptr(true)
	}
	if mainRepo.Valid && mainRepo.String != "" {
		entry.MainRepo = protocol.Ptr(mainRepo.String)
	}
	if repository.Valid && repository.String != "" {
		entry.Repository = protocol.Ptr(repository.String)
	}
	if closedAt != "" {
		entry.ClosedAt = protocol.Ptr(closedAt)
		entry.ClosedBy = protocol.Ptr(closedBy)
		if closeReason != "" {
			entry.CloseReason = protocol.Ptr(closeReason)
		}
	}
	return entry, nil
}
