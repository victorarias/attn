package store

import (
	"database/sql"
	"log"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

type SessionActivity struct {
	Line   string
	At     time.Time
	Cursor string
}

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

// The three columns are absent from the session upsert, so this is their only writer: a respawn
// or a re-add cannot clear a line. An empty line clears the activity and the cursor with it.
func (s *Store) UpdateSessionActivity(id, line string, at time.Time, cursor string) bool {
	return s.updateSessionActivity(id, nil, line, at, cursor)
}

// The check and write share the store lock with TransitionSessionConversation, so an executor straddling a transition cannot restore the cleared old state.
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

	query := `UPDATE sessions SET activity = ?, activity_at = ?, activity_cursor = ? WHERE id = ? AND closed_at = ''`
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

// A separate door because an empty line means "forget this line" in UpdateSessionActivity and "nothing generated yet" here.
func (s *Store) SetSessionActivityCursor(id, cursor string) bool {
	return s.setSessionActivityCursor(id, nil, cursor)
}

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

	query := `UPDATE sessions SET activity_cursor = ? WHERE id = ? AND closed_at = ''`
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

// Both present or both absent, never a line without the stamp that lets a client age it out.
func applyActivity(session *protocol.Session, line, stamp string) {
	if line == "" || stamp == "" {
		session.Activity, session.ActivityAt = nil, nil
		return
	}
	session.Activity = protocol.Ptr(line)
	session.ActivityAt = protocol.Ptr(stamp)
}

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
