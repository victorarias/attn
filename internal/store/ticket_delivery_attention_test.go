package store

import (
	"testing"
	"time"
)

func TestTicketDeliveryAttentionIsDurableAndMonotonic(t *testing.T) {
	dbPath := t.TempDir() + "/attn.db"
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	if err := s.SetTicketDeliveryAttention("role:chief_of_staff", first); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTicketDeliveryAttention("role:chief_of_staff", first.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok, err := s.TicketDeliveryAttention("role:chief_of_staff")
	if err != nil || !ok {
		t.Fatalf("TicketDeliveryAttention = %+v, %v, %v", got, ok, err)
	}
	if !got.LastAttentionAt.Equal(first) {
		t.Fatalf("last attention = %s, want %s", got.LastAttentionAt, first)
	}
}
