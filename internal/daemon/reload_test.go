package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// writeClaudeTranscriptFixture points HOME at a temp dir and writes a Claude
// transcript for sessionID so FindClaudeTranscript (which walks ~/.claude/projects)
// treats the session as resumable. Without it a reload-resume id with no transcript
// on disk is correctly downgraded to a fresh spawn.
func writeClaudeTranscriptFixture(t *testing.T, sessionID string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	projDir := filepath.Join(home, ".claude", "projects", "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projDir, sessionID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript fixture: %v", err)
	}
}

// fakeReloadBackend records the kill/remove/spawn orchestration and serves the
// SessionInfo (geometry) + SessionLaunchParams (registry) the reload path reads.
type fakeReloadBackend struct {
	mu        sync.Mutex
	liveIDs   []string
	info      ptybackend.SessionInfo
	params    ptybackend.SessionLaunchParams
	paramsErr error
	spawnErr  error
	calls     []string
	spawnOpts []ptybackend.SpawnOptions
	spawnGate *rendezvous // optional: forces concurrent reloads to collide at Spawn
}

// rendezvous is a best-effort barrier: arrivals release together once `want` of
// them gather, or fall through individually after `timeout`. It lets the concurrency
// test deterministically force two unsynchronized reloads to reach Spawn together
// (reproducing the "already exists" tear-down) while a correctly serialized reload —
// where only one goroutine is ever in the composite — simply times out and proceeds
// alone instead of deadlocking.
type rendezvous struct {
	want    int
	timeout time.Duration
	mu      sync.Mutex
	count   int
	release chan struct{}
}

func newRendezvous(want int, timeout time.Duration) *rendezvous {
	return &rendezvous{want: want, timeout: timeout, release: make(chan struct{})}
}

func (r *rendezvous) arrive() {
	r.mu.Lock()
	r.count++
	if r.count >= r.want {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	select {
	case <-r.release:
	case <-time.After(r.timeout):
	}
}

// Spawn models the real backend's liveness: it rejects an id that is still live
// ("already exists"). With kill/remove/spawn serialized per session this never
// fires; if two reloads interleave, the Spawn loser hits it — which is exactly the
// tear-down the per-session reload lock exists to prevent.
func (b *fakeReloadBackend) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	if b.spawnGate != nil {
		// Arrive BEFORE taking the liveness lock so two collided reloads (both having
		// already cleared the id via their own Kill/Remove) decide add-vs-"already
		// exists" against the same empty state.
		b.spawnGate.arrive()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, "spawn:"+opts.ID)
	if b.spawnErr != nil {
		return b.spawnErr
	}
	for _, id := range b.liveIDs {
		if id == opts.ID {
			return fmt.Errorf("session %s already exists", opts.ID)
		}
	}
	b.liveIDs = append(b.liveIDs, opts.ID)
	b.spawnOpts = append(b.spawnOpts, opts)
	return nil
}
func (b *fakeReloadBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{Running: true}, newFakeOutputStream(), nil
}
func (b *fakeReloadBackend) Input(context.Context, string, []byte) error          { return nil }
func (b *fakeReloadBackend) Resize(context.Context, string, uint16, uint16) error { return nil }
func (b *fakeReloadBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *fakeReloadBackend) Kill(_ context.Context, id string, _ syscall.Signal) error {
	b.mu.Lock()
	b.calls = append(b.calls, "kill:"+id)
	b.liveIDs = removeReloadID(b.liveIDs, id)
	b.mu.Unlock()
	return nil
}
func (b *fakeReloadBackend) Remove(_ context.Context, id string) error {
	b.mu.Lock()
	b.calls = append(b.calls, "remove:"+id)
	b.liveIDs = removeReloadID(b.liveIDs, id)
	b.mu.Unlock()
	return nil
}

func removeReloadID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}
func (b *fakeReloadBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.liveIDs...)
}
func (b *fakeReloadBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *fakeReloadBackend) Shutdown(context.Context) error { return nil }
func (b *fakeReloadBackend) SessionInfo(context.Context, string) (ptybackend.SessionInfo, error) {
	return b.info, nil
}
func (b *fakeReloadBackend) SessionLaunchParams(context.Context, string) (ptybackend.SessionLaunchParams, error) {
	return b.params, b.paramsErr
}

