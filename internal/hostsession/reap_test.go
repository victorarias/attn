package hostsession

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The registry is the crash-recovery net: it must exist exactly while the
// process might, so a record appears at spawn and disappears once the host is
// fully gone.
func TestSpawnWritesAndExitRemovesTheRegistryEntry(t *testing.T) {
	manager, rec := newManager(t)
	dataDir := t.TempDir()
	registryPath := RegistryPath(dataDir, "s1")
	script := writeScript(t, `
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{}}' >&3
while true; do sleep 0.05; done
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}, RegistryPath: registryPath}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	entry, err := ReadRegistry(registryPath)
	if err != nil {
		t.Fatalf("registry entry not written at spawn: %v", err)
	}
	if entry.SessionID != "s1" || entry.PID <= 0 {
		t.Fatalf("registry entry is incomplete: %+v", entry)
	}
	if entry.ProcessStartTime == "" {
		t.Fatalf("registry entry carries no process start time: %+v", entry)
	}
	current, err := processStartTime(entry.PID)
	if err != nil || current != entry.ProcessStartTime {
		t.Fatalf("recorded start time %q does not match the live process (%q, %v)", entry.ProcessStartTime, current, err)
	}

	if err := manager.Kill("s1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitForExit(t, rec)
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry entry survived the host's exit: %v", err)
	}
}

// orphanHost stands in for a host whose daemon died without shutting it down:
// a group-leading process nothing is waiting on, described only by a registry
// entry — exactly what `attn profile clean` finds. The returned entry is what
// the manager would have recorded at spawn. The body must touch $READY_FILE
// once its traps are installed; the reap's first SIGTERM must not race the
// script's setup.
func orphanHost(t *testing.T, dataDir, sessionID, body string) RegistryEntry {
	t.Helper()
	readyFile := filepath.Join(t.TempDir(), "ready")
	script := writeScript(t, "READY_FILE="+readyFile+"\n"+body)
	cmd := exec.Command(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orphan host: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap the exit ourselves so the pid does not linger as a zombie, which
	// would answer signal 0 forever and hang waitForGone.
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("orphan host never reported ready: %v", err)
	}

	entry := newRegistryEntry(sessionID, pid, []string{script})
	path := RegistryPath(dataDir, sessionID)
	if err := writeRegistry(path, entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return entry
}

// A cooperative host — one whose SIGTERM handler runs pi's dispose — is
// terminated by the first signal; the reap never escalates.
func TestReapTerminatesACooperativeOrphan(t *testing.T) {
	dataDir := t.TempDir()
	entry := orphanHost(t, dataDir, "s1", `
trap 'exit 0' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)

	start := time.Now()
	results := ReapDataDir(dataDir)
	if len(results) != 1 {
		t.Fatalf("expected one result, got %+v", results)
	}
	if results[0].Outcome != ReapTerminated {
		t.Fatalf("expected %s, got %+v", ReapTerminated, results[0])
	}
	if elapsed := time.Since(start); elapsed >= terminationGrace {
		t.Fatalf("cooperative reap waited out the %s grace (%s); SIGTERM is not reaching the host", terminationGrace, elapsed)
	}
	if processAlive(entry.PID) {
		t.Fatalf("host pid %d still alive after reap", entry.PID)
	}
}

// A wedged host that ignores SIGTERM is SIGKILLed after the grace window,
// together with whatever stayed in its group.
func TestReapKillsAnOrphanThatIgnoresSIGTERM(t *testing.T) {
	dataDir := t.TempDir()
	entry := orphanHost(t, dataDir, "s1", `
trap '' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapKilled {
		t.Fatalf("expected %s, got %+v", ReapKilled, results)
	}
	if processAlive(entry.PID) {
		t.Fatalf("host pid %d still alive after reap", entry.PID)
	}
}

// The identity gate: a registry entry whose pid now belongs to a different
// process (recorded start time no longer matches) must not be signalled.
func TestReapLeavesARecycledPIDAlone(t *testing.T) {
	dataDir := t.TempDir()
	entry := orphanHost(t, dataDir, "s1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	entry.ProcessStartTime = "not-this-process"
	if err := writeRegistry(RegistryPath(dataDir, "s1"), entry); err != nil {
		t.Fatalf("rewrite registry: %v", err)
	}

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapUnidentified {
		t.Fatalf("expected %s, got %+v", ReapUnidentified, results)
	}
	if !processAlive(entry.PID) {
		t.Fatalf("reap signalled a pid it could not identify")
	}
}

// An empty recorded start time (the spawn-time read failed) reads as "cannot
// identify", never as "safe to signal".
func TestReapRefusesAnEntryWithNoStartTime(t *testing.T) {
	dataDir := t.TempDir()
	entry := orphanHost(t, dataDir, "s1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	entry.ProcessStartTime = ""
	if err := writeRegistry(RegistryPath(dataDir, "s1"), entry); err != nil {
		t.Fatalf("rewrite registry: %v", err)
	}

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapUnidentified {
		t.Fatalf("expected %s, got %+v", ReapUnidentified, results)
	}
	if !processAlive(entry.PID) {
		t.Fatalf("reap signalled a pid it could not identify")
	}
}

// An entry whose process already exited is reported gone and nothing is
// signalled — the common case after a clean daemon shutdown that could not
// remove its records.
func TestReapReportsADeadEntryAsAlreadyGone(t *testing.T) {
	dataDir := t.TempDir()
	script := writeScript(t, "exit 0\n")
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	entry := newRegistryEntry("s1", pid, []string{script})
	_ = cmd.Wait()
	if err := writeRegistry(RegistryPath(dataDir, "s1"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapAlreadyGone {
		t.Fatalf("expected %s, got %+v", ReapAlreadyGone, results)
	}
}

// A data dir with no host registry at all — every profile before the first
// conversation session — reaps to nothing.
func TestReapOfAnEmptyDataDirIsQuiet(t *testing.T) {
	if results := ReapDataDir(t.TempDir()); len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}

// The reap tears down what pi's dispose would: the cooperative path is the
// only one that reaches tool subprocesses (they lead their own groups), so a
// host that forwards its teardown to a child stands in for it here.
func TestReapCooperativeTeardownReachesTheToolChild(t *testing.T) {
	dataDir := t.TempDir()
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	// `set -m` puts the background sleep in its own process group, the way
	// pi's tool subprocesses run — where the group sweep cannot reach it and
	// only the host's cooperative teardown does.
	orphanHost(t, dataDir, "s1", `
set -m
sleep 300 &
echo $! > `+childPIDFile+`
set +m
touch "$READY_FILE"
trap 'kill $(cat `+childPIDFile+`) 2>/dev/null; exit 0' TERM
while true; do sleep 0.05; done
`)
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(childPIDFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("orphan host never reported its tool child pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapTerminated {
		t.Fatalf("expected %s, got %+v", ReapTerminated, results)
	}
	if !waitForGone(childPID, 5*time.Second) {
		t.Fatalf("tool child %d survived the cooperative reap", childPID)
	}
}
