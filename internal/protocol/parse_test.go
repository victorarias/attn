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
			name:    "session selected message",
			input:   `{"cmd":"session_selected","id":"abc"}`,
			wantCmd: CmdSessionSelected,
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
			name:    "workspace layout set split ratio message",
			input:   `{"cmd":"workspace_layout_set_split_ratio","workspace_id":"ws1","split_id":"split-1","ratio":0.3}`,
			wantCmd: CmdWorkspaceLayoutSetSplitRatio,
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
			input:   `{"cmd":"install_plugin","source":"git@example.com:team/plugin.git"}`,
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

func TestParseWorkspaceLayoutSetSplitRatio(t *testing.T) {
	input := `{"cmd":"workspace_layout_set_split_ratio","workspace_id":"ws1","split_id":"split-1","ratio":0.3}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutSetSplitRatio {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutSetSplitRatio)
	}
	msg, ok := data.(*WorkspaceLayoutSetSplitRatioMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutSetSplitRatioMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.SplitID != "split-1" {
		t.Errorf("ids = %q/%q, want ws1/split-1", msg.WorkspaceID, msg.SplitID)
	}
	if msg.Ratio != 0.3 {
		t.Errorf("ratio = %v, want 0.3", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutDockPanel(t *testing.T) {
	input := `{"cmd":"workspace_layout_dock_panel","workspace_id":"ws1","anchor_pane_id":"pane-a","edge":"right","panel_id":"panel-md","panel_kind":"markdown","ratio":0.3}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutDockPanel {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutDockPanel)
	}
	msg, ok := data.(*WorkspaceLayoutDockPanelMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutDockPanelMessage", data)
	}
	if msg.AnchorPaneID != "pane-a" || msg.PanelID != "panel-md" || msg.PanelKind != "markdown" {
		t.Errorf("fields = %q/%q/%q, want pane-a/panel-md/markdown", msg.AnchorPaneID, msg.PanelID, msg.PanelKind)
	}
	if msg.Edge != WorkspaceLayoutDockEdgeRight {
		t.Errorf("edge = %q, want right", msg.Edge)
	}
	if msg.Ratio == nil || *msg.Ratio != 0.3 {
		t.Errorf("ratio = %v, want 0.3", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutUndockPanel(t *testing.T) {
	input := `{"cmd":"workspace_layout_undock_panel","workspace_id":"ws1","panel_id":"panel-md"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutUndockPanel {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutUndockPanel)
	}
	msg, ok := data.(*WorkspaceLayoutUndockPanelMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutUndockPanelMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.PanelID != "panel-md" {
		t.Errorf("fields = %q/%q, want ws1/panel-md", msg.WorkspaceID, msg.PanelID)
	}
}

func TestParseWorkspaceLayoutDockPanelIgnoresInjectedParams(t *testing.T) {
	input := `{"cmd":"workspace_layout_dock_panel","workspace_id":"ws1","anchor_pane_id":"pane-a","edge":"right","panel_id":"panel-md","panel_kind":"markdown","panel_params":"/abs/file.md"}`
	_, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	msg := data.(*WorkspaceLayoutDockPanelMessage)
	if msg.WorkspaceID != "ws1" || msg.PanelID != "panel-md" || msg.PanelKind != "markdown" {
		t.Errorf("fields = %q/%q/%q, want ws1/panel-md/markdown", msg.WorkspaceID, msg.PanelID, msg.PanelKind)
	}
}

func TestParseWorkspacePanelContentGet(t *testing.T) {
	input := `{"cmd":"workspace_panel_content_get","workspace_id":"ws1","panel_id":"panel-md"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspacePanelContentGet {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspacePanelContentGet)
	}
	msg, ok := data.(*WorkspacePanelContentGetMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspacePanelContentGetMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.PanelID != "panel-md" {
		t.Errorf("fields = %q/%q, want ws1/panel-md", msg.WorkspaceID, msg.PanelID)
	}
}

func TestParseOpenMarkdown(t *testing.T) {
	input := `{"cmd":"open_markdown","path":"/abs/file.md","session_id":"sess-1"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdOpenMarkdown {
		t.Fatalf("cmd = %q, want %q", cmd, CmdOpenMarkdown)
	}
	msg, ok := data.(*OpenMarkdownMessage)
	if !ok {
		t.Fatalf("data type = %T, want *OpenMarkdownMessage", data)
	}
	if msg.Path != "/abs/file.md" || msg.SessionID == nil || *msg.SessionID != "sess-1" {
		t.Errorf("fields = %q/%v, want /abs/file.md/sess-1", msg.Path, msg.SessionID)
	}
}
