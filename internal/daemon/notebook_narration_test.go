package daemon

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tasks"
)

// drainingConn returns a net.Conn whose writes are silently consumed, so
// d.sendOK(conn) inside handleStop does not block or nil-deref in a test that only
// cares about the narration-trigger side effects.
func drainingConn(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	go io.Copy(io.Discard, server)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client
}

// --- config ---

func TestParseNotebookNarrationConfig(t *testing.T) {
	t.Run("blank uses summarize tier default", func(t *testing.T) {
		config, err := parseNotebookNarrationConfig(notebookSummarizeSessionKind, "")
		if err != nil {
			t.Fatalf("parse blank: %v", err)
		}
		if config.Agent != notebookSummarizeDefaultAgent || config.Model != notebookSummarizeDefaultModel {
			t.Fatalf("config = %+v, want summarize default", config)
		}
	})

	t.Run("blank uses narrate tier default", func(t *testing.T) {
		config, err := parseNotebookNarrationConfig(notebookNarrateWorkspaceKind, "   ")
		if err != nil {
			t.Fatalf("parse blank: %v", err)
		}
		if config.Agent != notebookNarrateDefaultAgent || config.Model != notebookNarrateDefaultModel {
			t.Fatalf("config = %+v, want narrate default", config)
		}
	})

	t.Run("explicit value parses and lowercases agent", func(t *testing.T) {
		config, err := parseNotebookNarrationConfig(notebookNarrateWorkspaceKind, `{"agent":"CLAUDE","model":"claude-opus-test"}`)
		if err != nil {
			t.Fatalf("parse explicit: %v", err)
		}
		if config.Agent != "claude" || config.Model != "claude-opus-test" {
			t.Fatalf("config = %+v", config)
		}
	})

	for name, raw := range map[string]string{
		"missing model": `{"agent":"claude"}`,
		"missing agent": `{"model":"claude-test"}`,
		"unknown field": `{"agent":"claude","model":"claude-test","tier":"strong"}`,
		"unknown agent": `{"agent":"missing","model":"x"}`,
		"trailing json": `{"agent":"claude","model":"claude-test"} {}`,
	} {
		t.Run("invalid: "+name, func(t *testing.T) {
			if _, err := parseNotebookNarrationConfig(notebookSummarizeSessionKind, raw); err == nil {
				t.Fatalf("parseNotebookNarrationConfig(%q) succeeded", raw)
			}
		})
	}
}

func TestNotebookNarrationConfigForAppliesDefaultsAndSettings(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	// Unset -> tier defaults for both kinds.
	summarize, err := d.notebookNarrationConfigFor(notebookSummarizeSessionKind)
	if err != nil {
		t.Fatalf("summarize default: %v", err)
	}
	if summarize.Model != notebookSummarizeDefaultModel {
		t.Fatalf("summarize default model = %q", summarize.Model)
	}
	narrate, err := d.notebookNarrationConfigFor(notebookNarrateWorkspaceKind)
	if err != nil {
		t.Fatalf("narrate default: %v", err)
	}
	if narrate.Model != notebookNarrateDefaultModel {
		t.Fatalf("narrate default model = %q", narrate.Model)
	}

	// A configured override is honored.
	d.store.SetSetting(SettingNotebookNarrateWorkspace, `{"agent":"claude","model":"claude-custom"}`)
	narrate, err = d.notebookNarrationConfigFor(notebookNarrateWorkspaceKind)
	if err != nil {
		t.Fatalf("narrate override: %v", err)
	}
	if narrate.Model != "claude-custom" {
		t.Fatalf("narrate override model = %q", narrate.Model)
	}
}

// --- prompt builders ---

func TestBuildSummarizeSessionPromptEmbedsBriefAndPaths(t *testing.T) {
	prompt := buildSummarizeSessionPrompt("/t/transcript.jsonl", "session-xyz", "/raw/sessions/session-xyz.md")
	if !strings.Contains(prompt, "You are the attn keeper, performing your session-summary duty.") {
		t.Fatal("summarize prompt dropped the verbatim brief")
	}
	for _, want := range []string{
		"TRANSCRIPT_PATH: /t/transcript.jsonl",
		"SESSION_ID: session-xyz",
		"RAW_DIGEST_PATH: /raw/sessions/session-xyz.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("summarize prompt missing %q", want)
		}
	}
}

