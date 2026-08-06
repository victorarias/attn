package hostsession

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// ReapOutcome names how a single registered host was disposed of.
type ReapOutcome string

const (
	// ReapTerminated: SIGTERM was enough — the host ran pi's dispose and left
	// within the grace window. The normal path, and the one that also tears
	// down tool subprocesses (they lead their own process groups, so only the
	// host's cooperative teardown reaches them).
	ReapTerminated ReapOutcome = "terminated"
	// ReapKilled: the host outlived the grace window and its group was
	// SIGKILLed.
	ReapKilled ReapOutcome = "killed"
	// ReapAlreadyGone: the registry entry outlived its process.
	ReapAlreadyGone ReapOutcome = "already gone"
	// ReapUnidentified: a process answers at the recorded PID but could not be
	// confirmed as this entry's host (PID reuse, or a start time we cannot
	// read). Nothing is signalled — the PID is reported for a human to judge.
	ReapUnidentified ReapOutcome = "unidentified"
	// ReapSurvived: the host was positively identified and signalled but still
	// would not die. Reported loudly; a process that survives SIGKILL is the
	// kernel's problem, not something to hide.
	ReapSurvived ReapOutcome = "survived"
)

// ReapResult reports what happened to one registered host.
type ReapResult struct {
	SessionID string
	PID       int
	Outcome   ReapOutcome
	// Err carries why an entry could not be identified or would not die. It is
	// context for a degraded outcome, not necessarily a failure of the reap.
	Err error
}

// ReapDataDir shuts down every host registered under a profile's data dir and
// returns one result per registry entry.
//
// This exists for the same reason the pty-worker reap does. A host never
// outlives its daemon on purpose — daemon shutdown kills them all — but a
// daemon that dies without running its shutdown (SIGKILL, crash) leaves its
// hosts running, reparented to init, with their only trace in this registry.
// `attn profile clean` deletes the data dir, so any host still alive at that
// moment would be stranded forever: nothing but a hand-written kill would ever
// reclaim it. Callers that are about to remove a data dir must reap first.
//
// Teardown is cooperative first: SIGTERM, which the host answers by running
// pi's dispose — the only path that reaches its tool subprocesses, which lead
// their own process groups. After terminationGrace the host's group is
// SIGKILLed as a backstop, and after a confirmed leader exit the group is swept
// once more, mirroring the manager's own monitor. A group id is held until its
// last member leaves, so the sweep cannot hit a recycled pid.
func ReapDataDir(dataDir string) []ReapResult {
	paths, err := filepath.Glob(filepath.Join(dataDir, "hosts", "registry", "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)

	var results []ReapResult
	for _, path := range paths {
		entry, err := ReadRegistry(path)
		if err != nil {
			continue
		}
		results = append(results, reapEntry(entry))
	}
	return results
}

// reapEntry disposes of one host, degrading only as far as the evidence allows.
func reapEntry(entry RegistryEntry) ReapResult {
	res := ReapResult{SessionID: entry.SessionID, PID: entry.PID}

	if entry.PID <= 0 || !processAlive(entry.PID) {
		res.Outcome = ReapAlreadyGone
		return res
	}

	// Identity gate: signal only a process still carrying the start time the
	// manager recorded at spawn. A recycled pid fails this; so does an entry
	// whose spawn-time read failed (empty stamp never matches anything).
	current, err := processStartTime(entry.PID)
	if err != nil || entry.ProcessStartTime == "" || current != entry.ProcessStartTime {
		if err == nil {
			err = fmt.Errorf("start time %q does not match recorded %q", current, entry.ProcessStartTime)
		}
		res.Outcome = ReapUnidentified
		res.Err = err
		return res
	}

	if err := syscall.Kill(entry.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			res.Outcome = ReapAlreadyGone
			return res
		}
		res.Outcome = ReapUnidentified
		res.Err = fmt.Errorf("SIGTERM pid %d: %w", entry.PID, err)
		return res
	}
	if waitForGone(entry.PID, terminationGrace) {
		res.Outcome = ReapTerminated
		sweepGroup(entry.PID)
		return res
	}

	_ = syscall.Kill(-entry.PID, syscall.SIGKILL)
	_ = syscall.Kill(entry.PID, syscall.SIGKILL)
	if waitForGone(entry.PID, terminationGrace) {
		res.Outcome = ReapKilled
		sweepGroup(entry.PID)
		return res
	}
	res.Outcome = ReapSurvived
	res.Err = fmt.Errorf("pid %d still alive after SIGKILL", entry.PID)
	return res
}

// sweepGroup SIGKILLs whatever is left of a dead leader's process group — the
// same after-exit sweep the manager's monitor performs. ESRCH means the group
// is already empty, the common case.
func sweepGroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func waitForGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !processAlive(pid)
}
