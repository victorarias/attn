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
			name:    "delegate message",
			input:   `{"cmd":"delegate","source_session_id":"abc","brief":"Investigate this","agent":"codex"}`,
			wantCmd: CmdDelegate,
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

func TestParseDelegatePlacementAndWorktree(t *testing.T) {
	input := `{"cmd":"delegate","source_session_id":"source-1","brief":"Investigate this","agent":"codex","placement":"new_workspace","worktree":{"repo":"/repo","branch":"feat/delegated","starting_from":"main"}}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	if cmd != CmdDelegate {
		t.Fatalf("cmd = %q, want %q", cmd, CmdDelegate)
	}
	msg := data.(*DelegateMessage)
	if Deref(msg.Placement) != "new_workspace" || msg.Worktree == nil || msg.Worktree.Branch != "feat/delegated" {
		t.Fatalf("delegate message = %+v", msg)
	}
	if Deref(msg.Worktree.Repo) != "/repo" || Deref(msg.Worktree.StartingFrom) != "main" {
		t.Fatalf("delegate worktree = %+v", msg.Worktree)
	}
}

func TestParseDispatchCommands(t *testing.T) {
	cmd, data, err := ParseMessage([]byte(`{"cmd":"list_dispatches","source_session_id":"chief-1"}`))
	if err != nil {
		t.Fatalf("ParseMessage(list_dispatches) error = %v", err)
	}
	if cmd != CmdListDispatches || data.(*ListDispatchesMessage).SourceSessionID != "chief-1" {
		t.Fatalf("list dispatches = %q %+v", cmd, data)
	}

	cmd, data, err = ParseMessage([]byte(`{
		"cmd":"report_dispatch",
		"source_session_id":"worker-1",
		"report":"waiting for a decision",
		"structured_report":{
			"report_type":"blocker",
			"summary":"Core implementation ready",
			"work_state":"needs_input",
			"reported_at":""
		}
	}`))
	if err != nil {
		t.Fatalf("ParseMessage(report_dispatch) error = %v", err)
	}
	report := data.(*ReportDispatchMessage)
	if cmd != CmdReportDispatch ||
		report.SourceSessionID != "worker-1" ||
		report.Report != "waiting for a decision" ||
		report.StructuredReport == nil ||
		report.StructuredReport.WorkState != DispatchWorkStateNeedsInput {
		t.Fatalf("report dispatch = %q %+v", cmd, report)
	}

	cmd, data, err = ParseMessage([]byte(`{"cmd":"get_dispatch","source_session_id":"worker-1"}`))
	if err != nil {
		t.Fatalf("ParseMessage(get_dispatch) error = %v", err)
	}
	if cmd != CmdGetDispatch || data.(*GetDispatchMessage).SourceSessionID != "worker-1" {
		t.Fatalf("get dispatch = %q %+v", cmd, data)
	}

	cmd, data, err = ParseMessage([]byte(`{
		"cmd":"resolve_dispatch_request",
		"source_session_id":"chief-1",
		"dispatch_id":"dispatch-1",
		"response":"Use V1.",
		"resolution_link":"https://example.test/decision"
	}`))
	if err != nil {
		t.Fatalf("ParseMessage(resolve_dispatch_request) error = %v", err)
	}
	resolution := data.(*ResolveDispatchRequestMessage)
	if cmd != CmdResolveDispatchRequest ||
		resolution.DispatchID != "dispatch-1" ||
		resolution.Response != "Use V1." {
		t.Fatalf("resolve dispatch request = %q %+v", cmd, resolution)
	}
}

func TestParseWorkspaceContextCommands(t *testing.T) {
	tests := []struct {
		input string
		cmd   string
	}{
		{`{"cmd":"workspace_context_checkout","source_session_id":"session-1","force":true}`, CmdWorkspaceContextCheckout},
		{`{"cmd":"workspace_context_update","source_session_id":"session-1"}`, CmdWorkspaceContextUpdate},
		{`{"cmd":"workspace_context_status","source_session_id":"session-1"}`, CmdWorkspaceContextStatus},
		{`{"cmd":"workspace_context_list","request_id":"request-1"}`, CmdWorkspaceContextList},
		{`{"cmd":"workspace_context_compact","source_session_id":"session-1"}`, CmdWorkspaceContextCompact},
		{`{"cmd":"workspace_context_rollback","source_session_id":"session-1"}`, CmdWorkspaceContextRollback},
	}
	for _, test := range tests {
		cmd, _, err := ParseMessage([]byte(test.input))
		if err != nil {
			t.Fatalf("ParseMessage(%s) error = %v", test.input, err)
		}
		if cmd != test.cmd {
			t.Fatalf("ParseMessage(%s) cmd = %q, want %q", test.input, cmd, test.cmd)
		}
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

func TestParseWorkspaceLayoutDockTile(t *testing.T) {
	input := `{"cmd":"workspace_layout_dock_tile","workspace_id":"ws1","anchor_pane_id":"pane-a","edge":"right","tile_id":"tile-md","tile_kind":"markdown","ratio":0.3}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutDockTile {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutDockTile)
	}
	msg, ok := data.(*WorkspaceLayoutDockTileMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutDockTileMessage", data)
	}
	if msg.AnchorPaneID != "pane-a" || msg.TileID != "tile-md" || msg.TileKind != "markdown" {
		t.Errorf("fields = %q/%q/%q, want pane-a/tile-md/markdown", msg.AnchorPaneID, msg.TileID, msg.TileKind)
	}
	if msg.Edge != WorkspaceLayoutDockEdgeRight {
		t.Errorf("edge = %q, want right", msg.Edge)
	}
	if msg.Ratio == nil || *msg.Ratio != 0.3 {
		t.Errorf("ratio = %v, want 0.3", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutUndockTile(t *testing.T) {
	input := `{"cmd":"workspace_layout_undock_tile","workspace_id":"ws1","tile_id":"tile-md"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutUndockTile {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutUndockTile)
	}
	msg, ok := data.(*WorkspaceLayoutUndockTileMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutUndockTileMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-md" {
		t.Errorf("fields = %q/%q, want ws1/tile-md", msg.WorkspaceID, msg.TileID)
	}
}

func TestParseWorkspaceLayoutUpdateTile(t *testing.T) {
	input := `{"cmd":"workspace_layout_update_tile","workspace_id":"ws1","tile_id":"tile-browser","tile_params":"https://example.com/docs","request_id":"request-1"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutUpdateTile {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutUpdateTile)
	}
	msg, ok := data.(*WorkspaceLayoutUpdateTileMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutUpdateTileMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-browser" || msg.TileParams != "https://example.com/docs" || msg.RequestID != "request-1" {
		t.Errorf("unexpected fields: %+v", msg)
	}
}

func TestParseWorkspaceLayoutMoveLeafToWorkspace(t *testing.T) {
	input := `{"cmd":"workspace_layout_move_leaf_to_workspace","source_workspace_id":"ws1","target_workspace_id":"ws2","leaf_id":"pane-a","anchor_id":"pane-b","edge":"left","ratio":0.32}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutMoveLeafToWorkspace {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutMoveLeafToWorkspace)
	}
	msg, ok := data.(*WorkspaceLayoutMoveLeafToWorkspaceMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutMoveLeafToWorkspaceMessage", data)
	}
	if msg.SourceWorkspaceID != "ws1" || msg.TargetWorkspaceID != "ws2" || msg.LeafID != "pane-a" || Deref(msg.AnchorID) != "pane-b" {
		t.Errorf("fields = %q/%q/%q/%q, want ws1/ws2/pane-a/pane-b", msg.SourceWorkspaceID, msg.TargetWorkspaceID, msg.LeafID, Deref(msg.AnchorID))
	}
	if msg.Edge != WorkspaceLayoutDockEdgeLeft {
		t.Errorf("edge = %q, want left", msg.Edge)
	}
	if msg.Ratio == nil || *msg.Ratio != 0.32 {
		t.Errorf("ratio = %v, want 0.32", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutDockTileIgnoresInjectedParams(t *testing.T) {
	input := `{"cmd":"workspace_layout_dock_tile","workspace_id":"ws1","anchor_pane_id":"pane-a","edge":"right","tile_id":"tile-md","tile_kind":"markdown","tile_params":"/abs/file.md"}`
	_, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	msg := data.(*WorkspaceLayoutDockTileMessage)
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-md" || msg.TileKind != "markdown" {
		t.Errorf("fields = %q/%q/%q, want ws1/tile-md/markdown", msg.WorkspaceID, msg.TileID, msg.TileKind)
	}
}

func TestParseWorkspaceTileContentGet(t *testing.T) {
	input := `{"cmd":"workspace_tile_content_get","workspace_id":"ws1","tile_id":"tile-md"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceTileContentGet {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceTileContentGet)
	}
	msg, ok := data.(*WorkspaceTileContentGetMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceTileContentGetMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-md" {
		t.Errorf("fields = %q/%q, want ws1/tile-md", msg.WorkspaceID, msg.TileID)
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

func TestParseBrowserMessages(t *testing.T) {
	t.Run("open browser", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"open_browser","url":"http://localhost:3000","session_id":"sess-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		if cmd != CmdOpenBrowser {
			t.Fatalf("cmd = %q, want %q", cmd, CmdOpenBrowser)
		}
		msg, ok := data.(*OpenBrowserMessage)
		if !ok || msg.URL != "http://localhost:3000" || Deref(msg.SessionID) != "sess-1" {
			t.Fatalf("message = %#v, want open browser payload", data)
		}
	})

	t.Run("browser control result", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"browser_control_result","request_id":"req-1","success":true,"data":"ok"}`))
		if err != nil {
			t.Fatal(err)
		}
		if cmd != CmdBrowserControlResult {
			t.Fatalf("cmd = %q, want %q", cmd, CmdBrowserControlResult)
		}
		msg, ok := data.(*BrowserControlResultMessage)
		if !ok || msg.RequestID != "req-1" || !msg.Success || Deref(msg.Data) != "ok" {
			t.Fatalf("message = %#v, want browser control result payload", data)
		}
	})
}

func TestParseSetChiefOfStaff(t *testing.T) {
	cmd, data, err := ParseMessage([]byte(`{"cmd":"set_chief_of_staff","session_id":"session-1","chief_of_staff":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if cmd != CmdSetChiefOfStaff {
		t.Fatalf("cmd = %q, want %q", cmd, CmdSetChiefOfStaff)
	}
	msg, ok := data.(*SetChiefOfStaffMessage)
	if !ok || msg.SessionID != "session-1" || !msg.ChiefOfStaff {
		t.Fatalf("message = %#v, want chief-of-staff assignment", data)
	}
}