func TestBuildNarrateWorkspacePromptEmbedsBriefPathsAndRemovalFlag(t *testing.T) {
	prompt := buildNarrateWorkspacePrompt(narrateWorkspacePromptInputs{
		WorkspaceTitle:      "Chifplace",
		WorkspaceID:         "ws-1",
		ContextSnapshotPath: "/raw/context-snapshots/ws-1.md",
		RawSessionsDir:      "/raw/sessions",
		TranscriptPaths:     []string{"/t/a.jsonl", "/t/b.jsonl"},
		JournalPath:         "/nb/journal/2026-06-15.md",
		JournalDir:          "/nb/journal",
		KnowledgeDir:        "/nb/knowledge",
		IsRemovalPass:       true,
	})
	if !strings.Contains(prompt, "You are the attn keeper, narrating this workspace's work into the journal.") {
		t.Fatal("narrate prompt dropped the verbatim brief")
	}
	for _, want := range []string{
		"WORKSPACE_TITLE: Chifplace",
		"WORKSPACE_ID: ws-1",
		"CONTEXT_SNAPSHOT_PATH: /raw/context-snapshots/ws-1.md",
		"RAW_SESSIONS_DIR: /raw/sessions",
		"- /t/a.jsonl",
		"- /t/b.jsonl",
		"JOURNAL_PATH: /nb/journal/2026-06-15.md",
		"JOURNAL_DIR: /nb/journal",
		"KNOWLEDGE_DIR: /nb/knowledge",
		"IS_REMOVAL_PASS: true",
		// The removal-pass knowledge-base archive step and its workspace-link hook.
		"ARCHIVE THE WORKSPACE'S PROJECT FOLDER (removal pass only)",
		"resource: attn:workspace/<WORKSPACE_ID>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("narrate prompt missing %q", want)
		}
	}

	// No transcripts renders the explicit "(none resolved)" line and IS_REMOVAL_PASS=false.
	active := buildNarrateWorkspacePrompt(narrateWorkspacePromptInputs{WorkspaceID: "ws-2"})
	if !strings.Contains(active, "TRANSCRIPT_PATHS: (none resolved)") {
		t.Fatal("narrate prompt missing the empty-transcripts line")
	}
	if !strings.Contains(active, "IS_REMOVAL_PASS: false") {
		t.Fatal("narrate prompt missing IS_REMOVAL_PASS: false")
	}
}

// --- executor test rig ---

