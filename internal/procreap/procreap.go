// Package procreap gives a daemon-owned child process a durable on-disk record
// and a way to reap it from that record alone.
//
// The daemon's managers hold their children in memory; that inventory dies
// with the daemon. A daemon that exits without running its shutdown — SIGKILL,
// a crash, a power cut — leaves those children running, reparented to init,
// findable through nothing else. `attn profile clean` deletes the data dir, so
// any such process still alive at that moment would be stranded forever:
// nothing but a hand-written kill would ever reclaim its memory. The record is
// the net under that hole, and ReapDir is what clean pulls on it.
//
// Two managers use it today: conversation hosts (internal/hostsession) and
// plugin runtime processes (the daemon's plugin supervisor). pty-workers have
// their own richer registry (internal/ptyworker) with a control socket; this
// package is for children that have no RPC surface of their own.
package procreap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// Entry records what a manager knew about one child at spawn: enough to find
// the process again and to prove it is still the same one before signalling it.
type Entry struct {
	Version int `json:"version"`
	// ID names the entity the process serves — a session id, a plugin name.
	ID  string `json:"id"`
	PID int    `json:"pid"`
	// PGID is the process group the child was spawned into. When it equals PID
	// the child leads its own group and the reap may sweep it; otherwise the
	// group is shared (usually with the dead daemon's other children) and a
	// group signal could hit processes this entry does not own.
	PGID    int      `json:"pgid"`
	Command []string `json:"command"`
	// ProcessStartTime is the platform's own identity stamp for the pid, read
	// back from the live process right after spawn (see processStartTime). A
	// pid alone can be recycled; a pid whose start time still matches is the
	// process we started.
	ProcessStartTime string `json:"process_start_time"`
	StartedAt        string `json:"started_at"`
}

const entryVersion = 1

// NewEntry stamps a freshly spawned child. pgid is normally pid (spawned with
// Setpgid) — pass what the spawn actually did.
func NewEntry(id string, pid, pgid int, command []string) Entry {
	startTime, err := processStartTime(pid)
	if err != nil {
		// A child can exit faster than we can stamp it; the record still gets
		// written so the reaper sees the entity existed. An empty stamp reads
		// as "cannot identify", never as "safe to signal".
		startTime = ""
	}
	return Entry{
		Version:          entryVersion,
		ID:               id,
		PID:              pid,
		PGID:             pgid,
		Command:          append([]string(nil), command...),
		ProcessStartTime: startTime,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

// WriteEntry commits one record. The write is atomic (temp file + rename) so a
// reaper never reads a half-written record.
func WriteEntry(path string, entry Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create process registry dir: %w", err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode process registry entry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return fmt.Errorf("write process registry entry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit process registry entry: %w", err)
	}
	return nil
}

// ReadEntry loads one record.
func ReadEntry(path string) (Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Entry{}, fmt.Errorf("decode process registry entry %s: %w", path, err)
	}
	if entry.Version != entryVersion {
		return Entry{}, fmt.Errorf("process registry entry %s has version %d, want %d", path, entry.Version, entryVersion)
	}
	return entry, nil
}

