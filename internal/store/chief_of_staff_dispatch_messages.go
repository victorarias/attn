package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func cloneDispatchMessage(message *protocol.DispatchMessage) *protocol.DispatchMessage {
	if message == nil {
		return nil
	}
	cloned := *message
	if message.ReadAt != nil {
		cloned.ReadAt = protocol.Ptr(protocol.Deref(message.ReadAt))
	}
	if message.AcknowledgedAt != nil {
		cloned.AcknowledgedAt = protocol.Ptr(protocol.Deref(message.AcknowledgedAt))
	}
	if message.Acknowledgement != nil {
		cloned.Acknowledgement = protocol.Ptr(protocol.Deref(message.Acknowledgement))
	}
	return &cloned
}

func (s *Store) AddDispatchMessage(message *protocol.DispatchMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if message == nil || strings.TrimSpace(message.ID) == "" {
		return fmt.Errorf("message id cannot be empty")
	}
	if strings.TrimSpace(message.DispatchID) == "" {
		return fmt.Errorf("dispatch id cannot be empty")
	}
	if strings.TrimSpace(message.SenderSessionID) == "" {
		return fmt.Errorf("sender session id cannot be empty")
	}
	if strings.TrimSpace(message.TargetSessionID) == "" {
		return fmt.Errorf("target session id cannot be empty")
	}
	if strings.TrimSpace(message.Content) == "" {
		return fmt.Errorf("content cannot be empty")
	}

	if s.db == nil {
		if s.dispatchMessages == nil {
			s.dispatchMessages = make(map[string]*protocol.DispatchMessage)
		}
		s.dispatchMessages[message.ID] = cloneDispatchMessage(message)
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO chief_of_staff_dispatch_messages (
			id, dispatch_id, sender_session_id, target_session_id, content,
			created_at, read_at, acknowledged_at, acknowledgement
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		message.ID,
		message.DispatchID,
		message.SenderSessionID,
		message.TargetSessionID,
		message.Content,
		message.CreatedAt,
		protocol.Deref(message.ReadAt),
		protocol.Deref(message.AcknowledgedAt),
		protocol.Deref(message.Acknowledgement),
	)
	if err != nil {
		return fmt.Errorf("insert dispatch message %s: %w", message.ID, err)
	}
	return nil
}

func (s *Store) GetDispatchMessage(id string) *protocol.DispatchMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if s.db == nil {
		return cloneDispatchMessage(s.dispatchMessages[id])
	}
	return scanDispatchMessage(s.db.QueryRow(`
		SELECT id, dispatch_id, sender_session_id, target_session_id, content,
			created_at, read_at, acknowledged_at, acknowledgement
		FROM chief_of_staff_dispatch_messages
		WHERE id = ?`,
		id,
	))
}

func (s *Store) ListDispatchMessages(dispatchID string, unreadOnly bool) ([]*protocol.DispatchMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return nil, fmt.Errorf("dispatch id cannot be empty")
	}
	if s.db == nil {
		result := make([]*protocol.DispatchMessage, 0)
		for _, message := range s.dispatchMessages {
			if message.DispatchID != dispatchID || (unreadOnly && message.ReadAt != nil) {
				continue
			}
			result = append(result, cloneDispatchMessage(message))
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].CreatedAt == result[j].CreatedAt {
				return result[i].ID < result[j].ID
			}
			return result[i].CreatedAt < result[j].CreatedAt
		})
		return result, nil
	}

	query := `
		SELECT id, dispatch_id, sender_session_id, target_session_id, content,
			created_at, read_at, acknowledged_at, acknowledgement
		FROM chief_of_staff_dispatch_messages
		WHERE dispatch_id = ?`
	if unreadOnly {
		query += " AND read_at = ''"
	}
	query += " ORDER BY created_at, id"
	rows, err := s.db.Query(query, dispatchID)
	if err != nil {
		return nil, fmt.Errorf("list dispatch messages for %s: %w", dispatchID, err)
	}
	defer rows.Close()

	var result []*protocol.DispatchMessage
	for rows.Next() {
		message, err := scanDispatchMessageResult(rows)
		if err != nil {
			return nil, fmt.Errorf("scan dispatch message for %s: %w", dispatchID, err)
		}
		result = append(result, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dispatch messages for %s: %w", dispatchID, err)
	}
	return result, nil
}

