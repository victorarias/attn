package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCommandMetaExamples(t *testing.T) {
	if meta := CommandMeta[protocol.CmdPtyInput]; meta.Scope != ScopeSession {
		t.Fatalf("pty_input scope = %v, want %v", meta.Scope, ScopeSession)
	}
	if meta := CommandMeta[protocol.CmdTerminalPointerActivity]; meta.Scope != ScopeSession {
		t.Fatalf("terminal_pointer_activity scope = %v, want %v", meta.Scope, ScopeSession)
	}
	if meta := CommandMeta[protocol.CmdSpawnSession]; meta.Scope != ScopeEndpoint {
		t.Fatalf("spawn_session scope = %v, want %v", meta.Scope, ScopeEndpoint)
	}
	if meta := CommandMeta[protocol.CmdClearSessions]; meta.Scope != ScopeHubLocal {
		t.Fatalf("clear_sessions scope = %v, want %v", meta.Scope, ScopeHubLocal)
	}
	if meta := CommandMeta[protocol.CmdQueryPRs]; meta.Scope != ScopeHubLocal {
		t.Fatalf("query_prs scope = %v, want %v", meta.Scope, ScopeHubLocal)
	}
	if meta := CommandMeta[protocol.CmdOpenSeed]; meta.Scope != ScopeHubLocal {
		t.Fatalf("open_seed scope = %v, want %v", meta.Scope, ScopeHubLocal)
	}
	if meta := CommandMeta[protocol.CmdSeedEdit]; meta.Scope != ScopeHubLocal {
		t.Fatalf("seed_edit scope = %v, want %v", meta.Scope, ScopeHubLocal)
	}
	if !blocksDuringRecovery(protocol.CmdPtyInput) {
		t.Fatal("pty_input should block during recovery")
	}
	if shouldLogWSCommand(protocol.CmdPtyInput) {
		t.Fatal("pty_input should be excluded from normal websocket command logging")
	}
	if shouldLogWSCommand(protocol.CmdTerminalPointerActivity) {
		t.Fatal("terminal_pointer_activity should be excluded from normal websocket command logging")
	}
}

type stubRemoteCommandResolver struct {
	path map[string]string
}

func (s stubRemoteCommandResolver) EndpointIDForPath(path string) (string, bool) {
	endpointID, ok := s.path[path]
	return endpointID, ok
}

func TestRemoteCommandScopedEndpointID(t *testing.T) {
	resolver := stubRemoteCommandResolver{
		path: map[string]string{
			"/srv/repo": "endpoint-path",
		},
	}

	if endpointID, ok := remoteCommandScopedEndpointID(&protocol.GetFileDiffMessage{Directory: "/srv/repo"}, resolver); !ok || endpointID != "endpoint-path" {
		t.Fatalf("remoteCommandScopedEndpointID(path) = (%q, %v), want (%q, true)", endpointID, ok, "endpoint-path")
	}
}

