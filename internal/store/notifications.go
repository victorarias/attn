package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SQLite persistence for the global notifications feed (see
// docs/plans/2026-07-02-bg-task-notifications.md). A notification is a durable,
// read/unread record surfaced in the app's notifications panel; its producer is
// the durable task runner, which emits one when a background task reaches terminal
// `dead` (retries exhausted). NotificationRecord is a store-local row type — the
// daemon owns the mapping to the protocol shape — keeping this package a leaf.
//
// read_at is persisted as '' while unread and as a timestamp once read. Timestamps
// reuse the tasks table's RFC3339Nano encoding and parseTaskTime decoder (same
// package), so a blank/garbage value decodes to the zero time.

// NotificationRecord is one durable notification row. ReadAt is the zero time
// while unread; a non-zero ReadAt marks it read.
type NotificationRecord struct {
	ID         string
	Kind       string // e.g. "task_failed" (extensible)
	Title      string
	Body       string
	Detail     string
	SourceKind string // e.g. "task"
	SourceID   string // e.g. the task id (Retry deep-links here)
	CreatedAt  time.Time
	ReadAt     time.Time // zero == unread
}

// AddNotification inserts a new notification, generating its id and stamping
// CreatedAt, and returns the stored record. Notifications are append-only: each
// actionable event (a task crossing into dead) is its own row, never coalesced
// with a prior one — a retried-then-redead task is a fresh event worth surfacing.
func (s *Store) AddNotification(rec NotificationRecord, now time.Time) (NotificationRecord, error) {
	if s.db == nil {
		return NotificationRecord{}, fmt.Errorf("store: no database")
	}
	rec.ID = uuid.NewString()
	rec.CreatedAt = now.UTC()
	rec.ReadAt = time.Time{}
	_, err := s.db.Exec(
		`INSERT INTO notifications (id, kind, title, body, detail, source_kind, source_id, created_at, read_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '')`,
		rec.ID, rec.Kind, rec.Title, rec.Body, rec.Detail, rec.SourceKind, rec.SourceID,
		rec.CreatedAt.Format(notificationTimeFormat),
	)
	if err != nil {
		return NotificationRecord{}, fmt.Errorf("store: add notification: %w", err)
	}
	return rec, nil
}

// ListNotifications returns every notification, newest-created first.
func (s *Store) ListNotifications() ([]NotificationRecord, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT id, kind, title, body, detail, source_kind, source_id, created_at, read_at
		 FROM notifications ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list notifications: %w", err)
	}
	defer rows.Close()
	var out []NotificationRecord
	for rows.Next() {
		rec, err := scanNotificationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan notification: %w", err)
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// UnreadNotificationCount returns how many notifications are unread (read_at ”).
// It drives the sidebar's unread badge, so it is a cheap dedicated COUNT rather
// than a full list + filter.
func (s *Store) UnreadNotificationCount() (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE read_at = ''`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: unread notification count: %w", err)
	}
	return n, nil
}

// MarkNotificationRead marks one notification read. Idempotent: the WHERE read_at
// = ” guard keeps the first read time if it is already read, and a missing id is
// not an error.
func (s *Store) MarkNotificationRead(id string, now time.Time) error {
	if s.db == nil {
		return fmt.Errorf("store: no database")
	}
	if _, err := s.db.Exec(
		`UPDATE notifications SET read_at = ? WHERE id = ? AND read_at = ''`,
		now.UTC().Format(notificationTimeFormat), id); err != nil {
		return fmt.Errorf("store: mark notification read %s: %w", id, err)
	}
	return nil
}

// MarkAllNotificationsRead marks every unread notification read, returning how
// many were flipped.
func (s *Store) MarkAllNotificationsRead(now time.Time) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	res, err := s.db.Exec(
		`UPDATE notifications SET read_at = ? WHERE read_at = ''`,
		now.UTC().Format(notificationTimeFormat))
	if err != nil {
		return 0, fmt.Errorf("store: mark all notifications read: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// notificationTimeFormat matches the tasks table so both surfaces round-trip
// timestamps identically.
const notificationTimeFormat = time.RFC3339Nano

func scanNotificationRow(sc rowScanner) (*NotificationRecord, error) {
	var (
		rec                 NotificationRecord
		createdStr, readStr string
	)
	if err := sc.Scan(&rec.ID, &rec.Kind, &rec.Title, &rec.Body, &rec.Detail,
		&rec.SourceKind, &rec.SourceID, &createdStr, &readStr); err != nil {
		return nil, err
	}
	rec.CreatedAt = parseTaskTime(createdStr)
	rec.ReadAt = parseTaskTime(readStr)
	return &rec, nil
}