// installNotebookNarrationRunner enables the daemon's runner over a temp root and
// registers BOTH narration executors (real bodies), so the executors' resolve-
// inputs / verify-ledger logic runs for real. The agent spawn itself is replaced
// per-test via d.summarizeSessionExecution / d.narrateWorkspaceExecution. A fast
// poll interval avoids real-time waits. Returns the notebook root.
func installNotebookNarrationRunner(t *testing.T, d *Daemon) string {
	t.Helper()
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), writeFakeAgentExecutable(t))

	runner := tasks.New(tasks.Options{
		Root:         filepath.Join(t.TempDir(), "tasks"),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.RegisterWithTimeout(notebookSummarizeSessionKind, d.summarizeSessionExecutor, notebookSummarizeSessionTimeout); err != nil {
		t.Fatalf("register summarize_session: %v", err)
	}
	if err := runner.RegisterWithTimeout(notebookNarrateWorkspaceKind, d.narrateWorkspaceExecutor, notebookNarrateWorkspaceTimeout); err != nil {
		t.Fatalf("register narrate_workspace: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.compactRunner = runner
	return root
}

// writeFakeAgentExecutable writes an executable no-op script and returns its path,
// so resolveNotebookNarrationExecutable's exec.LookPath succeeds without a real
// claude/codex on PATH. The script is never actually invoked: the daemon-level
// execution hook replaces RunHeadlessTask.
func writeFakeAgentExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	return path
}

func waitForTaskState(t *testing.T, d *Daemon, kind, subject string, want tasks.State) *tasks.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, err := d.compactRunner.Get(tasks.TaskID(kind, subject))
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task != nil && task.State == want {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s:%s did not reach %s (last=%+v)", kind, subject, want, task)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// --- summarize_session executor ---

func TestSummarizeSessionExecutorVerifiesDigestLedger(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "session-1", Label: "session-1", Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	root := installNotebookNarrationRunner(t, d)

	// A solo session (no workspace) lands its digest under the reserved _solo bucket.
	soloBucket := filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket)
	digest := filepath.Join(soloBucket, "session-1.md")

	// The fake agent writes the digest where the prompt told it to: success.
	d.summarizeSessionExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if err := os.WriteFile(digest, []byte("# Session Digest\n"), 0o644); err != nil {
			t.Fatalf("fake write digest: %v", err)
		}
		// The prompt points the agent at the per-workspace bucket path.
		if !strings.Contains(req.Prompt, "RAW_DIGEST_PATH: "+digest) {
			t.Fatalf("prompt RAW_DIGEST_PATH not the bucketed path:\n%s", req.Prompt)
		}
		// The request widens to the digest's bucket dir for a Codex-backed narrate.
		if len(req.ExtraWritableRoots) != 1 || req.ExtraWritableRoots[0] != soloBucket {
			t.Fatalf("ExtraWritableRoots = %v, want [%s]", req.ExtraWritableRoots, soloBucket)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookSummarizeSessionKind, "session-1", tasks.StateDone)

	if _, err := os.Stat(digest); err != nil {
		t.Fatalf("digest not written: %v", err)
	}
}

func TestSummarizeSessionExecutorFailsWhenDigestMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "session-1", Label: "session-1", Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	installNotebookNarrationRunner(t, d)

	// The agent "succeeds" but writes nothing: the file is the ledger, so the run
	// must fail (it goes failed/requeued, never done on the first attempt).
	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "claimed done"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookSummarizeSessionKind, "session-1")
	if !strings.Contains(task.LastError, "did not write digest") {
		t.Fatalf("last error = %q, want digest-missing", task.LastError)
	}
}

func TestSummarizeSessionExecutorSkipsRemovedSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	installNotebookNarrationRunner(t, d)

	executed := false
	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		executed = true
		return agentdriver.HeadlessTaskResult{}, nil
	}

	// No session row exists -> the executor no-ops successfully (nothing to retry).
	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "gone-session", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookSummarizeSessionKind, "gone-session", tasks.StateDone)
	if executed {
		t.Fatal("executor ran the agent for a removed session")
	}
}

