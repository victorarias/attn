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
			input:   `{"cmd":"register","id":"abc","label":"test","dir":"/tmp","workspace_id":"workspace-abc"}`,
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
			name:    "workspace layout get message",
			input:   `{"cmd":"workspace_layout_get","workspace_id":"ws1"}`,
			wantCmd: CmdWorkspaceLayoutGet,
		},
		{
			name:    "clear warnings message",
			input:   `{"cmd":"clear_warnings"}`,
			wantCmd: CmdClearWarnings,
		},
		{
			name:    "list plugins message",
			input:   `{"cmd":"list_plugins"}`,
			wantCmd: CmdListPlugins,
		},
		{
			name:    "install plugin message",
			input:   `{"cmd":"install_plugin","path":"/tmp/plugin"}`,
			wantCmd: CmdInstallPlugin,
		},
		{
			name:    "remove plugin message",
			input:   `{"cmd":"remove_plugin","name":"demo"}`,
			wantCmd: CmdRemovePlugin,
		},
		{
			name:    "set plugin priority message",
			input:   `{"cmd":"set_plugin_priority","name":"demo","priority":10}`,
			wantCmd: CmdSetPluginPriority,
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
	input := `{"cmd":"register","id":"abc123","label":"drumstick","dir":"/home/user/project"}`
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