func (b *fakeReloadBackend) callOrder() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}
func (b *fakeReloadBackend) lastSpawn() (ptybackend.SpawnOptions, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.spawnOpts) == 0 {
		return ptybackend.SpawnOptions{}, false
	}
	return b.spawnOpts[len(b.spawnOpts)-1], true
}
func (b *fakeReloadBackend) spawnCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.spawnOpts)
}
func (b *fakeReloadBackend) spawnCountFor(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, opts := range b.spawnOpts {
		if opts.ID == id {
			n++
		}
	}
	return n
}

func newReloadTestDaemon(t *testing.T, backend *fakeReloadBackend) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = backend
	return d
}

func addReloadSession(d *Daemon, id string, agent protocol.SessionAgent, state protocol.SessionState) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: agent, Directory: "/tmp/" + id,
		WorkspaceID: "ws-" + id, State: state, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

// A reload re-spawns the live agent in place, preserving the transcript (resume)
// and the launch flags the daemon does not otherwise persist (yolo, executable),
// then announces runtime_respawned. The killed worker's exit is suppressed so the
// reload reads as a runtime replacement, not a session close.
func TestReloadSessionAgentRespawnsWithResumeAndPreservedLaunchParams(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"chief"},
		info:    ptybackend.SessionInfo{Cols: 120, Rows: 40},
		params:  ptybackend.SessionLaunchParams{Recorded: true, YoloMode: true, Executable: "/custom/claude"},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.persistResumeSessionID("chief", "resume-xyz")
	// The resume target must have a transcript on disk to be resumable; otherwise
	// the reload correctly downgrades to a fresh spawn (see the fresh-spawn test).
	writeClaudeTranscriptFixture(t, "resume-xyz")

	var respawned, exited bool
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) {
		if e == nil {
			return
		}
		switch e.Event {
		case protocol.EventRuntimeRespawned:
			if protocol.Deref(e.ID) == "chief" {
				respawned = true
			}
		case protocol.EventSessionExited:
			if protocol.Deref(e.ID) == "chief" {
				exited = true
			}
		}
	}

	d.reloadSessionAgent("chief")

	if order := backend.callOrder(); !reflect.DeepEqual(order, []string{"kill:chief", "remove:chief", "spawn:chief"}) {
		t.Fatalf("orchestration order = %v, want [kill remove spawn]", order)
	}
	opts, ok := backend.lastSpawn()
	if !ok {
		t.Fatal("no respawn recorded")
	}
	if opts.ResumeSessionID != "resume-xyz" {
		t.Fatalf("ResumeSessionID = %q, want resume-xyz (transcript preserved)", opts.ResumeSessionID)
	}
	if !opts.YoloMode {
		t.Fatal("YoloMode must be preserved across reload")
	}
	if opts.Executable != "/custom/claude" {
		t.Fatalf("Executable = %q, want /custom/claude", opts.Executable)
	}
	if opts.Cols != 120 || opts.Rows != 40 {
		t.Fatalf("geometry = %dx%d, want 120x40 (live SessionInfo)", opts.Cols, opts.Rows)
	}
	if !respawned {
		t.Fatal("expected a runtime_respawned broadcast")
	}

	// The killed worker's async exit must be suppressed (not a session close).
	d.handlePTYExit(ptybackend.ExitInfo{ID: "chief"})
	if exited {
		t.Fatal("session_exited must be suppressed for a reloading session")
	}
	if d.consumeReloading("chief") {
		t.Fatal("the suppressed exit should have consumed the reloading flag")
	}
}

// A chief promoted before it ever took a turn has a resume id (its own session id,
// assigned at spawn) pointing at a transcript Claude has not written yet. Resuming
// it would exit non-zero (a dead chief), so the reload must downgrade to a fresh
// spawn — which reuses --session-id and preserves the session identity.
func TestReloadSessionAgentFreshSpawnsWhenNotResumable(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"chief"},
		info:    ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	// A resume id with NO transcript on disk: point HOME at an empty temp home so
	// FindClaudeTranscript finds nothing for this id.
	d.persistResumeSessionID("chief", "chief")
	t.Setenv("HOME", t.TempDir())

	d.reloadSessionAgent("chief")

	opts, ok := backend.lastSpawn()
	if !ok {
		t.Fatal("no respawn recorded")
	}
	if opts.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty (fresh spawn — nothing to resume)", opts.ResumeSessionID)
	}
}