// TestSummarizeSessionExecutorUsesCarriedMetaWhenRowGone is the core fix: after a
// single-session-workspace teardown deletes BOTH the session row and the workspace
// row, the debounced summarize must still write the digest to the workspace's bucket
// (RawSessionsDir/<wsID>/<sid>.md), NOT the _solo bucket — using the transcript path
// and workspace id carried on the task, since neither row exists to re-derive from.
func TestSummarizeSessionExecutorUsesCarriedMetaWhenRowGone(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	// NO session row and NO workspace row: the teardown already removed both. Block
	// any narrate the re-narrate hook enqueues so it cannot race to done/fail.
	d.narrateWorkspaceExecution = blockingExecution(t)

	carriedTranscript := filepath.Join(t.TempDir(), "final-turn.jsonl")
	if err := os.WriteFile(carriedTranscript, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	wsBucket := filepath.Join(notebook.RawSessionsDir(root), "ws-gone")
	digest := filepath.Join(wsBucket, "session-1.md")
	soloDigest := filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket, "session-1.md")

	d.summarizeSessionExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		// The carried transcript (not a re-derived one) flows into the prompt.
		if !strings.Contains(req.Prompt, "TRANSCRIPT_PATH: "+carriedTranscript) {
			t.Fatalf("prompt did not use carried transcript path:\n%s", req.Prompt)
		}
		// The carried workspace id routes the digest to the workspace bucket.
		if !strings.Contains(req.Prompt, "RAW_DIGEST_PATH: "+digest) {
			t.Fatalf("digest not routed to workspace bucket:\n%s", req.Prompt)
		}
		if err := os.WriteFile(digest, []byte("# Final session digest\n"), 0o644); err != nil {
			t.Fatalf("fake write digest: %v", err)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{
		Meta: map[string]string{
			notebookSummarizeMetaTranscript: carriedTranscript,
			notebookSummarizeMetaWorkspace:  "ws-gone",
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookSummarizeSessionKind, "session-1", tasks.StateDone)

	if _, err := os.Stat(digest); err != nil {
		t.Fatalf("digest not written to workspace bucket: %v", err)
	}
	if _, err := os.Stat(soloDigest); err == nil {
		t.Fatal("digest leaked into the _solo bucket instead of the workspace bucket")
	}
}

// TestSummarizeSessionReNarratesWhenWorkspaceRemoved proves the timing-gap hook: a
// successful digest write for a session whose workspace ROW IS GONE re-enqueues a
// zero-debounce narrate_workspace so the removal retrospective is rewritten with the
// now-available digest.
func TestSummarizeSessionReNarratesWhenWorkspaceRemoved(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	// Block narrate so the re-enqueued record stays observable instead of running.
	d.narrateWorkspaceExecution = blockingExecution(t)

	carriedTranscript := filepath.Join(t.TempDir(), "turn.jsonl")
	if err := os.WriteFile(carriedTranscript, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	digest := filepath.Join(notebook.RawSessionsDir(root), "ws-gone", "session-1.md")
	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if err := os.WriteFile(digest, []byte("# digest\n"), 0o644); err != nil {
			t.Fatalf("fake write digest: %v", err)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	// No workspace row for ws-gone, both rows gone -> the hook should re-narrate.
	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{
		Meta: map[string]string{
			notebookSummarizeMetaTranscript: carriedTranscript,
			notebookSummarizeMetaWorkspace:  "ws-gone",
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookSummarizeSessionKind, "session-1", tasks.StateDone)

	if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-gone") {
		t.Fatal("digest success for a removed workspace did not re-enqueue a narrate")
	}
}

// TestSummarizeSessionDoesNotReNarrateWhenWorkspacePresent proves the hook is scoped
// to removal: a successful digest for a session whose workspace row STILL EXISTS must
// NOT burn an extra strong-tier narrate (the active workspace's pending narrate
// already covers the fresh digest).
func TestSummarizeSessionDoesNotReNarrateWhenWorkspacePresent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-live")
	root := installNotebookNarrationRunner(t, d)
	d.narrateWorkspaceExecution = blockingExecution(t)

	digest := filepath.Join(notebook.RawSessionsDir(root), "ws-live", "session-1.md")
	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if err := os.WriteFile(digest, []byte("# digest\n"), 0o644); err != nil {
			t.Fatalf("fake write digest: %v", err)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{
		Meta: map[string]string{notebookSummarizeMetaWorkspace: "ws-live"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookSummarizeSessionKind, "session-1", tasks.StateDone)

	// The workspace is alive -> no re-narrate. Give the worker a beat; the narrate
	// record must never appear.
	time.Sleep(20 * time.Millisecond)
	task, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, "ws-live"))
	if err != nil {
		t.Fatalf("get narrate: %v", err)
	}
	if task != nil {
		t.Fatalf("live workspace unexpectedly got a re-narrate task: %+v", task)
	}
}

// --- narrate_workspace executor ---

func TestNarrateWorkspaceExecutorActiveDayVerifiesMarker(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	d.narrateWorkspaceExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		// Active day -> the live workspace row exists -> IS_REMOVAL_PASS false.
		if !strings.Contains(req.Prompt, "IS_REMOVAL_PASS: false") {
			t.Fatalf("expected active-day prompt, got removal flag set")
		}
		// The Codex sandbox widens to the whole notebook root.
		if len(req.ExtraWritableRoots) != 1 || req.ExtraWritableRoots[0] != root {
			t.Fatalf("ExtraWritableRoots = %v, want [%s]", req.ExtraWritableRoots, root)
		}
		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		body := "## Chifplace — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nDid work.\n"
		if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
			t.Fatalf("fake write journal: %v", err)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", tasks.StateDone)

	journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
	content, err := os.ReadFile(journal)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if !strings.Contains(string(content), "attn:wsnarr:ws-1") {
		t.Fatalf("journal missing workspace marker:\n%s", content)
	}
}

func TestNarrateWorkspaceExecutorRemovalPassDerivesFlagFromAbsentRow(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }
	// No workspace row for ws-removed -> IS_REMOVAL_PASS must be derived true.

	var sawRemoval bool
	d.narrateWorkspaceExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		sawRemoval = strings.Contains(req.Prompt, "IS_REMOVAL_PASS: true")
		// Title falls back to the id when the row is gone.
		if !strings.Contains(req.Prompt, "WORKSPACE_TITLE: ws-removed") {
			t.Fatalf("removal prompt did not fall back title to id:\n%s", req.Prompt)
		}
		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		body := "## ws-removed — 2026-06-15\n<!-- attn:wsnarr:ws-removed -->\n\nRetrospective.\n"
		if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
			t.Fatalf("fake write journal: %v", err)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-removed", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookNarrateWorkspaceKind, "ws-removed", tasks.StateDone)
	if !sawRemoval {
		t.Fatal("removal pass did not derive IS_REMOVAL_PASS=true from absent workspace row")
	}
}

func TestNarrateWorkspaceExecutorFailsWhenMarkerMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	// Agent writes the day's file but NOT this workspace's marker -> ledger says no.
	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		if err := os.WriteFile(journal, []byte("## Other — 2026-06-15\n<!-- attn:wsnarr:other-ws -->\n"), 0o644); err != nil {
			t.Fatalf("fake write journal: %v", err)
		}
		return agentdriver.HeadlessTaskResult{Diagnostics: "wrote wrong marker"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookNarrateWorkspaceKind, "ws-1")
	if !strings.Contains(task.LastError, "did not write") {
		t.Fatalf("last error = %q, want marker-missing", task.LastError)
	}
}

// waitForNonDoneFailure waits until a task has recorded a failure (LastError set
// and not done). The runner auto-requeues failed tasks, so the task may cycle
// failed->queued->running; we only assert it recorded the failure at least once.
func waitForNonDoneFailure(t *testing.T, d *Daemon, kind, subject string) *tasks.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, err := d.compactRunner.Get(tasks.TaskID(kind, subject))
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task != nil && task.LastError != "" && task.State != tasks.StateDone {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s:%s never recorded a failure (last=%+v)", kind, subject, task)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// --- triggers ---

// TestHandleStopEnqueuesNarrationForWorkspaceSession proves a Stop on a workspace
// session enqueues BOTH a per-session digest and a coalesced workspace narrate.
func TestHandleStopEnqueuesNarrationForWorkspaceSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	installNotebookNarrationRunner(t, d)
	// Block both executors so the enqueued records stay observable.
	d.summarizeSessionExecution = blockingExecution(t)
	d.narrateWorkspaceExecution = blockingExecution(t)

	d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1"})

	if !taskExists(t, d, notebookSummarizeSessionKind, "session-1") {
		t.Fatal("stop did not enqueue summarize_session")
	}
	if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-1") {
		t.Fatal("stop did not enqueue narrate_workspace for the live workspace")
	}
}

