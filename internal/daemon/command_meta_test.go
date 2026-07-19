package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCommandMetaCoversAllCommands(t *testing.T) {
	commands := []string{
		protocol.CmdRegister,
		protocol.CmdUnregister,
		protocol.CmdState,
		protocol.CmdSetSessionResumeID,
		protocol.CmdSessionInstructions,
		protocol.CmdSessionTranscript,
		protocol.CmdStop,
		protocol.CmdTodos,
		protocol.CmdQuery,
		protocol.CmdHeartbeat,
		protocol.CmdSessionVisualized,
		protocol.CmdSessionSelected,
		protocol.CmdTriggerNudge,
		protocol.CmdMuteWorkspace,
		protocol.CmdQueryPRs,
		protocol.CmdMutePR,
		protocol.CmdMuteRepo,
		protocol.CmdMuteAuthor,
		protocol.CmdCollapseRepo,
		protocol.CmdQueryRepos,
		protocol.CmdQueryAuthors,
		protocol.CmdFetchPRDetails,
		protocol.CmdRefreshPRs,
		protocol.CmdClearSessions,
		protocol.CmdClearWarnings,
		protocol.CmdPRVisited,
		protocol.CmdListWorktrees,
		protocol.CmdCreateWorktree,
		protocol.CmdDeleteWorktree,
		protocol.CmdGetSettings,
		protocol.CmdSetSetting,
		protocol.CmdApprovePR,
		protocol.CmdMergePR,
		protocol.CmdInjectTestPR,
		protocol.CmdInjectTestSession,
		protocol.CmdGetRecentLocations,
		protocol.CmdBrowseDirectory,
		protocol.CmdInspectPath,
		protocol.CmdListBranches,
		protocol.CmdCreateWorktreeFromBranch,
		protocol.CmdGetDefaultBranch,
		protocol.CmdFetchRemotes,
		protocol.CmdListRemoteBranches,
		protocol.CmdEnsureRepo,
		protocol.CmdSubscribeGitStatus,
		protocol.CmdUnsubscribeGitStatus,
		protocol.CmdGetFileDiff,
		protocol.CmdGetRepoInfo,
		protocol.CmdSpawnSession,
		protocol.CmdAttachSession,
		protocol.CmdDetachSession,
		protocol.CmdPtyInput,
		protocol.CmdPtyResize,
		protocol.CmdKillSession,
		protocol.CmdWorkspaceLayoutGet,
		protocol.CmdWorkspaceLayoutAddSessionPane,
		protocol.CmdWorkspaceLayoutClosePane,
		protocol.CmdWorkspaceLayoutFocusPane,
		protocol.CmdWorkspaceLayoutRenamePane,
		protocol.CmdWorkspaceLayoutSetSplitRatio,
		protocol.CmdWorkspaceLayoutDockTile,
		protocol.CmdWorkspaceLayoutUndockTile,
		protocol.CmdWorkspaceLayoutUpdateTile,
		protocol.CmdWorkspaceTileContentGet,
		protocol.CmdMarkdownAnnotationsGet,
		protocol.CmdMarkdownAnnotationsSave,
		protocol.CmdMarkdownAnnotationsClear,
		protocol.CmdMarkdownAnnotationsSubmit,
		protocol.CmdRenameSession,
		protocol.CmdRenameWorkspace,
		protocol.CmdSetChiefOfStaff,
	}

	for _, cmd := range commands {
		if _, ok := CommandMeta[cmd]; !ok {
			t.Fatalf("missing command metadata for %s", cmd)
		}
	}
}

func TestCommandMetaExamples(t *testing.T) {
	if meta := CommandMeta[protocol.CmdPtyInput]; meta.Scope != ScopeSession {
		t.Fatalf("pty_input scope = %v, want %v", meta.Scope, ScopeSession)
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
	if !blocksDuringRecovery(protocol.CmdPtyInput) {
		t.Fatal("pty_input should block during recovery")
	}
	if shouldLogWSCommand(protocol.CmdPtyInput) {
		t.Fatal("pty_input should be excluded from normal websocket command logging")
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
			name: "session_visualized",
			cmd:  protocol.CmdSessionVisualized,
			msg:  &protocol.SessionVisualizedMessage{ID: "sess-visualized"},
			want: "sess-visualized",
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
			// Hub→remote regression: Submit's draft-read/format/deliver all run
			// on whichever daemon handles the command, so it must route by the
			// SAME target_session_id it delivers to — otherwise a hub would
			// format-and-clear a draft it never wrote (Get/Save/Clear route by
			// workspace_id) and try to deliver against a session absent from
			// its own local store.
			name: "markdown_annotations_submit",
			cmd:  protocol.CmdMarkdownAnnotationsSubmit,
			msg:  &protocol.MarkdownAnnotationsSubmitMessage{Path: "/tmp/notes.md", TargetSessionID: "sess-md-submit"},
			want: "sess-md-submit",
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
	msg := &protocol.MarkdownAnnotationsGetMessage{WorkspaceID: "workspace-md-get"}
	if got := remoteCommandWorkspaceID(protocol.CmdMarkdownAnnotationsGet, msg); got != msg.WorkspaceID {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, msg.WorkspaceID)
	}
}

func TestRemoteCommandWorkspaceID_IncludesMarkdownAnnotationsSave(t *testing.T) {
	msg := &protocol.MarkdownAnnotationsSaveMessage{WorkspaceID: "workspace-md-save"}
	if got := remoteCommandWorkspaceID(protocol.CmdMarkdownAnnotationsSave, msg); got != msg.WorkspaceID {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, msg.WorkspaceID)
	}
}

func TestRemoteCommandWorkspaceID_IncludesMarkdownAnnotationsClear(t *testing.T) {
	msg := &protocol.MarkdownAnnotationsClearMessage{WorkspaceID: "workspace-md-clear"}
	if got := remoteCommandWorkspaceID(protocol.CmdMarkdownAnnotationsClear, msg); got != msg.WorkspaceID {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, msg.WorkspaceID)
	}
}

func TestRemoteCommandWorkspaceID_IncludesRenameWorkspace(t *testing.T) {
	msg := &protocol.RenameWorkspaceMessage{WorkspaceID: "workspace-rename"}
	if got := remoteCommandWorkspaceID(protocol.CmdRenameWorkspace, msg); got != msg.WorkspaceID {
		t.Fatalf("remoteCommandWorkspaceID() = %q, want %q", got, msg.WorkspaceID)
	}
}
