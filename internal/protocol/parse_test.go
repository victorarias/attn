package protocol

import (
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "register message",
			input:   `{"cmd":"register","id":"abc","label":"test","dir":"/tmp","tmux":"main:1.%0"}`,
			wantCmd: CmdRegister,
		},
		{
			name:    "state message",
			input:   `{"cmd":"state","id":"abc","state":"waiting"}`,
			wantCmd: CmdState,
		},
		{
			name:    "query message",
			input:   `{"cmd":"query","filter":"waiting"}`,
			wantCmd: CmdQuery,
		},
		{
			name:    "unregister message",
			input:   `{"cmd":"unregister","id":"abc"}`,
			wantCmd: CmdUnregister,
		},
		{
			name:    "clear warnings message",
			input:   `{"cmd":"clear_warnings"}`,
			wantCmd: CmdClearWarnings,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "missing cmd",
			input:   `{"id":"abc"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := ParseMessage([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestParseRegister(t *testing.T) {
	input := `{"cmd":"register","id":"abc123","label":"drumstick","dir":"/home/user/project","tmux":"main:1.%42"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdRegister {
		t.Fatalf("cmd = %q, want %q", cmd, CmdRegister)
	}

	msg, ok := data.(*RegisterMessage)
	if !ok {
		t.Fatalf("data type = %T, want *RegisterMessage", data)
	}
	if msg.ID != "abc123" {
		t.Errorf("ID = %q, want %q", msg.ID, "abc123")
	}
	if Deref(msg.Label) != "drumstick" {
		t.Errorf("Label = %q, want %q", Deref(msg.Label), "drumstick")
	}
}