// RemoveEntry retires a record once its process is known gone. Missing is fine
// — the data dir may already be on its way out.
func RemoveEntry(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReapOutcome names how a single registered process was disposed of.
type ReapOutcome string

const (
	// ReapTerminated: SIGTERM was enough — the process shut down within the
	// grace window. The normal path, and for hosts the one that also tears
	// down tool subprocesses (they lead their own process groups, so only the
	// host's cooperative teardown reaches them).
	ReapTerminated ReapOutcome = "terminated"
	// ReapKilled: the process outlived the grace window and was SIGKILLed
	// (with its group, when it led one).
	ReapKilled ReapOutcome = "killed"
	// ReapAlreadyGone: the registry entry outlived its process.
	ReapAlreadyGone ReapOutcome = "already gone"
	// ReapUnidentified: a process answers at the recorded PID but could not be
	// confirmed as this entry's child (PID reuse, or a start time we cannot
	// read). Nothing is signalled — the PID is reported for a human to judge.
	ReapUnidentified ReapOutcome = "unidentified"
	// ReapSurvived: the process was positively identified and signalled but
	// still would not die. Reported loudly; a process that survives SIGKILL is
	// the kernel's problem, not something to hide.
	ReapSurvived ReapOutcome = "survived"
	// ReapUnreadable: a record exists but could not be decoded (corrupt, or
	// written by a version this build does not understand), so whatever it
	// described cannot be reaped. Named rather than skipped: silence here reads
	// as "nothing was registered", which is the opposite of the truth.
	ReapUnreadable ReapOutcome = "unreadable"
)

// ReapResult reports what happened to one registered process.
type ReapResult struct {
	ID      string
	PID     int
	Outcome ReapOutcome
	// Err carries why an entry could not be identified or would not die. It is
	// context for a degraded outcome, not necessarily a failure of the reap.
	Err error
}

// ReapDir shuts down every process registered under dir and returns one result
// per registry entry.
//
// Teardown is cooperative first: SIGTERM, then after grace a SIGKILL backstop.
// A child that led its own process group has the whole group swept once the
// leader is gone — the same after-exit sweep its manager would have performed.
// A group id is held until its last member leaves, so the sweep cannot hit a
// recycled pid; a child that shared a group is never group-signalled, because
// the group's other members are not this entry's to kill.
func ReapDir(dir string, grace time.Duration) []ReapResult {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)

	var results []ReapResult
	for _, path := range paths {
		entry, err := ReadEntry(path)
		if err != nil {
			results = append(results, ReapResult{
				ID:      filepath.Base(path),
				Outcome: ReapUnreadable,
				Err:     err,
			})
			continue
		}
		results = append(results, reapEntry(entry, grace))
	}
	return results
}

// reapEntry disposes of one process, degrading only as far as the evidence
// allows.
func reapEntry(entry Entry, grace time.Duration) ReapResult {
	res := ReapResult{ID: entry.ID, PID: entry.PID}
	leadsGroup := entry.PGID == entry.PID && entry.PID > 0

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
	if waitForGone(entry.PID, grace) {
		res.Outcome = ReapTerminated
		if leadsGroup {
			sweepGroup(entry.PGID)
		}
		return res
	}

	if leadsGroup {
		_ = syscall.Kill(-entry.PGID, syscall.SIGKILL)
	}
	_ = syscall.Kill(entry.PID, syscall.SIGKILL)
	if waitForGone(entry.PID, grace) {
		res.Outcome = ReapKilled
		if leadsGroup {
			sweepGroup(entry.PGID)
		}
		return res
	}
	res.Outcome = ReapSurvived
	res.Err = fmt.Errorf("pid %d still alive after SIGKILL", entry.PID)
	return res
}

// sweepGroup SIGKILLs whatever is left of a dead leader's process group — the
// same after-exit sweep the owning manager would have performed. ESRCH means
// the group is already empty, the common case.
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

// processStartTime returns an opaque stamp of when the pid's process started,
// stable across reads for the same process and distinct between any two
// processes that could share a pid. Comparing the stamp recorded at spawn
// against a fresh read is the identity check that makes signalling a recorded
// pid safe.
//
// Resolution is the whole safety property. A stamp coarser than the interval in
// which the kernel can hand the same pid to a different process makes the gate
// pass for a stranger, and the reaper then SIGTERMs, SIGKILLs and group-sweeps
// an innocent one — the kill-by-pattern this package exists to avoid. Each
// implementation states its resolution and the measured pid-reuse floor it has
// to beat (procstart_darwin.go, procstart_linux.go); there is deliberately no
// generic fallback, so an unsupported platform fails to compile rather than
// reaping on a stamp nobody has checked.
