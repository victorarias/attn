package ptyworker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ReapOutcome names how a single worker was disposed of.
type ReapOutcome string

const (
	// ReapRemoved: the worker accepted the authenticated `remove` RPC and shut
	// itself down. The normal path.
	ReapRemoved ReapOutcome = "removed"
	// ReapAlreadyGone: the registry entry outlived its process.
	ReapAlreadyGone ReapOutcome = "already gone"
	// ReapSignalled: the control socket was unreachable but the process was
	// positively identified as this entry's worker, so it was SIGTERMed.
	ReapSignalled ReapOutcome = "signalled"
	// ReapUnidentified: the socket was unreachable AND the live PID could not be
	// confirmed to be this worker (PID reuse, or a process we cannot inspect).
	// Nothing is signalled — the PID is reported for a human to judge.
	ReapUnidentified ReapOutcome = "unidentified"
)

// ReapResult reports what happened to one registered worker.
type ReapResult struct {
	SessionID string
	WorkerPID int
	Outcome   ReapOutcome
	// Err carries why the preferred (control-socket) path was not taken. It is
	// context for a degraded outcome, not necessarily a failure of the reap.
	Err error
}

// ReapDataDir shuts down every pty-worker registered under a profile's data
// dir and returns one result per registry entry.
//
// This exists because stopping a daemon deliberately leaves its workers running
// — they are built to outlive a daemon restart and be re-adopted. Deleting the
// data dir (as `attn profile clean` does) destroys the registry those workers
// are found through, so any worker still alive at that moment is stranded
// forever: no daemon can ever adopt it, and nothing but a hand-written kill will
// reclaim its memory. Callers that are about to remove a data dir must reap
// first.
//
// Workers are shut down over their own authenticated control socket rather than
// by signalling a PID: the registry records the socket path, the owning daemon
// instance id, and the control token, so `remove` is precise by construction and
// cannot hit an unrelated process that inherited the PID.
func ReapDataDir(dataDir string) []ReapResult {
	paths, err := filepath.Glob(filepath.Join(dataDir, "workers", "*", "registry", "*.json"))
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
		results = append(results, reapEntry(entry, path))
	}
	return results
}

// reapEntry disposes of one worker, preferring the control socket and degrading
// only as far as the evidence allows.
func reapEntry(entry RegistryEntry, registryPath string) ReapResult {
	res := ReapResult{SessionID: entry.SessionID, WorkerPID: entry.WorkerPID}

	if entry.WorkerPID <= 0 || !ProcessAlive(entry.WorkerPID) {
		res.Outcome = ReapAlreadyGone
		return res
	}

	if err := requestWorkerRemove(entry); err == nil {
		if waitForExit(entry.WorkerPID, 5*time.Second) {
			res.Outcome = ReapRemoved
			return res
		}
		res.Err = errors.New("worker accepted remove but did not exit")
	} else {
		res.Err = err
	}

	// The control socket did not finish the job. Signal only a process we can
	// still positively identify as this worker: the registry path is unique per
	// session per data dir, so finding it in the process's own argv rules out a
	// recycled PID.
	if !processHasArg(entry.WorkerPID, registryPath) {
		res.Outcome = ReapUnidentified
		return res
	}
	_ = syscall.Kill(entry.WorkerPID, syscall.SIGTERM)
	waitForExit(entry.WorkerPID, 5*time.Second)
	res.Outcome = ReapSignalled
	return res
}

// requestWorkerRemove performs the worker handshake and asks it to remove its
// session, which SIGTERMs the child and stops the worker runtime.
func requestWorkerRemove(entry RegistryEntry) error {
	if strings.TrimSpace(entry.SocketPath) == "" {
		return errors.New("registry entry has no socket path")
	}
	conn, err := net.DialTimeout("unix", entry.SocketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	hello := HelloParams{
		RPCMajor:         RPCMajor,
		RPCMinor:         RPCMinor,
		DaemonInstanceID: entry.DaemonInstanceID,
		ControlToken:     entry.ControlToken,
	}
	if err := writeReapRequest(enc, "reap-hello", MethodHello, hello); err != nil {
		return err
	}
	if err := awaitOK(dec, "reap-hello"); err != nil {
		return err
	}
	if err := writeReapRequest(enc, "reap-remove", MethodRemove, map[string]any{}); err != nil {
		return err
	}
	return awaitOK(dec, "reap-remove")
}

func writeReapRequest(enc *json.Encoder, id, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return enc.Encode(RequestEnvelope{Type: "req", ID: id, Method: method, Params: raw})
}

// awaitOK reads frames until the response for id arrives, skipping the events a
// worker may interleave with responses.
func awaitOK(dec *json.Decoder, id string) error {
	for {
		var res ResponseEnvelope
		if err := dec.Decode(&res); err != nil {
			return err
		}
		if res.Type != "res" || res.ID != id {
			continue
		}
		if !res.OK {
			if res.Error != nil {
				return fmt.Errorf("worker %s: %s", res.Error.Code, res.Error.Message)
			}
			return errors.New("worker rejected request")
		}
		return nil
	}
}

// ProcessAlive reports whether a recorded worker PID still names a live
// process. Exported because callers that merely inventory a data dir need the
// same liveness answer the reaper uses.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !ProcessAlive(pid)
}

// processHasArg reports whether the live process's own argv contains want. It is
// the identity check that makes signalling safe, so an unreadable argv must read
// as "not identified" rather than "probably fine".
func processHasArg(pid int, want string) bool {
	if want == "" {
		return false
	}
	if raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline")); err == nil {
		for _, arg := range strings.Split(string(raw), "\x00") {
			if arg == want {
				return true
			}
		}
		return false
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), want)
}
