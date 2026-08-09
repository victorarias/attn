package store

import (
	"database/sql"
	"strings"
	"time"
)

// The durable event bus substrate (migration 84). This file is only persistence:
// an append-only log whose AUTOINCREMENT seq doubles as the cursor space, plus a
// registry of named consumers holding a position in that space. The delivery
// semantics — ordering, at-least-once, filters, retention window — live in
// internal/bus, which reaches this through an interface so neither package
// depends on the other (the daemon adapts them, as it does for internal/jobs).
//
// The shape is lifted from the ticket event log (migration 56), which proved it:
// a monotonic seq is a cursor space, and a consumer that was down catches up by
// reading forward from its own bookmark. The difference is scope. Ticket cursors
// are per-(identity, ticket) because "unread" is a per-ticket question; bus
// cursors are one per consumer because a consumer is a program, not a reader.

// BusEvent is one fact on the log. Payload is opaque JSON (the empty string when
// a fact carries nothing beyond its subject). Source names the publisher for
// diagnosis, not for routing.
type BusEvent struct {
	Seq       int64
	Name      string
	Subject   string
	Payload   string
	Source    string
	CreatedAt time.Time
}

// BusConsumer is a durable consumer's registration and position. Filter is the
// consumer's subscription expression, stored so `bus status` can report what a
// consumer is watching without the consumer being live. Enabled=false is the kill
// switch: a disabled consumer is not delivered to, and — deliberately — does not
// pin the retention window.
type BusConsumer struct {
	Name      string
	Cursor    int64
	Filter    string
	Enabled   bool
	UpdatedAt time.Time
}

// AppendBusEvent appends a fact and returns its seq.
//
// There is no dedup here, unlike the ticket event log. Ticket events dedup
// against the previous event because a retried mutation must not double-post a
// comment; bus facts are emitted by code that already decided something changed,
// and two identical facts in a row are a real occurrence (a session entering the
// same state twice), not a retry.
func (s *Store) AppendBusEvent(e BusEvent, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	return appendBusEventWith(s.db, e, now)
}

// appendBusEventWith is the append itself, taking whatever runs the statement.
// A composite write (see CommitDocumentWrite) passes its transaction so the fact
// and the change it describes are one commit; AppendBusEvent passes the database
// and gets SQLite's implicit single-statement transaction.
func appendBusEventWith(x execer, e BusEvent, now time.Time) (int64, error) {
	res, err := x.Exec(`
		INSERT INTO bus_events (name, subject, payload, source, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, e.Name, e.Subject, e.Payload, e.Source, formatTicketTime(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BusEventsSince returns up to limit events with seq greater than cursor, in seq
// order. Filtering is the caller's job: a consumer reads the raw forward stream
// and advances its cursor past events it does not want, which keeps the cursor
// monotone and the query free of pattern matching.
func (s *Store) BusEventsSince(cursor int64, limit int) ([]BusEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT seq, name, subject, payload, source, created_at
		FROM bus_events WHERE seq > ? ORDER BY seq ASC LIMIT ?
	`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []BusEvent
	for rows.Next() {
		var (
			e         BusEvent
			createdAt string
		)
		if err := rows.Scan(&e.Seq, &e.Name, &e.Subject, &e.Payload, &e.Source, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTicketTime(createdAt)
		events = append(events, e)
	}
	return events, rows.Err()
}

// BusBounds returns the lowest and highest seq currently in the log; both are 0
// when the log is empty. The low bound is what makes a trimmed-past cursor
// detectable: a consumer whose cursor sits below it has missed events that no
// longer exist.
func (s *Store) BusBounds() (earliest, head int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, 0, nil
	}
	var lo, hi sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(seq), MAX(seq) FROM bus_events`).Scan(&lo, &hi); err != nil {
		return 0, 0, err
	}
	return lo.Int64, hi.Int64, nil
}

// GetBusConsumer loads a consumer registration by name.
func (s *Store) GetBusConsumer(name string) (BusConsumer, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return BusConsumer{}, false, nil
	}
	var (
		c         BusConsumer
		enabled   int
		updatedAt string
	)
	err := s.db.QueryRow(`
		SELECT name, cursor, filter, enabled, updated_at FROM bus_consumers WHERE name = ?
	`, name).Scan(&c.Name, &c.Cursor, &c.Filter, &enabled, &updatedAt)
	switch err {
	case nil:
		c.Enabled = enabled != 0
		c.UpdatedAt = parseTicketTime(updatedAt)
		return c, true, nil
	case sql.ErrNoRows:
		return BusConsumer{}, false, nil
	default:
		return BusConsumer{}, false, err
	}
}

// SaveBusConsumer creates or updates a registration. An existing row keeps its
// cursor and enabled bit: re-registering at startup must not rewind a consumer's
// position, and must not silently re-enable one the operator killed.
func (s *Store) SaveBusConsumer(c BusConsumer, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO bus_consumers (name, cursor, filter, enabled, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET filter = excluded.filter, updated_at = excluded.updated_at
	`, c.Name, c.Cursor, c.Filter, enabled, formatTicketTime(now))
	return err
}