// Resume restores the transcript, not launch flags. A worker that did not record
// its launch params (pre-reload build) must NOT be respawned with defaulted flags
// — a yolo chief would silently come back asking permissions. Abort instead and
// leave the live worker untouched.
func TestReloadSessionAgentAbortsWhenLaunchParamsNotRecorded(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"chief"},
		info:    ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:  ptybackend.SessionLaunchParams{Recorded: false, YoloMode: true},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.reloadSessionAgent("chief")

	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("expected no kill/remove/spawn when launch params unrecorded, got %v", order)
	}
	if d.consumeReloading("chief") {
		t.Fatal("reloading flag must not be set when the reload aborts")
	}
}

// A reload reconstructs SpawnOptions from scratch, so it must re-attach the chief
// context-window cap or a runtime reload/respawn would silently bring a chief back
// UNCAPPED even though a fresh chief launch is capped. The cap is keyed on the
// persisted chief role (not a spawn-time request flag), which is the same source
// the wrapper's NotebookGuide RPC uses to decide chief-ness — so the reloaded cap
// and the reloaded guidance stay consistent. Ordinary/delegated sessions stay
// uncapped through reload even when a cap is configured.
func TestBuildReloadSpawnOptionsCarriesChiefContextWindowCap(t *testing.T) {
	// No transcript on disk → deterministic resume resolution (fresh-spawn), which
	// keeps buildReloadSpawnOptions from depending on a resumable transcript.
	t.Setenv("HOME", t.TempDir())

	newDaemonWithSession := func(t *testing.T, sessionID string) *Daemon {
		t.Helper()
		backend := &fakeReloadBackend{params: ptybackend.SessionLaunchParams{Recorded: true}}
		d := newReloadTestDaemon(t, backend)
		addReloadSession(d, sessionID, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		return d
	}

	t.Run("reloaded chief keeps the configured cap", func(t *testing.T) {
		d := newDaemonWithSession(t, "chief")
		if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
			t.Fatalf("assign chief role: %v", err)
		}
		d.store.SetSetting(SettingChiefContextWindowCap, "160000")

		opts, err := d.buildReloadSpawnOptions(d.store.Get("chief"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ChiefContextWindowCap != 160000 {
			t.Fatalf("ChiefContextWindowCap = %d, want 160000 (reloaded chief must stay capped)", opts.ChiefContextWindowCap)
		}
	})

	t.Run("reloaded chief with no configured cap falls back to the default", func(t *testing.T) {
		d := newDaemonWithSession(t, "chief")
		if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
			t.Fatalf("assign chief role: %v", err)
		}

		opts, err := d.buildReloadSpawnOptions(d.store.Get("chief"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ChiefContextWindowCap != agentdriver.DefaultContextWindowCap {
			t.Fatalf("ChiefContextWindowCap = %d, want default %d", opts.ChiefContextWindowCap, agentdriver.DefaultContextWindowCap)
		}
	})

	t.Run("reloaded non-chief session stays uncapped even with a cap configured", func(t *testing.T) {
		d := newDaemonWithSession(t, "worker")
		// A configured chief cap must not leak onto a delegated/ordinary reload.
		d.store.SetSetting(SettingChiefContextWindowCap, "160000")

		opts, err := d.buildReloadSpawnOptions(d.store.Get("worker"))
		if err != nil {
			t.Fatalf("buildReloadSpawnOptions: %v", err)
		}
		if opts.ChiefContextWindowCap != 0 {
			t.Fatalf("ChiefContextWindowCap = %d, want 0 (non-chief reload must stay uncapped)", opts.ChiefContextWindowCap)
		}
	})
}

func TestReloadSessionAgentSkipsWhenNoLiveWorker(t *testing.T) {
	backend := &fakeReloadBackend{liveIDs: nil, params: ptybackend.SessionLaunchParams{Recorded: true}}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	d.reloadSessionAgent("chief")

	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("expected no-op for a session with no live worker, got %v", order)
	}
}

func TestReloadSessionAgentSkipsUnsupportedAgent(t *testing.T) {
	backend := &fakeReloadBackend{liveIDs: []string{"chief"}, params: ptybackend.SessionLaunchParams{Recorded: true}}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgent(protocol.AgentShellValue), protocol.SessionStateIdle)

	d.reloadSessionAgent("chief")

	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("expected no reload for an agent without a chief-guidance launch path, got %v", order)
	}
}

