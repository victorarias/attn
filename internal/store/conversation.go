package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type SessionConversation struct {
	NativeID       string
	TranscriptPath string
}

func (s *Store) TransitionSessionConversation(sessionID, nativeID, transcriptPath string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, transcriptPath, true)
}

func (s *Store) TransitionSessionResumeID(sessionID, nativeID string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, "", false)
}

// Repeated transitions are no-ops. When the live session row is already gone the ticket
// mirror is still updated: ticket Resume captures this binding after close.
func (s *Store) transitionSessionConversation(sessionID, nativeID, transcriptPath string, pathRequired bool) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	nativeID = strings.TrimSpace(nativeID)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if sessionID == "" || nativeID == "" || (pathRequired && transcriptPath == "") {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin conversation transition: %w", err)
	}
	defer tx.Rollback()

	var current SessionConversation
	err = tx.QueryRow(`SELECT resume_session_id, transcript_path FROM sessions WHERE id = ?`, sessionID).Scan(
		&current.NativeID,
		&current.TranscriptPath,
	)
	if err == sql.ErrNoRows {
		if _, err := tx.Exec(
			`UPDATE tickets SET resume_session_id = ? WHERE assignee = ?`,
			nativeID,
			sessionID,
		); err != nil {
			return false, fmt.Errorf("mirror closed-session conversation binding for %s: %w", sessionID, err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit closed-session conversation binding for %s: %w", sessionID, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read conversation binding for session %s: %w", sessionID, err)
	}
	current.NativeID = strings.TrimSpace(current.NativeID)
	current.TranscriptPath = strings.TrimSpace(current.TranscriptPath)
	if pathRequired {
		if current.NativeID == nativeID && current.TranscriptPath == transcriptPath {
			return false, nil
		}
	} else {
		if current.NativeID == nativeID {
			return false, nil
		}
		transcriptPath = ""
	}

	query := `UPDATE sessions SET resume_session_id = ?, transcript_path = ? WHERE id = ? AND closed_at = ''`
	if current.NativeID != "" && current.NativeID != nativeID {
		query = `
			UPDATE sessions
			SET resume_session_id = ?, transcript_path = ?, activity = '', activity_at = '', activity_cursor = ''
			WHERE id = ? AND closed_at = ''
		`
	}
	if _, err := tx.Exec(query, nativeID, transcriptPath, sessionID); err != nil {
		return false, fmt.Errorf("update conversation binding for session %s: %w", sessionID, err)
	}
	if _, err := tx.Exec(
		`UPDATE tickets SET resume_session_id = ? WHERE assignee = ?`,
		nativeID,
		sessionID,
	); err != nil {
		return false, fmt.Errorf("mirror conversation binding for session %s: %w", sessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit conversation transition for session %s: %w", sessionID, err)
	}
	return true, nil
}

func (s *Store) GetSessionConversation(sessionID string) SessionConversation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return SessionConversation{}
	}

	var binding SessionConversation
	if err := s.db.QueryRow(
		`SELECT resume_session_id, transcript_path FROM sessions WHERE id = ?`,
		strings.TrimSpace(sessionID),
	).Scan(&binding.NativeID, &binding.TranscriptPath); err != nil {
		return SessionConversation{}
	}
	binding.NativeID = strings.TrimSpace(binding.NativeID)
	binding.TranscriptPath = strings.TrimSpace(binding.TranscriptPath)
	return binding
}
