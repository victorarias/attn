package procreap

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

const testGrace = 3 * time.Second

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-child.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake child: %v", err)
	}
	return path
}

// orphan stands in for a child whose daemon died without shutting it down: a
// group-leading process nothing is waiting on, described only by a registry
// entry — exactly what `attn profile clean` finds. The body must touch
// $READY_FILE once its traps are installed; the reap's first SIGTERM must not
// race the script's setup.
func orphan(t *testing.T, dir, id, body string) Entry {
	t.Helper()
	readyFile := filepath.Join(t.TempDir(), "ready")
	script := writeScript(t, "READY_FILE="+readyFile+"\n"+body)
	cmd := exec.Command(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orphan: %v", err)
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
		t.Fatalf("orphan never reported ready: %v", err)
	}

	entry := NewEntry(id, pid, pid, []string{script})
	if err := WriteEntry(filepath.Join(dir, id+".json"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return entry
}

func TestEntryRoundTripsWithIdentityStamp(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	if entry.ProcessStartTime == "" {
		t.Fatalf("entry carries no process start time: %+v", entry)
	}
	read, err := ReadEntry(filepath.Join(dir, "e1.json"))
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if read.ID != "e1" || read.PID != entry.PID || read.ProcessStartTime != entry.ProcessStartTime {
		t.Fatalf("entry did not round trip: wrote %+v read %+v", entry, read)
	}
	current, err := processStartTime(entry.PID)
	if err != nil || current != entry.ProcessStartTime {
		t.Fatalf("recorded start time %q does not match the live process (%q, %v)", entry.ProcessStartTime, current, err)
	}
}

// The identity gate is only as good as the stamp's resolution. A stamp coarser
// than the interval in which the kernel can hand the same pid to a different
// process lets an imposter through the gate, and the reaper then SIGTERMs,
// SIGKILLs and group-sweeps a stranger — the kill-by-pattern this package
// exists to avoid.
//
// The floor a stamp has to beat is the pid-reuse interval, which requires a
// full wrap of the pid space: measured at 2m37s on macOS (92043 spawns at
// 588/s, ceiling ~99998) and at more than 4 minutes on Linux (1949409 spawns at
// 8123/s without a single reuse, pid_max 4194304). Each platform's
// stampResolution — 1µs from the Darwin sysctl, 10ms from Linux's /proc — sits
// orders of magnitude under that, and this pins it: processes spawned that far
// apart must carry different stamps. A second-granularity source (Darwin's
// `ps -o lstart=`, whose strftime %c drops everything below a second) fails
// here.
func TestStartTimeStampResolvesFasterThanPidsAreReused(t *testing.T) {
	// Generous multiple of the platform floor, still five orders of magnitude
	// under the measured reuse interval.
	separation := 20 * stampResolution
	if separation < time.Millisecond {
		separation = time.Millisecond
	}
	script := writeScript(t, "while true; do sleep 0.05; done\n")

	spawn := func() (int, string) {
		t.Helper()
		cmd := exec.Command(script)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}
		pid := cmd.Process.Pid
		go func() { _ = cmd.Wait() }()
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		stamp, err := processStartTime(pid)
		if err != nil {
			t.Fatalf("read start time of pid %d: %v", pid, err)
		}
		return pid, stamp
	}

	firstPID, firstStamp := spawn()
	time.Sleep(separation)
	secondPID, secondStamp := spawn()

	if firstStamp == secondStamp {
		t.Fatalf("pids %d and %d started %s apart share the stamp %q; the stamp is too coarse to tell a recycled pid from the recorded process",
			firstPID, secondPID, separation, firstStamp)
	}
}

// The stamp must also be stable: re-reading a live process has to reproduce
// what was recorded at spawn, or every reap would degrade to "unidentified"
// and clean would quietly stop reaping anything.
func TestStartTimeStampIsStableAcrossReads(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	time.Sleep(50 * time.Millisecond)
	again, err := processStartTime(entry.PID)
	if err != nil {
		t.Fatalf("re-read start time: %v", err)
	}
	if again != entry.ProcessStartTime {
		t.Fatalf("stamp for pid %d changed between reads: recorded %q, now %q", entry.PID, entry.ProcessStartTime, again)
	}
}

// A cooperative child — one whose SIGTERM handler runs its teardown — is
// terminated by the first signal; the reap never escalates.
func TestReapTerminatesACooperativeOrphan(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
trap 'exit 0' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)

	start := time.Now()
	results := ReapDir(dir, testGrace)
	if len(results) != 1 {
		t.Fatalf("expected one result, got %+v", results)
	}
	if results[0].Outcome != ReapTerminated {
		t.Fatalf("expected %s, got %+v", ReapTerminated, results[0])
	}
	if elapsed := time.Since(start); elapsed >= testGrace {
		t.Fatalf("cooperative reap waited out the %s grace (%s); SIGTERM is not reaching the child", testGrace, elapsed)
	}
	if processAlive(entry.PID) {
		t.Fatalf("pid %d still alive after reap", entry.PID)
	}
}