// TestHandleStopEnqueuesOnlyDigestForSoloSession proves a Stop on a session with no
// workspace enqueues the digest but no workspace narrate.
func TestHandleStopEnqueuesOnlyDigestForSoloSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "solo", Label: "solo", Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	installNotebookNarrationRunner(t, d)
	d.summarizeSessionExecution = blockingExecution(t)

	d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "solo"})

	if !taskExists(t, d, notebookSummarizeSessionKind, "solo") {
		t.Fatal("stop did not enqueue summarize_session for solo session")
	}
	// No workspace -> no narrate task at all.
	task, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, ""))
	if err != nil {
		t.Fatalf("get narrate: %v", err)
	}
	if task != nil {
		t.Fatalf("solo session unexpectedly enqueued a narrate task: %+v", task)
	}
}

// TestHandleStopStashesTranscriptAndWorkspaceInMeta proves the Stop trigger carries
// the transcript path and the workspace id onto the summarize task's Meta, where both
// the session row and the workspace row still exist — so the debounced run can still
// resolve them after a teardown deletes both rows.
func TestHandleStopStashesTranscriptAndWorkspaceInMeta(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	installNotebookNarrationRunner(t, d)
	// Block both executors so the enqueued summarize record stays observable.
	d.summarizeSessionExecution = blockingExecution(t)
	d.narrateWorkspaceExecution = blockingExecution(t)

	transcript := filepath.Join(t.TempDir(), "turn.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1", TranscriptPath: transcript})

	if !taskExists(t, d, notebookSummarizeSessionKind, "session-1") {
		t.Fatal("stop did not enqueue summarize_session")
	}
	task, err := d.compactRunner.Get(tasks.TaskID(notebookSummarizeSessionKind, "session-1"))
	if err != nil || task == nil {
		t.Fatalf("get summarize task: %v", err)
	}
	if task.Meta[notebookSummarizeMetaTranscript] != transcript {
		t.Fatalf("summarize Meta transcript = %q, want %q", task.Meta[notebookSummarizeMetaTranscript], transcript)
	}
	if task.Meta[notebookSummarizeMetaWorkspace] != "ws-1" {
		t.Fatalf("summarize Meta workspace = %q, want ws-1", task.Meta[notebookSummarizeMetaWorkspace])
	}
}

