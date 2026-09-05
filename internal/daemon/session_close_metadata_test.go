package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func closeMetadataFixture(t *testing.T) (*Daemon, *wsClient, string, string, string) {
	t.Helper()
	d := newGardenDaemon(t)
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID, sessionID, paneID := "workspace-close-metadata", "session-close-metadata", "pane-close-metadata"
	cwd := t.TempDir()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Close metadata", Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
		PaneID: protocol.Ptr(paneID), SessionID: sessionID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.store.Add(&protocol.Session{
		ID: sessionID, Agent: protocol.SessionAgentCodex, Directory: cwd, WorkspaceID: workspaceID,
		Branch: protocol.Ptr("feature/current"), MainRepo: protocol.Ptr("/projects/repo"),
	})
	d.associateSessionWithWorkspace(sessionID, workspaceID)
	d.store.SetResumeSessionID(sessionID, "native-current")
	return d, client, workspaceID, sessionID, paneID
}

func TestClosePanePreservesExecutionWithoutRunningGit(t *testing.T) {
	for _, saved := range []bool{false, true} {
		name := "first capture"
		if saved {
			name = "previously captured"
		}
		t.Run(name, func(t *testing.T) {
			d, client, workspaceID, sessionID, paneID := closeMetadataFixture(t)
			cwd := d.store.Get(sessionID).Directory
			if saved {
				_, err := d.updateGardenDispatch(sessionID, func(current garden.Dispatch) (garden.Dispatch, bool, error) {
					current.Crown, current.SupersededBy = "s-original", "successor"
					current.RepositoryRoot, current.RepositorySubdir = "/projects/repo", "nested"
					current.Resume = "native-old"
					return current, true, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			bin := t.TempDir()
			calls := filepath.Join(bin, "git-called")
			t.Setenv("CLOSE_TEST_GIT_CALLS", calls)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CLOSE_TEST_GIT_CALLS\"\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
				Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
			})
			expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
			d.waitForSessionTeardown(sessionID)
			if calls, err := os.ReadFile(calls); !os.IsNotExist(err) {
				t.Fatalf("closing invoked Git: %s (read error: %v)", calls, err)
			}
			if d.store.Get(sessionID) != nil || d.store.GetWorkspaceLayout(workspaceID) != nil {
				t.Fatal("close retained the session or its layout")
			}
			execution, ok := d.gardenDispatch(sessionID)
			if !ok || execution.Cwd != cwd || execution.Agent != "codex" || execution.Resume != "native-current" ||
				execution.Branch != "feature/current" || execution.RepositoryRoot != "/projects/repo" {
				t.Fatalf("saved execution = %+v, found=%v", execution, ok)
			}
			if saved && (execution.RepositorySubdir != "nested" || execution.Crown != "s-original" || execution.SupersededBy != "successor") {
				t.Fatalf("close changed saved continuation or ownership: %+v", execution)
			}
		})
	}
}

func TestReapedSessionPreservesCheckedOutBranch(t *testing.T) {
	d, _, workspaceID, sessionID, _ := closeMetadataFixture(t)
	cwd := d.store.Get(sessionID).Directory
	runGit(t, cwd, "init", "-b", "feature/current")
	runGit(t, cwd, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Initial commit")
	if _, err := d.captureGardenSessionExecution(d.store.Get(sessionID)); err != nil {
		t.Fatal(err)
	}
	runGit(t, cwd, "checkout", "-b", "feature/checked-out")
	if got := protocol.Deref(d.store.Get(sessionID).Branch); got != "feature/current" {
		t.Fatalf("cached branch = %q, want feature/current", got)
	}

	d.removeReapedSession(sessionID)

	if d.store.Get(sessionID) != nil || d.store.GetWorkspaceLayout(workspaceID) != nil {
		t.Fatal("reaping retained the session or its layout")
	}
	execution, ok := d.gardenDispatch(sessionID)
	if !ok || execution.Branch != "feature/checked-out" || execution.Resume != "native-current" ||
		!sameDirectory(execution.RepositoryRoot, cwd) {
		t.Fatalf("reaped execution = %+v, found=%v", execution, ok)
	}
}

func TestClosePaneWinsOverInFlightExecutionCapture(t *testing.T) {
	for _, beforeRemoval := range []bool{true, false} {
		name := "after record removal"
		if beforeRemoval {
			name = "before record removal"
		}
		t.Run(name, func(t *testing.T) {
			d, client, workspaceID, sessionID, paneID := closeMetadataFixture(t)
			d.store.SetResumeSessionID(sessionID, "native-old")
			read, release := make(chan struct{}), make(chan struct{})
			var paused atomic.Bool
			d.gardenDispatchBeforeWrite = func(id string) {
				if id == sessionID && paused.CompareAndSwap(false, true) {
					close(read)
					<-release
				}
			}
			done := make(chan error, 1)
			go func() {
				_, err := d.captureGardenSessionExecution(d.store.Get(sessionID))
				done <- err
			}()
			var releaseOnce sync.Once
			finishCapture := func() {
				releaseOnce.Do(func() {
					close(release)
					if err := <-done; err != nil {
						t.Errorf("late capture: %v", err)
					}
				})
			}
			defer finishCapture()
			<-read
			if beforeRemoval {
				var writes atomic.Int32
				d.gardenDispatchAfterWrite = func(id string) {
					// The second close snapshot is saved immediately before record removal.
					if id == sessionID && writes.Add(1) == 2 {
						finishCapture()
					}
				}
			}
			d.store.SetResumeSessionID(sessionID, "native-current")
			d.store.UpdateBranch(sessionID, "feature/newer", true, "/projects/repo", "/projects/repo")
			d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
				Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
			})
			expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
			d.waitForSessionTeardown(sessionID)
			finishCapture()
			execution, ok := d.gardenDispatch(sessionID)
			if !ok || execution.Resume != "native-current" || execution.Branch != "feature/newer" {
				t.Errorf("late capture replaced close information: %+v", execution)
			}
		})
	}
}