func TestReloadSessionAgentRecomposesPluginChiefInstructionsBeforeKill(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"plugin-chief"},
		info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
		params: ptybackend.SessionLaunchParams{
			Recorded: true,
			YoloMode: true,
			Model:    "provider/model",
			Effort:   "high",
		},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-plugin-chief", t.TempDir())
	addReloadSession(d, "plugin-chief", protocol.SessionAgent("opencode"), protocol.SessionStateIdle)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "plugin-chief"); err != nil {
		t.Fatalf("assign chief role: %v", err)
	}
	if !d.store.BeginAgentDriverRun("plugin-chief", "opencode-plugin", "run-old") {
		t.Fatal("begin old plugin run")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-chief", "run-old", 1, `{"native_id":"same-session"}`) {
		t.Fatal("seed plugin metadata")
	}
	plugin, done := startPluginPipe(t, d, "opencode-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "opencode", map[string]bool{
		"resume": true, "yolo": true, "model_pin": true, "effort_pin": true, "launch_instructions": true,
	})
	closed := make(chan pluginDriverSessionClosedParams, 1)
	go func() {
		request := decodeJSONRPCMessage(t, plugin)
		if request.Method != "driver.resume" {
			t.Errorf("method=%q, want driver.resume", request.Method)
			return
		}
		var params pluginDriverSpawnParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode resume params: %v", err)
			return
		}
		if params.Instructions == nil || params.Instructions.Kind != pluginInstructionKindChief || !strings.Contains(params.Instructions.Content, "You are the chief of staff") {
			t.Errorf("resume instructions=%+v, want current chief guidance", params.Instructions)
			return
		}
		if params.Model != "provider/model" || params.Effort != "high" || !params.Yolo || string(params.Metadata) != `{"native_id":"same-session"}` {
			t.Errorf("resume params=%+v, want preserved flags and metadata", params)
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"opencode-launcher"}})
		request = decodeJSONRPCMessage(t, plugin)
		var closeParams pluginDriverSessionClosedParams
		if err := json.Unmarshal(request.Params, &closeParams); err != nil {
			t.Errorf("decode session_closed params: %v", err)
			return
		}
		respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
		closed <- closeParams
	}()

	d.reloadSessionAgent("plugin-chief")

	if order := backend.callOrder(); !reflect.DeepEqual(order, []string{"kill:plugin-chief", "remove:plugin-chief", "spawn:plugin-chief"}) {
		t.Fatalf("orchestration order=%v", order)
	}
	spawn, ok := backend.lastSpawn()
	if !ok || !reflect.DeepEqual(spawn.ExternalCommand, []string{"opencode-launcher"}) || spawn.LifecycleID == "" {
		t.Fatalf("plugin respawn=%+v", spawn)
	}
	if active := d.store.GetAgentDriverRun("plugin-chief"); active.RunID != spawn.LifecycleID || active.PluginName != "opencode-plugin" {
		t.Fatalf("active plugin run=%+v, want replacement", active)
	}
	select {
	case params := <-closed:
		if params.RunID != "run-old" || params.Reason != "reloaded" {
			t.Fatalf("closed old run=%+v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("old plugin run was not closed after replacement")
	}
}

func TestReloadSessionAgentLeavesPluginWorkerAliveWhenResumeCannotBePrepared(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"plugin-chief"},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addTestWorkspace(d, "ws-plugin-chief", t.TempDir())
	addReloadSession(d, "plugin-chief", protocol.SessionAgent("opencode"), protocol.SessionStateIdle)
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "plugin-chief"); err != nil {
		t.Fatalf("assign chief role: %v", err)
	}
	plugin, done := startPluginPipe(t, d, "opencode-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "opencode", map[string]bool{"resume": true, "launch_instructions": true})
	closed := make(chan struct{})
	go func() {
		request := decodeJSONRPCMessage(t, plugin)
		_ = json.NewEncoder(plugin).Encode(jsonRPCMessage{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error:   &jsonRPCError{Code: jsonRPCInternalError, Message: "native resume unavailable"},
		})
		request = decodeJSONRPCMessage(t, plugin)
		respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
		close(closed)
	}()

	d.reloadSessionAgent("plugin-chief")

	if order := backend.callOrder(); len(order) != 0 {
		t.Fatalf("failed preflight touched live worker: %v", order)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed replacement run was not cleaned up")
	}
}

