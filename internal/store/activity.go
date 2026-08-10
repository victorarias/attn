package store

import (
	"database/sql"
	"log"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

// SessionActivity is what the generator needs before deciding whether to run at
// all: the line it wrote last time, and how far into the transcript it had read
// when it wrote it.
//
// Cursor is the load-bearing field. A session whose transcript has not moved
// past it has written nothing new, so its existing line is still true and the
// run is skipped — which is what keeps blocked and finished sessions free even
// while the dashboard is open.
type SessionActivity struct {
	Line   string
	At     time.Time
	Cursor string
}

// GetSessionActivity reads a session's activity line and cursor. A session that
// does not exist, and one that has never had a line generated, both come back
// zero — the caller treats them the same way, as a cold start.
func (s *Store) GetSessionActivity(id string) SessionActivity {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		session, ok := s.sessions[id]
		if !ok {
			return SessionActivity{}
		}
		return SessionActivity{
			Line:   protocol.Deref(session.Activity),
			At:     parseActivityStamp(protocol.Deref(session.ActivityAt)),
			Cursor: s.activityCursors[id],
		}
	}

	var line, at, cursor string
	err := s.db.QueryRow(
		`SELECT activity, activity_at, activity_cursor FROM sessions WHERE id = ?`, id,
	).Scan(&line, &at, &cursor)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[store] GetSessionActivity: read failed for session %s: %v", id, err)
		}
		return SessionActivity{}
	}
	return SessionActivity{Line: line, At: parseActivityStamp(at), Cursor: cursor}
}

// UpdateSessionActivity records a freshly generated line and the cursor it was
// generated through. It returns true when a session was updated.
//
// The three columns are absent from the session upsert, so this is their only
// writer: a respawn, a state change, or a re-add cannot clear a line — the same
// arrangement the pin has, and for the same reason.
//
// An empty line clears the session's activity. That is the "this line is wrong,
// forget it" path, and it deliberately clears the cursor too, so the next run
// re-seeds from head rather than reading a delta against a line that is gone.
func (s *Store) UpdateSessionActivity(id, line string, at time.Time, cursor string) bool {
	return s.updateSessionActivity(id, nil, line, at, cursor)
}

// UpdateSessionActivityForConversation records activity only while the session
// is still bound to the conversation that produced it. The check and write
// share the store lock with TransitionSessionConversation, so an executor that
// straddles a conversation transition cannot restore the cleared old state.
func (s *Store) UpdateSessionActivityForConversation(id, resumeID, line string, at time.Time, cursor string) bool {
	resumeID = strings.TrimSpace(resumeID)
	return s.updateSessionActivity(id, &resumeID, line, at, cursor)
}

func (s *Store) updateSessionActivity(id string, resumeID *string, line string, at time.Time, cursor string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if line == "" {
		at, cursor = time.Time{}, ""
	}
	stamp := ""
	if !at.IsZero() {
		stamp = at.UTC().Format(docstore.TimeFormat)
	}

	if s.db == nil {
		if resumeID != nil && *resumeID != "" {
			return false
		}
		session, ok := s.sessions[id]
		if !ok {
			return false
		}
		applyActivity(session, line, stamp)
		if s.activityCursors == nil {
			s.activityCursors = make(map[string]string)
		}
		if cursor == "" {
			delete(s.activityCursors, id)
		} else {
			s.activityCursors[id] = cursor
		}
		return true
	}

	query := `UPDATE sessions SET activity = ?, activity_at = ?, activity_cursor = ? WHERE id = ?`
	args := []any{line, stamp, cursor, id}
	if resumeID != nil {
		query += ` AND resume_session_id = ?`
		args = append(args, *resumeID)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		log.Printf("[store] UpdateSessionActivity: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// SetSessionActivityCursor moves the read position without touching the line.
//
// It is a separate door from UpdateSessionActivity because the two mean opposite
// things when the line is empty: there, an empty line means "forget this line",
// while here it means "nothing has been generated yet" — the cold start, whose
// whole point is to record how far we have read so the first real line is about
// the present rather than the session's whole history.
func (s *Store) SetSessionActivityCursor(id, cursor string) bool {
	return s.setSessionActivityCursor(id, nil, cursor)
}

// SetSessionActivityCursorForConversation advances the cursor only while the
// session still names the conversation whose transcript supplied it.
func (s *Store) SetSessionActivityCursorForConversation(id, resumeID, cursor string) bool {
	resumeID = strings.TrimSpace(resumeID)
	return s.setSessionActivityCursor(id, &resumeID, cursor)
}

func (s *Store) setSessionActivityCursor(id string, resumeID *string, cursor string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		if resumeID != nil && *resumeID != "" {
			return false
		}
		if _, ok := s.sessions[id]; !ok {
			return false
		}
		if s.activityCursors == nil {
			s.activityCursors = make(map[string]string)
		}
		if cursor == "" {
			delete(s.activityCursors, id)
		} else {
			s.activityCursors[id] = cursor
		}
		return true
	}

	query := `UPDATE sessions SET activity_cursor = ? WHERE id = ?`
	args := []any{cursor, id}
	if resumeID != nil {
		query += ` AND resume_session_id = ?`
		args = append(args, *resumeID)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		log.Printf("[store] SetSessionActivityCursor: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}

// applyActivity puts the pair on a session as the wire carries it: both present
// or both absent, never a line without the stamp that lets a client age it out.
func applyActivity(session *protocol.Session, line, stamp string) {
	if line == "" || stamp == "" {
		session.Activity, session.ActivityAt = nil, nil
		return
	}
	session.Activity = protocol.Ptr(line)
	session.ActivityAt = protocol.Ptr(stamp)
}

// parseActivityStamp decodes a stored stamp, tolerating every RFC3339 form
// docstore.ParseTime accepts. An undecodable stamp yields the zero time rather
// than an error: the line is still worth showing, and a caller that cannot age
// it treats it as old, which is the safe direction.
func parseActivityStamp(stamp string) time.Time {
	if stamp == "" {
		return time.Time{}
	}
	parsed, err := docstore.ParseTime(stamp)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
