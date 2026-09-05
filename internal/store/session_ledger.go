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

// Before is the id of the last row of the previous page; this page starts after it.
type SessionLedgerQuery struct {
	Scope  SessionLedgerScope
	Limit  int
	Before string
}

type SessionLedgerPage struct {
	Entries []protocol.SessionLedgerEntry
	// Omitted counts the rows this page left out, all older than its last entry.
	Omitted    int
	NextBefore string
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

// ReopenSession returns false when the id names no closed session.
func (s *Store) ReopenSession(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		mark, closed := s.sessionCloses[id]
		if !closed {
			return false, nil
		}
		s.sessions[id] = mark.session
		delete(s.sessionCloses, id)
		return true, nil
	}

	result, err := s.db.Exec(`UPDATE sessions SET closed_at = '', closed_by = '', close_reason = ''
		WHERE id = ? AND closed_at <> ''`, id)
	if err != nil {
		return false, fmt.Errorf("reopen session %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("reopen session %s: %w", id, err)
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
	state, last_seen, closed_at, closed_by, close_reason`

const ledgerAt = `CASE WHEN closed_at <> '' THEN closed_at ELSE last_seen END`

func (s *Store) sessionLedgerDB(query SessionLedgerQuery, limit int) (SessionLedgerPage, error) {
	where := []string{}
	switch query.Scope {
	case SessionLedgerClosed:
		where = append(where, "closed_at <> ''")
	case SessionLedgerAll:
	default:
		where = append(where, "closed_at = ''")
	}

	var args []any
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

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

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
	return finishLedgerPage(page, matching), nil
}

func (s *Store) sessionLedgerMemory(query SessionLedgerQuery, limit int) (SessionLedgerPage, error) {
	entries := make([]protocol.SessionLedgerEntry, 0, len(s.sessions)+len(s.sessionCloses))
	if query.Scope != SessionLedgerClosed {
		for _, session := range s.sessions {
			entries = append(entries, ledgerEntryFromSession(session, sessionCloseMark{}))
		}
	}
	if query.Scope == SessionLedgerClosed || query.Scope == SessionLedgerAll {
		for _, mark := range s.sessionCloses {
			entries = append(entries, ledgerEntryFromSession(mark.session, mark))
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
	return finishLedgerPage(SessionLedgerPage{Entries: entries}, matching), nil
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
	var branch, mainRepo, workspaceID sql.NullString
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
	if closedAt != "" {
		entry.ClosedAt = protocol.Ptr(closedAt)
		entry.ClosedBy = protocol.Ptr(closedBy)
		if closeReason != "" {
			entry.CloseReason = protocol.Ptr(closeReason)
		}
	}
	return entry, nil
}
