package store

import (
	"database/sql"
	"time"
)

// TicketDeliveryAttention is the one durable piece of notification scheduling
// state. Event rows remain the batch and ticket_event_cursors remain the read
// acknowledgement; this records when an observer was last interrupted and the
// newest event that delivery covered.
type TicketDeliveryAttention struct {
	ObserverKey         string
	LastAttentionAt     time.Time
	DeliveredThroughSeq int64
}

// TicketDeliveryAttention returns the observer's most recent non-empty ticket
// delivery. A missing row means the observer has no delivery history.
func (s *Store) TicketDeliveryAttention(observerKey string) (TicketDeliveryAttention, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || observerKey == "" {
		return TicketDeliveryAttention{}, false, nil
	}
	var raw string
	var deliveredThroughSeq int64
	err := s.db.QueryRow(`SELECT last_attention_at, delivered_through_seq FROM ticket_delivery_attention WHERE observer_key = ?`, observerKey).Scan(&raw, &deliveredThroughSeq)
	if err == sql.ErrNoRows {
		return TicketDeliveryAttention{}, false, nil
	}
	if err != nil {
		return TicketDeliveryAttention{}, false, err
	}
	return TicketDeliveryAttention{ObserverKey: observerKey, LastAttentionAt: parseTicketTime(raw), DeliveredThroughSeq: deliveredThroughSeq}, true, nil
}

// SetTicketDeliveryAttention advances the observer's interruption clock. The
// value is monotonic so concurrent successful reads cannot move it backwards.
func (s *Store) SetTicketDeliveryAttention(observerKey string, at time.Time) error {
	return s.SetTicketDeliveryAttentionThrough(observerKey, at, 0)
}

// SetTicketDeliveryAttentionThrough advances the interruption clock and the
// newest event covered by that delivery. Both values are monotonic.
func (s *Store) SetTicketDeliveryAttentionThrough(observerKey string, at time.Time, deliveredThroughSeq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || observerKey == "" {
		return nil
	}
	return setTicketDeliveryAttentionTx(s.db, observerKey, at, deliveredThroughSeq)
}

func setTicketDeliveryAttentionTx(ex ticketExecer, observerKey string, at time.Time, deliveredThroughSeq int64) error {
	_, err := ex.Exec(`
		INSERT INTO ticket_delivery_attention (observer_key, last_attention_at, delivered_through_seq)
		VALUES (?, ?, ?)
		ON CONFLICT(observer_key) DO UPDATE SET
			last_attention_at = MAX(last_attention_at, excluded.last_attention_at),
			delivered_through_seq = MAX(delivered_through_seq, excluded.delivered_through_seq)
	`, observerKey, formatTicketTime(at), deliveredThroughSeq)
	return err
}
