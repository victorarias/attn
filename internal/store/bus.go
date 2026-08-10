package store

import (
	"database/sql"
	"strings"
	"time"
)

// Durable event bus substrate (migration 84): an append-only log whose
// AUTOINCREMENT seq doubles as the cursor space, plus a registry of named
// consumers. Delivery semantics live in internal/bus, reached through an
// interface so neither package imports the other.

// BusEvent is one fact on the log. Payload is opaque JSON (empty when the
// subject says everything); Source names the publisher for diagnosis.
type BusEvent struct {
	Seq       int64
	Name      string
	Subject   string
	Payload   string
	Source    string
	CreatedAt time.Time
}

// BusConsumer is a durable consumer's registration and position. Enabled=false
// is the kill switch: not delivered to, and deliberately not pinning retention.
type BusConsumer struct {
	Name      string
	Cursor    int64
	Filter    string
	Enabled   bool
	UpdatedAt time.Time
}

// AppendBusEvent appends a fact and returns its seq. No dedup: two identical
// facts in a row are a real occurrence, not a retry.
func (s *Store) AppendBusEvent(e BusEvent, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	return appendBusEventWith(s.db, e, now)
}

// appendBusEventWith takes whatever runs the statement: a composite write (see
// CommitDocumentWrite) passes its transaction so fact and change are one commit.
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

// BusEventsSince returns up to limit events with seq greater than cursor, in
// seq order; filtering is the caller's job.
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

// BusBounds returns the lowest and highest seq in the log (both 0 when empty);
// a cursor below the low bound has missed events that no longer exist.
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
// cursor and enabled bit: startup must not rewind or silently re-enable one.
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

// SetBusConsumerCursor persists a consumer's position (the delivery hot path).
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

// SetBusConsumerEnabled flips the kill switch for a consumer.
func (s *Store) SetBusConsumerEnabled(name string, enabled bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	flag := 0
	if enabled {
		flag = 1
	}
	_, err := s.db.Exec(`
		UPDATE bus_consumers SET enabled = ?, updated_at = ? WHERE name = ?
	`, flag, formatTicketTime(now), name)
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

// TrimBusEvents deletes events older than cutoff that every ENABLED consumer
// has passed. Disabled consumers are excluded from the floor on purpose: a
// killed consumer must not pin the log; internal/bus resumes it at head.
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

// CompactBusEvents keeps only the newest fact per subject among the named
// names, at or below floor (the cursor floor: every enabled consumer has read
// those rows, so removal costs no delivery and punches no holes above the
// floor). Which names are compactable is internal/bus's call. An empty name
// list compacts nothing, not everything.
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
	// The correlated MAX walks idx_bus_events_subject (subject, seq).
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

// BusProducer is one fact class's contribution to the log: how many events it
// holds, what they weigh, how many distinct entities they describe, and how many
// landed inside each cutoff BusProducers was asked about.
type BusProducer struct {
	Name     string
	Events   int64
	Bytes    int64
	Subjects int64
	// Recent is aligned with the cutoffs passed to BusProducers, one count per
	// cutoff, each counting events at or after it.
	Recent []int64
}

// BusProducers reports every fact class on the log with its totals and its
// counts inside each cutoff, loudest first. Which windows are worth asking about
// is internal/bus's call, so this takes the cutoffs rather than naming them.
//
// One pass over bus_events answers all of it, including the row count and bytes
// BusLogSize would scan for separately — the aggregate subsumes it rather than
// paying for the table twice. Measured on a copy of production: 209ms at 945k
// rows (~0.22us/row), against 106ms for BusLogSize alone. That is an on-demand
// cost, which is why nothing polls it on a timer.
func (s *Store) BusProducers(cutoffs []time.Time) ([]BusProducer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	var (
		columns strings.Builder
		args    []any
	)
	for _, c := range cutoffs {
		columns.WriteString(", COALESCE(SUM(created_at >= ?), 0)")
		args = append(args, formatTicketTime(c))
	}
	rows, err := s.db.Query(`
		SELECT name,
		       COUNT(*),
		       COALESCE(SUM(LENGTH(name) + LENGTH(subject) + LENGTH(payload) + LENGTH(source) + LENGTH(created_at)), 0),
		       COUNT(DISTINCT subject)`+columns.String()+`
		FROM bus_events
		GROUP BY name
		ORDER BY COUNT(*) DESC, name ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BusProducer
	for rows.Next() {
		p := BusProducer{Recent: make([]int64, len(cutoffs))}
		dest := []any{&p.Name, &p.Events, &p.Bytes, &p.Subjects}
		for i := range p.Recent {
			dest = append(dest, &p.Recent[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// BusEventTimeAt returns the timestamp of the first event at or above seq, and
// whether one exists. Both callers ask an age question — how old the log's tail
// is, and how long a consumer's oldest unread event has been waiting — and both
// resolve through the seq primary key, so neither scans.
func (s *Store) BusEventTimeAt(seq int64) (time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return time.Time{}, false, nil
	}
	var createdAt string
	err := s.db.QueryRow(`
		SELECT created_at FROM bus_events WHERE seq >= ? ORDER BY seq ASC LIMIT 1
	`, seq).Scan(&createdAt)
	switch err {
	case nil:
		return parseTicketTime(createdAt), true, nil
	case sql.ErrNoRows:
		return time.Time{}, false, nil
	default:
		return time.Time{}, false, err
	}
}

// BusLogSize reports the log's row count and event-text bytes. Bytes measures
// the rows themselves, not the database file — SQLite pages are shared.
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
