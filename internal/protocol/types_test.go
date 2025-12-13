package protocol

import (
	"encoding/json"
	"testing"
)

func TestRegisterMessage_Marshal(t *testing.T) {
	msg := RegisterMessage{
		Cmd:   "register",
		ID:    "abc123",
		Label: Ptr("drumstick"),
		Dir:   "/home/user/projects/drumstick",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RegisterMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.ID != msg.ID {
		t.Errorf("ID mismatch: got %q, want %q", decoded.ID, msg.ID)
	}
	if Deref(decoded.Label) != Deref(msg.Label) {
		t.Errorf("Label mismatch: got %q, want %q", Deref(decoded.Label), Deref(msg.Label))
	}
}

func TestStateMessage_Marshal(t *testing.T) {
	msg := StateMessage{
		Cmd:   "state",
		ID:    "abc123",
		State: "waiting",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded StateMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.State != "waiting" {
		t.Errorf("State mismatch: got %q, want %q", decoded.State, msg.State)
	}
}

func TestQueryMessage_Marshal(t *testing.T) {
	msg := QueryMessage{
		Cmd:    "query",
		Filter: Ptr("waiting"),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded QueryMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if Deref(decoded.Filter) != "waiting" {
		t.Errorf("Filter mismatch: got %q, want %q", Deref(decoded.Filter), Deref(msg.Filter))
	}
}