// SetBusConsumerCursor persists a consumer's position. This is the write on the
// delivery hot path, so it touches only the two columns it owns.
func (s *Store) SetBusConsumerCursor(name string, cursor int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		UPDATE bus_consumers SET cursor = ?, updated_at = ? WHERE name = ?
	`, cursor, formatTicketTime(now), name)
	return err
}

// SetBusConsumerEnabled flips the kill switch for a consumer, and reports
// whether there was a row to flip.
//
// The report is what a caller checks a moment after reading the registration:
// between the read and this write the consumer may have been unregistered, and
// an UPDATE that matches nothing is indistinguishable from a successful flip
// without it. A caller that answers "disabled" for a consumer that no longer
// exists has told its user something untrue.
func (s *Store) SetBusConsumerEnabled(name string, enabled bool, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	flag := 0
	if enabled {
		flag = 1
	}
	res, err := s.db.Exec(`
		UPDATE bus_consumers SET enabled = ?, updated_at = ? WHERE name = ?
	`, flag, formatTicketTime(now), name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteBusConsumer removes a registration. Deleting a row that is not there is
// success, not an error: the caller is an uninstall path, and an uninstall that
// fails the second time it runs is a worse surface than one that says nothing.
//
// An abandoned row is not harmless. While it exists and is enabled it holds the
// cursor floor down, so retention and compaction cannot pass it — forever, for a
// consumer nobody serves. Deleting the row is what ends that.
func (s *Store) DeleteBusConsumer(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM bus_consumers WHERE name = ?`, name)
	return err
}

// ListBusConsumers returns every registration, by name.
func (s *Store) ListBusConsumers() ([]BusConsumer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT name, cursor, filter, enabled, updated_at FROM bus_consumers ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BusConsumer
	for rows.Next() {
		var (
			c         BusConsumer
			enabled   int
			updatedAt string
		)
		if err := rows.Scan(&c.Name, &c.Cursor, &c.Filter, &enabled, &updatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		c.UpdatedAt = parseTicketTime(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

// TrimBusEvents deletes events older than cutoff that every ENABLED consumer has
// already passed, and reports how many rows went. Both conditions must hold: the
// age window bounds growth, and the cursor floor keeps a live-but-lagging
// consumer from losing events it has not read.
//
// Disabled consumers are excluded from the floor on purpose. A killed app
// must not pin the log indefinitely; when it comes back below the floor,
// internal/bus resumes it at head and logs the gap.
func (s *Store) TrimBusEvents(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	res, err := s.db.Exec(`
		DELETE FROM bus_events
		WHERE created_at < ?
		  AND seq <= COALESCE(
		      (SELECT MIN(cursor) FROM bus_consumers WHERE enabled = 1),
		      (SELECT COALESCE(MAX(seq), 0) FROM bus_events)
		  )
	`, formatTicketTime(cutoff))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// CompactBusEvents keeps only the newest fact per subject among the named ones,
// and only below floor. It reports how many rows went.
//
// Which names may be compacted is a semantic question and is answered in
// internal/bus; this is the SQL, the same split trimming uses. A compactable
// name is one whose facts are pure invalidations — five of them about one
// subject carry no more information than the newest, because the state itself
// lives in the store and every consumer reads it from there.
//
// floor is the caller's cursor floor and is what makes this safe: a row at or
// below it has been read by every enabled consumer, so removing it cannot cost
// anyone a delivery, and reconcileGap's "below the earliest surviving seq means
// trimmed" assumption stays true because no holes are punched above the floor.
//
// An empty name list is not "compact everything": it compacts nothing, because
// a caller that named nothing asked for nothing.
func (s *Store) CompactBusEvents(names []string, floor int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil || len(names) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, 0, len(names)*2+1)
	for _, n := range names {
		args = append(args, n)
	}
	args = append(args, floor)
	for _, n := range names {
		args = append(args, n)
	}
	// The correlated MAX is an index walk over idx_bus_events_subject (subject,
	// seq): for each candidate row, the newest fact about the same subject is the
	// last entry of that subject's index range.
	res, err := s.db.Exec(`
		DELETE FROM bus_events
		WHERE name IN (`+placeholders+`)
		  AND seq <= ?
		  AND seq < (
		      SELECT MAX(newer.seq) FROM bus_events AS newer
		      WHERE newer.subject = bus_events.subject
		        AND newer.name IN (`+placeholders+`)
		  )
	`, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// BusLogSize reports how many facts the log holds and how many bytes of event
// text they carry.
//
// Bytes is the weight of the rows themselves — name, subject, payload, source
// and stamp — not the size of the database file: SQLite pages are shared with
// every other table, so a file size would answer a different question than "is
// the log outgrowing the data it describes". That is the question `attn bus
// status` asks, and the one fact-class compaction exists to keep answerable.
func (s *Store) BusLogSize() (rows int64, bytes int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, 0, nil
	}
	err = s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(LENGTH(name) + LENGTH(subject) + LENGTH(payload) + LENGTH(source) + LENGTH(created_at)), 0)
		FROM bus_events
	`).Scan(&rows, &bytes)
	if err != nil {
		return 0, 0, err
	}
	return rows, bytes, nil
}