// TestWorkspaceRemovalEnqueuesFinalNarrateWithZeroDebounce proves the removal
// boundary enqueues the final retrospective narrate that overrides a pending
// active-day debounce (ZeroDebounce -> NextAttemptAt is not pushed forward).
func TestWorkspaceRemovalEnqueuesFinalNarrate(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	installNotebookNarrationRunner(t, d)
	d.narrateWorkspaceExecution = blockingExecution(t)

	// Pre-seed a far-future debounced active-day narrate.
	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{Debounce: time.Hour}); err != nil {
		t.Fatalf("seed pending narrate: %v", err)
	}
	before, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, "ws-1"))
	if err != nil || before == nil {
		t.Fatalf("get seeded task: %v", err)
	}

	// Remove the workspace (the app's UnregisterWorkspace path).
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "ws-1"})

	if d.store.GetWorkspace("ws-1") != nil {
		t.Fatal("workspace not removed")
	}
	after, err := d.compactRunner.Get(tasks.TaskID(notebookNarrateWorkspaceKind, "ws-1"))
	if err != nil || after == nil {
		t.Fatalf("get final task: %v", err)
	}
	// ZeroDebounce overrode the hour-long debounce: the final attempt is no later
	// than the seeded one (in practice much sooner / now).
	if after.NextAttemptAt.After(before.NextAttemptAt) {
		t.Fatalf("final narrate did not override the pending debounce: before=%s after=%s",
			before.NextAttemptAt, after.NextAttemptAt)
	}
}

// TestNarrationTriggersAreNilSafeBeforeRunner proves the Stop and removal triggers
// tolerate a nil compactRunner (the window before startCompactRunner runs).
func TestNarrationTriggersAreNilSafeBeforeRunner(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "ws-1")
	d.compactRunner = nil // mimic production before startCompactRunner

	// Neither must panic.
	d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1"})
	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "ws-1"})

	if d.store.GetWorkspace("ws-1") != nil {
		t.Fatal("workspace not removed after nil-runner unregister")
	}
}

// blockingExecution returns an execution hook that blocks until the test ends, so
// an enqueued task that the worker picks up stays observable as running rather than
// racing to done/failed before the assertion reads it.
func blockingExecution(t *testing.T) func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return func(ctx context.Context, _ agentdriver.HeadlessTaskProvider, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return agentdriver.HeadlessTaskResult{}, ctx.Err()
	}
}

// --- path-traversal guard (the load-bearing security property) ---

// assertJournalUntouched fails the test if ANY entry appears under <root>/journal/
// (the curated journal must stay empty in these raw-tier traversal tests), so a
// path-traversal write that escaped the raw tier into the journal dir is caught. An
// absent journal dir is fine — nothing escaped into it.
func assertJournalUntouched(t *testing.T, root string) {
	t.Helper()
	journalDir := filepath.Join(root, notebook.DirJournal)
	entries, err := os.ReadDir(journalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return // no journal dir at all -> nothing escaped into it
		}
		t.Fatalf("read journal dir: %v", err)
	}
	for _, e := range entries {
		t.Fatalf("journal dir unexpectedly contains %q — a write escaped the raw tier", e.Name())
	}
}