func TestSetChiefOfStaffRejectsPluginRoleChangeWhenResumePreflightFails(t *testing.T) {
	for _, test := range []struct {
		name         string
		promote      bool
		initialChief string
		wantChief    string
		wantKind     string
	}{
		{name: "promotion", promote: true, wantKind: pluginInstructionKindChief},
		{name: "demotion", promote: false, initialChief: "plugin-chief", wantChief: "plugin-chief", wantKind: pluginInstructionKindWorkspace},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeReloadBackend{
				liveIDs: []string{"plugin-chief"},
				info:    ptybackend.SessionInfo{Cols: 100, Rows: 32},
				params:  ptybackend.SessionLaunchParams{Recorded: true},
			}
			d := newReloadTestDaemon(t, backend)
			addTestWorkspace(d, "ws-plugin-chief", t.TempDir())
			addReloadSession(d, "plugin-chief", protocol.SessionAgent("opencode"), protocol.SessionStateIdle)
			d.store.SetSetting(SettingNotebookRoot, t.TempDir())
			if test.initialChief != "" {
				if err := d.store.SetProfileRole(profileRoleChiefOfStaff, test.initialChief); err != nil {
					t.Fatalf("seed chief role: %v", err)
				}
			}
			if !d.store.BeginAgentDriverRun("plugin-chief", "opencode-plugin", "run-live") {
				t.Fatal("begin live plugin run")
			}

			plugin, done := startPluginPipe(t, d, "opencode-plugin", nil)
			defer func() {
				_ = plugin.Close()
				<-done
			}()
			registerTestPluginDriver(t, plugin, "opencode", map[string]bool{"resume": true, "launch_instructions": true})
			closed := make(chan struct{})
			go func() {
				request := decodeJSONRPCMessage(t, plugin)
				if request.Method != "driver.resume" {
					t.Errorf("method=%q, want driver.resume", request.Method)
					return
				}
				var params pluginDriverSpawnParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					t.Errorf("decode resume params: %v", err)
					return
				}
				if params.Instructions == nil || params.Instructions.Kind != test.wantKind {
					t.Errorf("instructions=%+v, want kind %q", params.Instructions, test.wantKind)
					return
				}
				_ = json.NewEncoder(plugin).Encode(jsonRPCMessage{
					JSONRPC: "2.0",
					ID:      request.ID,
					Error:   &jsonRPCError{Code: jsonRPCInternalError, Message: "native resume unavailable"},
				})
				request = decodeJSONRPCMessage(t, plugin)
				respondPluginRequest(t, plugin, request, pluginDriverSessionClosedResult{OK: true})
				close(closed)
			}()

			client := newRenameTestClient()
			d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
				Cmd: protocol.CmdSetChiefOfStaff, SessionID: "plugin-chief", ChiefOfStaff: test.promote,
			})
			result := readChiefOfStaffResult(t, client)
			if result.Success || !strings.Contains(protocol.Deref(result.Error), "native resume unavailable") {
				t.Fatalf("result=%+v, want resume preflight failure", result)
			}
			if got := d.chiefOfStaffSessionID(); got != test.wantChief {
				t.Fatalf("persisted chief role=%q, want %q", got, test.wantChief)
			}
			if order := backend.callOrder(); len(order) != 0 {
				t.Fatalf("failed preflight touched live worker: %v", order)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed prepared run was not cleaned up")
			}
		})
	}
}

// A respawn that fails after the kill must never leave a live-looking pane over a
// dead session: emit the real session_exited so the UI degrades to a dead pane.
func TestReloadSessionAgentRespawnFailureBroadcastsSessionExited(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs:  []string{"chief"},
		info:     ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:   ptybackend.SessionLaunchParams{Recorded: true},
		spawnErr: fmt.Errorf("boom"),
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	var exited bool
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) {
		if e != nil && e.Event == protocol.EventSessionExited && protocol.Deref(e.ID) == "chief" {
			exited = true
		}
	}

	d.reloadSessionAgent("chief")

	if !exited {
		t.Fatal("a failed respawn must broadcast session_exited (dead-pane fallback)")
	}
	if d.consumeReloading("chief") {
		t.Fatal("reloading flag must be cleared after a failed respawn")
	}
}

// Promotion AND demotion both reload, so the new chief status reaches the system
// prompt either way (assign injects guidance, demote drops it).
func TestSetChiefOfStaffReloadsOnAssignAndDemote(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"chief"},
		info:    ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	client := newRenameTestClient()

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "chief", ChiefOfStaff: true,
	})
	waitForSpawnCount(t, backend, 1, "assign")

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "chief", ChiefOfStaff: false,
	})
	waitForSpawnCount(t, backend, 2, "demote")
}

