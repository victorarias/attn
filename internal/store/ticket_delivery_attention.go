package store

import (
	"database/sql"
	"time"
)

// TicketDeliveryAttention is the one durable piece of notification scheduling
// state. Event rows remain the batch and ticket_event_cursors remain the read
// acknowledgement; this records only when an observer was last interrupted.
type TicketDeliveryAttention struct {
	ObserverKey     string
	LastAttentionAt time.Time
}

// TicketDeliveryAttention returns the observer's most recent non-empty ticket
// delivery. A missing row means the observer has no buffered-delivery history.
func (s *Store) TicketDeliveryAttention(observerKey string) (TicketDeliveryAttention, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || observerKey == "" {
		return TicketDeliveryAttention{}, false, nil
	}
	var raw string
	err := s.db.QueryRow(`SELECT last_attention_at FROM ticket_delivery_attention WHERE observer_key = ?`, observerKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return TicketDeliveryAttention{}, false, nil
	}
	if err != nil {
		return TicketDeliveryAttention{}, false, err
	}
	return TicketDeliveryAttention{ObserverKey: observerKey, LastAttentionAt: parseTicketTime(raw)}, true, nil
}

// SetTicketDeliveryAttention advances the observer's interruption clock. The
// value is monotonic so concurrent successful reads cannot move it backwards.
func (s *Store) SetTicketDeliveryAttention(observerKey string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil || observerKey == "" {
		return nil
	}
	return setTicketDeliveryAttentionTx(s.db, observerKey, at)
}

func setTicketDeliveryAttentionTx(ex ticketExecer, observerKey string, at time.Time) error {
	_, err := ex.Exec(`
		INSERT INTO ticket_delivery_attention (observer_key, last_attention_at)
		VALUES (?, ?)
		ON CONFLICT(observer_key) DO UPDATE SET
			last_attention_at = MAX(last_attention_at, excluded.last_attention_at)
	`, observerKey, formatTicketTime(at))
	return err
}