// TestSummarizeSessionExecutorRejectsTraversalSessionID proves a crafted session id
// that filepath.Clean would resolve into the curated journal is rejected before the
// agent runs, and nothing lands under the journal dir. The base raw-floor PR added
// rawTierFilename precisely for this; this is the missing load-bearing test.
func TestSummarizeSessionExecutorRejectsTraversalSessionID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	craftedID := "../../../journal/2026-06-15"
	d.store.Add(&protocol.Session{
		ID: craftedID, Label: craftedID, Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	root := installNotebookNarrationRunner(t, d)

	ran := false
	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		ran = true
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, craftedID, tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookSummarizeSessionKind, craftedID)
	if !strings.Contains(task.LastError, "unsafe session id") {
		t.Fatalf("last error = %q, want unsafe-session-id rejection", task.LastError)
	}
	if ran {
		t.Fatal("executor spawned the agent for a traversal session id")
	}
	assertJournalUntouched(t, root)
}

// TestNarrateWorkspaceExecutorRejectsTraversalWorkspaceID proves a crafted workspace
// id is rejected (so the narrate pass is never handed a CONTEXT_SNAPSHOT_PATH/journal
// read target that climbed out of the raw tier) and nothing lands under the journal.
func TestNarrateWorkspaceExecutorRejectsTraversalWorkspaceID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	craftedID := "../../../journal/2026-06-15"
	ran := false
	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		ran = true
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, craftedID, tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookNarrateWorkspaceKind, craftedID)
	if !strings.Contains(task.LastError, "unsafe workspace id") {
		t.Fatalf("last error = %q, want unsafe-workspace-id rejection", task.LastError)
	}
	if ran {
		t.Fatal("executor spawned the agent for a traversal workspace id")
	}
	assertJournalUntouched(t, root)
}

// --- ledger freshness (no false done off a prior run's file) ---

// TestSummarizeSessionExecutorRequiresFreshDigest proves a coalesced re-run whose
// agent leaves the prior digest byte-identical is treated as a failure, not a false
// done — otherwise a stale digest (missing the new turns) would report success.
func TestSummarizeSessionExecutorRequiresFreshDigest(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "session-1", Label: "session-1", Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	root := installNotebookNarrationRunner(t, d)

	digest := filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket, "session-1.md")
	if err := os.MkdirAll(filepath.Dir(digest), 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	if err := os.WriteFile(digest, []byte("# Session Digest\n\nprior run\n"), 0o644); err != nil {
		t.Fatalf("seed prior digest: %v", err)
	}

	// Agent no-ops: leaves the prior digest exactly as-is.
	d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookSummarizeSessionKind, "session-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookSummarizeSessionKind, "session-1")
	if !strings.Contains(task.LastError, "unchanged") {
		t.Fatalf("last error = %q, want unchanged-digest rejection", task.LastError)
	}
}

// TestNarrateWorkspaceExecutorRequiresFreshEntry proves the removal retrospective
// is not silently dropped when an active-day entry already exists: a re-run whose
// agent leaves this workspace's marker block byte-identical fails (and is retried),
// instead of being marked done off the prior block.
func TestNarrateWorkspaceExecutorRequiresFreshEntry(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatalf("mkdir journal: %v", err)
	}
	prior := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nactive-day entry\n\nsource: workspace:ws-1\n"
	if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed prior entry: %v", err)
	}

	// Removal-pass agent no-ops: leaves the active-day block exactly as-is.
	d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task := waitForNonDoneFailure(t, d, notebookNarrateWorkspaceKind, "ws-1")
	if !strings.Contains(task.LastError, "unchanged") {
		t.Fatalf("last error = %q, want unchanged-entry rejection", task.LastError)
	}
}

// --- marker prefix collision (sibling whose id is a prefix) ---