// Two reloads of the same session fired concurrently (a rapid double-toggle, or a
// role transfer that reloads both chiefs) must not interleave: the per-session
// reload lock serializes them so neither Spawn loses the "already exists" race and
// tears down the other's respawn. The session ends with exactly one live worker and
// no session_exited.
func TestReloadSessionAgentSerializesConcurrentReloads(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs:   []string{"chief"},
		info:      ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:    ptybackend.SessionLaunchParams{Recorded: true},
		spawnGate: newRendezvous(2, 500*time.Millisecond),
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	var exited int
	var exitedMu sync.Mutex
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) {
		if e != nil && e.Event == protocol.EventSessionExited && protocol.Deref(e.ID) == "chief" {
			exitedMu.Lock()
			exited++
			exitedMu.Unlock()
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.reloadSessionAgent("chief")
		}()
	}
	wg.Wait()

	exitedMu.Lock()
	gotExited := exited
	exitedMu.Unlock()
	if gotExited != 0 {
		t.Fatalf("concurrent reloads broadcast %d session_exited; want 0 (no tear-down)", gotExited)
	}
	if live := backend.SessionIDs(context.Background()); len(live) != 1 || live[0] != "chief" {
		t.Fatalf("live workers after concurrent reloads = %v, want exactly [chief]", live)
	}
	if backend.spawnCount() != 2 {
		t.Fatalf("spawn count = %d, want 2 (both reloads respawned, serialized)", backend.spawnCount())
	}
}

// Promoting a new chief while another session still holds the role demotes the old
// chief via the single-holder upsert. Both must reload: the new chief to gain the
// guidance, the displaced one to drop it now instead of keeping it until it restarts.
func TestSetChiefOfStaffRoleTransferReloadsBothChiefs(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"alice", "bob"},
		info:    ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "alice", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addReloadSession(d, "bob", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	client := newRenameTestClient()

	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "alice", ChiefOfStaff: true,
	})
	waitForSpawnCount(t, backend, 1, "assign alice")

	// Transfer the role to bob while alice still holds it.
	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "bob", ChiefOfStaff: true,
	})
	waitForSpawnCount(t, backend, 3, "transfer to bob")

	if got := backend.spawnCountFor("bob"); got != 1 {
		t.Fatalf("bob (new chief) respawns = %d, want 1", got)
	}
	if got := backend.spawnCountFor("alice"); got != 2 {
		t.Fatalf("alice respawns = %d, want 2 (1 assign + 1 displaced-on-transfer)", got)
	}
}

// The reload is destructive (kill + respawn), unlike the doorbell it replaced. A
// redundant toggle that changes no role must NOT kill+respawn an innocent agent:
// demoting a session that isn't the chief (a ClearProfileRole no-op), or re-assigning
// the session that already holds the role.
func TestSetChiefOfStaffNoReloadOnNoOpToggle(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"chief", "other"},
		info:    ptybackend.SessionInfo{Cols: 80, Rows: 24},
		params:  ptybackend.SessionLaunchParams{Recorded: true},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "chief", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addReloadSession(d, "other", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	client := newRenameTestClient()

	// Demote a session that holds no role: nothing changes, nothing should reload.
	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "other", ChiefOfStaff: false,
	})
	assertSpawnCountStaysBelow(t, backend, 1, "no-op demote of a non-chief")

	// Real assign reloads once.
	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "chief", ChiefOfStaff: true,
	})
	waitForSpawnCount(t, backend, 1, "assign chief")

	// Re-assigning the SAME session that already holds the role changes nothing.
	d.handleSetChiefOfStaff(client, &protocol.SetChiefOfStaffMessage{
		Cmd: protocol.CmdSetChiefOfStaff, SessionID: "chief", ChiefOfStaff: true,
	})
	assertSpawnCountStaysBelow(t, backend, 2, "redundant re-assign of the current chief")
}

// assertSpawnCountStaysBelow gives any (incorrectly fired) async reload a window to
// land, then asserts the spawn count never reached want.
func assertSpawnCountStaysBelow(t *testing.T, backend *fakeReloadBackend, want int, label string) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if got := backend.spawnCount(); got >= want {
		t.Fatalf("%s: spawn count = %d, want < %d (no-op toggle must not reload)", label, got, want)
	}
}

func waitForSpawnCount(t *testing.T, backend *fakeReloadBackend, want int, label string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if backend.spawnCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: respawn count = %d, want >= %d", label, backend.spawnCount(), want)
}
