package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

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