// TestWorkspaceNarrationBlockNoPrefixCollision proves ws-1's success ledger is not
// satisfied by ws-10's entry. A bare-substring match would falsely verify ws-1 off
// `<!-- attn:wsnarr:ws-10 -->`; the full delimited marker line does not.
func TestWorkspaceNarrationBlockNoPrefixCollision(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "2026-06-15.md")
	// Only ws-10 has an entry. ws-1 is a prefix of ws-10.
	body := "## ws-10 — 2026-06-15\n<!-- attn:wsnarr:ws-10 -->\n\nsibling entry\n\nsource: workspace:ws-10\n"
	if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	got, err := workspaceNarrationBlock(journal, "ws-1")
	if err != nil {
		t.Fatalf("workspaceNarrationBlock: %v", err)
	}
	if got.present {
		t.Fatal("ws-1 ledger falsely verified off ws-10's entry (prefix collision)")
	}

	// ws-10 itself is correctly found.
	got, err = workspaceNarrationBlock(journal, "ws-10")
	if err != nil {
		t.Fatalf("workspaceNarrationBlock ws-10: %v", err)
	}
	if !got.present || !strings.Contains(got.body, "sibling entry") {
		t.Fatalf("ws-10 ledger did not find its own entry: %+v", got)
	}
}

// --- per-workspace digest scoping (no cross-workspace contamination) ---

// TestNarrateWorkspaceScopesSessionsToWorkspace proves each narrate pass is handed
// only its OWN workspace's digest bucket, so one workspace's journal entry cannot be
// contaminated with a sibling's (or a solo session's) digests.
func TestNarrateWorkspaceScopesSessionsToWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-a", "ws-1")
	root := installNotebookNarrationRunner(t, d)
	d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

	wantDir, err := notebookWorkspaceSessionsDir(root, "ws-1")
	if err != nil {
		t.Fatalf("workspace sessions dir: %v", err)
	}
	if wantDir == notebook.RawSessionsDir(root) {
		t.Fatal("per-workspace dir collapsed to the shared flat sessions dir")
	}

	d.narrateWorkspaceExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		if !strings.Contains(req.Prompt, "RAW_SESSIONS_DIR: "+wantDir) {
			t.Fatalf("narrate prompt RAW_SESSIONS_DIR not scoped to ws-1:\n%s", req.Prompt)
		}
		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		body := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nwork.\n"
		if err := os.WriteFile(journal, []byte(body), 0o644); err != nil {
			t.Fatalf("fake write journal: %v", err)
		}
		return agentdriver.HeadlessTaskResult{}, nil
	}

	if _, err := d.compactRunner.Enqueue(notebookNarrateWorkspaceKind, "ws-1", tasks.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", tasks.StateDone)
}

// --- config-time validation (fail fast, not mid-run hang) ---

// TestDaemon_ValidatesNotebookNarrationAgentAndExecutable mirrors the keeper compaction
// validate test: a valid config passes, a missing configured executable is rejected
// at config time, a blank value validates (tier default), and invalid JSON is
// rejected through the handler-facing validateSetting route — for BOTH narration
// keys.
func TestDaemon_ValidatesNotebookNarrationAgentAndExecutable(t *testing.T) {
	tempDir := t.TempDir()
	executable := filepath.Join(tempDir, "custom-claude")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", tempDir)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), "custom-claude")

	for _, key := range []string{SettingNotebookSummarizeSession, SettingNotebookNarrateWorkspace} {
		// Blank validates (tier default), but the default agent must still resolve.
		if err := d.validateSetting(key, ""); err != nil {
			t.Fatalf("%s: blank rejected: %v", key, err)
		}
		// Explicit valid config passes.
		if err := d.validateSetting(key, `{"agent":"claude","model":"m"}`); err != nil {
			t.Fatalf("%s: valid config rejected: %v", key, err)
		}
		// Invalid JSON is rejected through the handler route.
		if err := d.validateSetting(key, `{"agent":"claude"`); err == nil {
			t.Fatalf("%s: invalid JSON accepted", key)
		}
	}

	// A missing configured executable is rejected at config time.
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), "missing-claude")
	if err := d.validateSetting(SettingNotebookNarrateWorkspace, `{"agent":"claude","model":"m"}`); err == nil {
		t.Fatal("narrate setting accepted a missing configured executable")
	}
}

func taskExists(t *testing.T, d *Daemon, kind, subject string) bool {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		task, err := d.compactRunner.Get(tasks.TaskID(kind, subject))
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}
