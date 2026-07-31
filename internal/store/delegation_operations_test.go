package store

import (
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegationOperationReservesActiveTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	first, claimed, err := s.ClaimDelegationOperation("request-1", "operation-1", "session-1", "", "planned", `{"ticket_id":"planned"}`, now)
	if err != nil || !claimed {
		t.Fatalf("first claim = %+v, %v, %v", first, claimed, err)
	}
	if protocol.Deref(first.Operation.TicketID) != "planned" {
		t.Fatalf("reserved ticket = %q", protocol.Deref(first.Operation.TicketID))
	}
	_, _, err = s.ClaimDelegationOperation("request-2", "operation-2", "session-2", "", "planned", `{"ticket_id":"planned"}`, now)
	if !errors.Is(err, ErrTicketDelegationReserved) {
		t.Fatalf("second claim error = %v, want ErrTicketDelegationReserved", err)
	}
	if err := s.UpdateDelegationOperation("operation-1", protocol.DelegationOperationStateFailed, "failed", "", "", "", nil, errors.New("failed"), now); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.ClaimDelegationOperation("request-3", "operation-3", "session-3", "", "planned", `{"ticket_id":"planned"}`, now); err != nil || !claimed {
		t.Fatalf("claim after terminal operation = %v, %v", claimed, err)
	}
}