// A wedged child that ignores SIGTERM is SIGKILLed after the grace window,
// together with whatever stayed in its group.
func TestReapKillsAnOrphanThatIgnoresSIGTERM(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
trap '' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapKilled {
		t.Fatalf("expected %s, got %+v", ReapKilled, results)
	}
	if processAlive(entry.PID) {
		t.Fatalf("pid %d still alive after reap", entry.PID)
	}
}

// The identity gate: a registry entry whose pid now belongs to a different
// process (recorded start time no longer matches) must not be signalled.
func TestReapLeavesARecycledPIDAlone(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	entry.ProcessStartTime = "not-this-process"
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("rewrite registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
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
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	entry.ProcessStartTime = ""
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("rewrite registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
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
	dir := t.TempDir()
	script := writeScript(t, "exit 0\n")
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	entry := NewEntry("e1", cmd.Process.Pid, cmd.Process.Pid, []string{script})
	_ = cmd.Wait()
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapAlreadyGone {
		t.Fatalf("expected %s, got %+v", ReapAlreadyGone, results)
	}
}

// A directory with no registry at all reaps to nothing.
func TestReapOfAnEmptyDirIsQuiet(t *testing.T) {
	if results := ReapDir(t.TempDir(), testGrace); len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}

// A record this build cannot decode — corrupt, or written by a future version —
// describes a process that will now never be reaped. Reporting it is the whole
// point: staying silent would read as "nothing was registered".
func TestReapReportsAnUnreadableRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "e1.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := WriteEntry(filepath.Join(dir, "e2.json"), Entry{Version: entryVersion + 1, ID: "e2", PID: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 2 {
		t.Fatalf("expected both records reported, got %+v", results)
	}
	for _, res := range results {
		if res.Outcome != ReapUnreadable {
			t.Fatalf("expected %s, got %+v", ReapUnreadable, res)
		}
		if res.Err == nil || res.ID == "" {
			t.Fatalf("an unreadable record must name itself and say why: %+v", res)
		}
	}
}

// The reap tears down what a host's dispose would: the cooperative path is the
// only one that reaches tool subprocesses (they lead their own groups), so a
// child that forwards its teardown to a grandchild stands in for it here.
func TestReapCooperativeTeardownReachesTheDetachedChild(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	// `set -m` puts the background sleep in its own process group, the way
	// pi's tool subprocesses run — where the group sweep cannot reach it and
	// only the cooperative teardown does.
	orphan(t, dir, "e1", `
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
		t.Fatal("orphan never reported its detached child pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapTerminated {
		t.Fatalf("expected %s, got %+v", ReapTerminated, results)
	}
	if !waitForGone(childPID, 5*time.Second) {
		t.Fatalf("detached child %d survived the cooperative reap", childPID)
	}
}

// A recorded child that shared its manager's process group (pgid != pid) is
// signalled alone: a group signal would hit processes the entry does not own.
func TestReapNeverGroupSignalsASharedGroupEntry(t *testing.T) {
	dir := t.TempDir()
	// Spawn WITHOUT Setpgid: the child shares this test process's group. A
	// group SIGKILL from the reap would take the test run down with it — the
	// test passing at all is the assertion.
	readyFile := filepath.Join(t.TempDir(), "ready")
	script := writeScript(t, "READY_FILE="+readyFile+`
trap '' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if pgid == pid {
		t.Fatalf("test setup: child unexpectedly leads its own group")
	}
	entry := NewEntry("e1", pid, pgid, []string{script})
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapKilled {
		t.Fatalf("expected %s, got %+v", ReapKilled, results)
	}
	if processAlive(pid) {
		t.Fatalf("pid %d still alive after reap", pid)
	}
}