func TestRemoteCommandSessionID(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		msg  interface{}
		want string
	}{
		{
			name: "unregister handled locally",
			cmd:  protocol.CmdUnregister,
			msg:  &protocol.UnregisterMessage{ID: "sess-unregister"},
			want: "",
		},
		{
			name: "session_selected",
			cmd:  protocol.CmdSessionSelected,
			msg:  &protocol.SessionSelectedMessage{ID: "sess-selected"},
			want: "sess-selected",
		},
		{
			name: "rename_session",
			cmd:  protocol.CmdRenameSession,
			msg:  &protocol.RenameSessionMessage{SessionID: "sess-rename"},
			want: "sess-rename",
		},
		{
			name: "open_markdown",
			cmd:  protocol.CmdOpenMarkdown,
			msg:  &protocol.OpenMarkdownMessage{Path: "/tmp/notes.md", SessionID: protocol.Ptr("sess-open-markdown")},
			want: "sess-open-markdown",
		},
		{
			name: "open_markdown without session id",
			cmd:  protocol.CmdOpenMarkdown,
			msg:  &protocol.OpenMarkdownMessage{Path: "/tmp/notes.md"},
			want: "",
		},
		{
			name: "open_seed stays hub local",
			cmd:  protocol.CmdOpenSeed,
			msg:  &protocol.OpenSeedMessage{SeedID: "s-abc123", SessionID: protocol.Ptr("sess-remote")},
			want: "",
		},
		{
			// Hub→remote regression: Submit's draft-read/format/deliver all run
			// on whichever daemon handles the command, so it must route by the
			// SAME target_session_id it delivers to — otherwise a hub would
			// format-and-clear a draft it never wrote (Get/Save/Clear route by
			// workspace_id) and try to deliver against a session absent from
			// its own local store.
			name: "markdown_annotations_submit",
			cmd:  protocol.CmdMarkdownAnnotationsSubmit,
			msg:  &protocol.MarkdownAnnotationsSubmitMessage{Path: protocol.Ptr("/tmp/notes.md"), TargetSessionID: protocol.Ptr("sess-md-submit")},
			want: "sess-md-submit",
		},
		{
			name: "markdown_annotations_note_on_seed stays home local",
			cmd:  protocol.CmdMarkdownAnnotationsSubmit,
			msg: &protocol.MarkdownAnnotationsSubmitMessage{
				SeedID: protocol.Ptr("s-abc123"), TargetSeedID: protocol.Ptr("s-abc123"),
			},
			want: "",
		},
		{
			// Hub→remote regression: a turn's stamps are written by the daemon
			// that owns the session. Settled locally, a hub would write nothing
			// the remote knows about, and the endpoint's next snapshot would
			// report the turn still owed and put the row back.
			name: "settle_turn",
			cmd:  protocol.CmdSettleTurn,
			msg:  &protocol.SettleTurnMessage{SessionID: "sess-settle"},
			want: "sess-settle",
		},
		{
			// Hub→remote regression: the transcript is read from the filesystem
			// of the machine running the agent. A hub answering locally has no
			// transcript and no session row, so a remote pane would offer
			// nothing to annotate.
			name: "session_messages_get",
			cmd:  protocol.CmdSessionMessagesGet,
			msg:  &protocol.SessionMessagesGetMessage{SessionID: "sess-messages"},
			want: "sess-messages",
		},
		{
			// Hub→remote regression: the submit types into the session's PTY,
			// which only the daemon running that PTY can do. Handled locally, a
			// remote pane's Send all would report success and deliver nothing.
			name: "session_annotations_submit",
			cmd:  protocol.CmdSessionAnnotationsSubmit,
			msg:  &protocol.SessionAnnotationsSubmitMessage{SessionID: "sess-anno-submit", Text: "feedback"},
			want: "sess-anno-submit",
		},
		{
			// Hub→remote regression: annotation drafts are keyed by session in
			// the owning daemon's store. Read, written, or cleared on the hub
			// instead, a remote pane would keep a second divergent set — and
			// the generation ordering that makes two panes converge would be
			// comparing against the wrong row entirely.
			name: "session_annotations_get",
			cmd:  protocol.CmdSessionAnnotationsGet,
			msg:  &protocol.SessionAnnotationsGetMessage{SessionID: "sess-anno-get"},
			want: "sess-anno-get",
		},
		{
			name: "session_annotations_save",
			cmd:  protocol.CmdSessionAnnotationsSave,
			msg:  &protocol.SessionAnnotationsSaveMessage{SessionID: "sess-anno-save"},
			want: "sess-anno-save",
		},
		{
			name: "session_annotations_clear",
			cmd:  protocol.CmdSessionAnnotationsClear,
			msg:  &protocol.SessionAnnotationsClearMessage{SessionID: "sess-anno-clear"},
			want: "sess-anno-clear",
		},
	}

	for _, tc := range cases {
		if got := remoteCommandSessionID(tc.cmd, tc.msg); got != tc.want {
			t.Fatalf("%s remoteCommandSessionID() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRemoteCommandWorkspaceID_IncludesTileContentGet(t *testing.T) {
	msg := &protocol.WorkspaceTileContentGetMessage{WorkspaceID: "workspace-remote"}
	if got := remoteCommandWorkspaceID(protocol.CmdWorkspaceTileContentGet, msg); got != msg.WorkspaceID {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, msg.WorkspaceID)
	}
}

func TestRemoteCommandWorkspaceID_IncludesMarkdownAnnotationsGet(t *testing.T) {
	msg := &protocol.MarkdownAnnotationsGetMessage{SourceKind: annotationSourceFile, WorkspaceID: protocol.Ptr("workspace-md-get")}
	if got := remoteCommandWorkspaceID(protocol.CmdMarkdownAnnotationsGet, msg); got != protocol.Deref(msg.WorkspaceID) {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, protocol.Deref(msg.WorkspaceID))
	}
}

func TestRemoteCommandWorkspaceID_IncludesMarkdownAnnotationsSave(t *testing.T) {
	msg := &protocol.MarkdownAnnotationsSaveMessage{SourceKind: annotationSourceFile, WorkspaceID: protocol.Ptr("workspace-md-save")}
	if got := remoteCommandWorkspaceID(protocol.CmdMarkdownAnnotationsSave, msg); got != protocol.Deref(msg.WorkspaceID) {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, protocol.Deref(msg.WorkspaceID))
	}
}

func TestRemoteCommandWorkspaceID_IncludesMarkdownAnnotationsClear(t *testing.T) {
	msg := &protocol.MarkdownAnnotationsClearMessage{SourceKind: annotationSourceFile, WorkspaceID: protocol.Ptr("workspace-md-clear")}
	if got := remoteCommandWorkspaceID(protocol.CmdMarkdownAnnotationsClear, msg); got != protocol.Deref(msg.WorkspaceID) {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, protocol.Deref(msg.WorkspaceID))
	}
}

func TestRemoteCommandWorkspaceID_DoesNotRouteSeedAnnotations(t *testing.T) {
	workspaceID := protocol.Ptr("workspace-must-not-route")
	cases := []struct {
		cmd string
		msg interface{}
	}{
		{protocol.CmdMarkdownAnnotationsGet, &protocol.MarkdownAnnotationsGetMessage{SourceKind: annotationSourceSeed, WorkspaceID: workspaceID}},
		{protocol.CmdMarkdownAnnotationsSave, &protocol.MarkdownAnnotationsSaveMessage{SourceKind: annotationSourceSeed, WorkspaceID: workspaceID}},
		{protocol.CmdMarkdownAnnotationsClear, &protocol.MarkdownAnnotationsClearMessage{SourceKind: annotationSourceSeed, WorkspaceID: workspaceID}},
	}
	for _, tc := range cases {
		if got := remoteCommandWorkspaceID(tc.cmd, tc.msg); got != "" {
			t.Fatalf("%s routed seed annotation to %q", tc.cmd, got)
		}
	}
}

func TestRemoteCommandWorkspaceID_IncludesRenameWorkspace(t *testing.T) {
	msg := &protocol.RenameWorkspaceMessage{WorkspaceID: "workspace-rename"}
	if got := remoteCommandWorkspaceID(protocol.CmdRenameWorkspace, msg); got != msg.WorkspaceID {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, msg.WorkspaceID)
	}
}