func (s *Store) CountUnreadDispatchMessages(dispatchID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return 0, fmt.Errorf("dispatch id cannot be empty")
	}
	if s.db == nil {
		count := 0
		for _, message := range s.dispatchMessages {
			if message.DispatchID == dispatchID && message.ReadAt == nil {
				count++
			}
		}
		return count, nil
	}
	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM chief_of_staff_dispatch_messages
		WHERE dispatch_id = ? AND read_at = ''`,
		dispatchID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unread dispatch messages for %s: %w", dispatchID, err)
	}
	return count, nil
}

func (s *Store) MarkDispatchMessageRead(id, dispatchID, targetSessionID string) (*protocol.DispatchMessage, error) {
	return s.updateDispatchMessage(id, dispatchID, targetSessionID, "", false)
}

func (s *Store) AcknowledgeDispatchMessage(
	id, dispatchID, targetSessionID, acknowledgement string,
) (*protocol.DispatchMessage, error) {
	return s.updateDispatchMessage(id, dispatchID, targetSessionID, acknowledgement, true)
}

func (s *Store) updateDispatchMessage(
	id, dispatchID, targetSessionID, acknowledgement string,
	acknowledge bool,
) (*protocol.DispatchMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	dispatchID = strings.TrimSpace(dispatchID)
	targetSessionID = strings.TrimSpace(targetSessionID)
	acknowledgement = strings.TrimSpace(acknowledgement)
	if id == "" {
		return nil, fmt.Errorf("message id cannot be empty")
	}
	if dispatchID == "" {
		return nil, fmt.Errorf("dispatch id cannot be empty")
	}
	if targetSessionID == "" {
		return nil, fmt.Errorf("target session id cannot be empty")
	}

	now := string(protocol.TimestampNow())
	if s.db == nil {
		message := cloneDispatchMessage(s.dispatchMessages[id])
		if message == nil {
			return nil, fmt.Errorf("message %s not found", id)
		}
		if message.DispatchID != dispatchID || message.TargetSessionID != targetSessionID {
			return nil, fmt.Errorf("message %s does not belong to dispatch %s for session %s", id, dispatchID, targetSessionID)
		}
		if message.ReadAt == nil {
			message.ReadAt = protocol.Ptr(now)
		}
		if acknowledge {
			message.AcknowledgedAt = protocol.Ptr(now)
			message.Acknowledgement = nil
			if acknowledgement != "" {
				message.Acknowledgement = protocol.Ptr(acknowledgement)
			}
		}
		s.dispatchMessages[id] = cloneDispatchMessage(message)
		return message, nil
	}

	message := scanDispatchMessage(s.db.QueryRow(`
		SELECT id, dispatch_id, sender_session_id, target_session_id, content,
			created_at, read_at, acknowledged_at, acknowledgement
		FROM chief_of_staff_dispatch_messages
		WHERE id = ?`,
		id,
	))
	if message == nil {
		return nil, fmt.Errorf("message %s not found", id)
	}
	if message.DispatchID != dispatchID || message.TargetSessionID != targetSessionID {
		return nil, fmt.Errorf("message %s does not belong to dispatch %s for session %s", id, dispatchID, targetSessionID)
	}

	readAt := protocol.Deref(message.ReadAt)
	if readAt == "" {
		readAt = now
	}
	acknowledgedAt := protocol.Deref(message.AcknowledgedAt)
	storedAcknowledgement := protocol.Deref(message.Acknowledgement)
	if acknowledge {
		acknowledgedAt = now
		storedAcknowledgement = acknowledgement
	}
	if _, err := s.db.Exec(`
		UPDATE chief_of_staff_dispatch_messages
		SET read_at = ?, acknowledged_at = ?, acknowledgement = ?
		WHERE id = ?`,
		readAt,
		acknowledgedAt,
		storedAcknowledgement,
		id,
	); err != nil {
		return nil, fmt.Errorf("update dispatch message %s: %w", id, err)
	}
	message.ReadAt = protocol.Ptr(readAt)
	if acknowledgedAt != "" {
		message.AcknowledgedAt = protocol.Ptr(acknowledgedAt)
	}
	message.Acknowledgement = nil
	if storedAcknowledgement != "" {
		message.Acknowledgement = protocol.Ptr(storedAcknowledgement)
	}
	return message, nil
}

type dispatchMessageScanner interface {
	Scan(dest ...interface{}) error
}

func scanDispatchMessage(scanner dispatchMessageScanner) *protocol.DispatchMessage {
	message, _ := scanDispatchMessageResult(scanner)
	return message
}

func scanDispatchMessageResult(scanner dispatchMessageScanner) (*protocol.DispatchMessage, error) {
	var (
		message                                 protocol.DispatchMessage
		readAt, acknowledgedAt, acknowledgement sql.NullString
	)
	if err := scanner.Scan(
		&message.ID,
		&message.DispatchID,
		&message.SenderSessionID,
		&message.TargetSessionID,
		&message.Content,
		&message.CreatedAt,
		&readAt,
		&acknowledgedAt,
		&acknowledgement,
	); err != nil {
		return nil, err
	}
	if readAt.Valid && readAt.String != "" {
		message.ReadAt = protocol.Ptr(readAt.String)
	}
	if acknowledgedAt.Valid && acknowledgedAt.String != "" {
		message.AcknowledgedAt = protocol.Ptr(acknowledgedAt.String)
	}
	if acknowledgement.Valid && acknowledgement.String != "" {
		message.Acknowledgement = protocol.Ptr(acknowledgement.String)
	}
	return &message, nil
}
