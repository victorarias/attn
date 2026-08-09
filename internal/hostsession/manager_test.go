package hostsession

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	events []Event
	exits  []ExitInfo
	exited chan struct{}
	logs   []string
}

func newRecorder() *recorder {
	return &recorder{exited: make(chan struct{})}
}

func (r *recorder) logf(format string, args ...interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, format)
	_ = args
}

func (r *recorder) event(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) exit(info ExitInfo) {
	r.mu.Lock()
	r.exits = append(r.exits, info)
	r.mu.Unlock()
	close(r.exited)
}

func (r *recorder) snapshot() ([]Event, []ExitInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...), append([]ExitInfo(nil), r.exits...)
}

// writeScript stages an executable fake host. Fakes stand in for pi so these
// tests assert the lifecycle contract — the envelope fd, the verb pipe, and the
// process group — without an API key or a model.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-host.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake host: %v", err)
	}
	return path
}

func newManager(t *testing.T) (*Manager, *recorder) {
	t.Helper()
	rec := newRecorder()
	return New(rec.logf, rec.event, rec.exit), rec
}

func waitForExit(t *testing.T, rec *recorder) ExitInfo {
	t.Helper()
	select {
	case <-rec.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("host never reported an exit")
	}
	_, exits := rec.snapshot()
	if len(exits) != 1 {
		t.Fatalf("expected exactly one exit, got %d", len(exits))
	}
	return exits[0]
}

// processGone reports whether a pid we do not own has left the process table.
// The only observable for a grandchild's death is that its pid stops
// answering signal 0, so this polls it against a deadline far past any real
// teardown and fails loudly rather than passing on a guess.
func processGone(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestSpawnForwardsEnvelopesFromTheDedicatedFD(t *testing.T) {
	manager, rec := newManager(t)
	// Prints on stdout as well, to prove a chatty host cannot corrupt the
	// envelope stream — pi loads the user's own extensions, and any of them
	// may print.
	script := writeScript(t, `
echo "noise on stdout"
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{"model":"openai/x"}}' >&3
echo '{"session_id":"s1","seq":2,"kind":"message_delta","body":{"id":"m1","text":"hi"}}' >&3
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}, LogPath: filepath.Join(t.TempDir(), "host.log")}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitForExit(t, rec)

	events, _ := rec.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 envelopes, got %d: %+v", len(events), events)
	}
	if events[0].Kind != "session_ready" || events[0].Seq != 1 {
		t.Fatalf("unexpected first envelope: %+v", events[0])
	}
	if events[1].Kind != "message_delta" || events[1].Body["text"] != "hi" {
		t.Fatalf("unexpected second envelope: %+v", events[1])
	}
	if events[1].SessionID != "s1" {
		t.Fatalf("envelope not stamped with the spawned session: %+v", events[1])
	}
}

func TestSpawnKeepsHostStdoutOutOfTheEnvelopeStream(t *testing.T) {
	manager, rec := newManager(t)
	logPath := filepath.Join(t.TempDir(), "host.log")
	script := writeScript(t, `
echo "an extension printed this"
echo "and this went to stderr" >&2
echo '{"session_id":"s1","seq":1,"kind":"run_settled","body":{}}' >&3
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}, LogPath: logPath}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitForExit(t, rec)

	events, _ := rec.snapshot()
	if len(events) != 1 || events[0].Kind != "run_settled" {
		t.Fatalf("host stdout leaked into the envelope stream: %+v", events)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read host log: %v", err)
	}
	if !strings.Contains(string(log), "an extension printed this") || !strings.Contains(string(log), "and this went to stderr") {
		t.Fatalf("host log is missing the process output: %q", log)
	}
}

// Every delivery reaches the host as the verb it was asked for, with its text
// intact. The verb name is the whole of what steer, follow-up and prompt differ
// by on this side of the pipe: the host is what decides what each one means.
func TestDeliveryReachesTheHostAsItsOwnVerb(t *testing.T) {
	for _, how := range []Delivery{DeliveryPrompt, DeliverySteer, DeliveryFollowUp} {
		t.Run(string(how), func(t *testing.T) {
			manager, rec := newManager(t)
			script := writeScript(t, `
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{}}' >&3
while read -r line; do
  printf '{"session_id":"s1","seq":2,"kind":"message_end","body":{"echo":%s}}\n' "$(printf '%s' "$line" | sed 's/"/\\"/g; s/^/"/; s/$/"/')" >&3
  break
done
`)

			if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}}); err != nil {
				t.Fatalf("spawn: %v", err)
			}
			// The host answers the verb, so waiting on its exit is waiting on delivery.
			if err := manager.Deliver("s1", how, "hello host"); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			waitForExit(t, rec)

			events, _ := rec.snapshot()
			if len(events) != 2 {
				t.Fatalf("expected the ready envelope and the echo, got %+v", events)
			}
			echoed, _ := events[1].Body["echo"].(string)
			if !strings.Contains(echoed, `"verb":"`+string(how)+`"`) {
				t.Fatalf("host did not receive the %s verb: %q", how, echoed)
			}
			if !strings.Contains(echoed, `hello host`) {
				t.Fatalf("host did not receive the text: %q", echoed)
			}
		})
	}
}

func TestDeliveryRejectsAnUnknownVerb(t *testing.T) {
	manager, _ := newManager(t)
	if err := manager.Deliver("s1", Delivery("shout"), "hi"); err == nil {
		t.Fatal("expected an error naming the unsupported delivery")
	} else if !strings.Contains(err.Error(), "shout") {
		t.Fatalf("error does not name the delivery: %v", err)
	}
}

func TestDeliveryRejectsAnUnknownSession(t *testing.T) {
	manager, _ := newManager(t)
	if err := manager.Deliver("nope", DeliveryPrompt, "hi"); err == nil {
		t.Fatal("expected an error naming the missing session")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error does not name the session: %v", err)
	}
}

// The receipted bug this whole package exists for: a host that dies takes its
// tool subprocesses with it. Here the host exits ON ITS OWN, so nothing kills
// the group unless the manager sweeps it after the exit.
func TestSelfExitSweepsOrphanedToolSubprocesses(t *testing.T) {
	manager, rec := newManager(t)
	script := writeScript(t, `
sleep 300 &
echo "{\"session_id\":\"s1\",\"seq\":1,\"kind\":\"session_ready\",\"body\":{\"child\":$!}}" >&3
exit 0
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitForExit(t, rec)

	events, _ := rec.snapshot()
	if len(events) == 0 {
		t.Fatal("host reported no envelope, so the child pid is unknown")
	}
	childPID := pidFromBody(t, events[0].Body["child"])
	if !processGone(childPID) {
		syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("tool subprocess %d outlived its host", childPID)
	}
}

func TestKillSweepsAHostThatIgnoresSIGTERM(t *testing.T) {
	manager, rec := newManager(t)
	script := writeScript(t, `
trap '' TERM
sleep 300 &
echo "{\"session_id\":\"s1\",\"seq\":1,\"kind\":\"session_ready\",\"body\":{\"child\":$!}}" >&3
while true; do sleep 1; done
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Wait for the ready envelope so the child pid is known before the kill.
	deadline := time.Now().Add(10 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if events, _ := rec.snapshot(); len(events) > 0 {
			childPID = pidFromBody(t, events[0].Body["child"])
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("host never reported its child pid")
	}

	if err := manager.Kill("s1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if manager.Has("s1") {
		t.Fatal("Kill returned while the host was still registered")
	}
	waitForExit(t, rec)
	if !processGone(childPID) {
		syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("tool subprocess %d survived the group kill", childPID)
	}
}

func TestKillLetsACooperativeHostTearItselfDown(t *testing.T) {
	manager, rec := newManager(t)
	script := writeScript(t, `
trap 'echo "{\"session_id\":\"s1\",\"seq\":9,\"kind\":\"run_settled\",\"body\":{\"cooperative\":true}}" >&3; exit 0' TERM
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{}}' >&3
while true; do sleep 0.05; done
`)

	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if events, _ := rec.snapshot(); len(events) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	if err := manager.Kill("s1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= terminationGrace {
		t.Fatalf("cooperative teardown waited out the %s grace (%s); SIGTERM is not reaching the host", terminationGrace, elapsed)
	}
	exit := waitForExit(t, rec)
	if exit.ExitCode != 0 {
		t.Fatalf("cooperative host should exit 0, got %+v", exit)
	}
	events, _ := rec.snapshot()
	if len(events) != 2 || events[1].Body["cooperative"] != true {
		t.Fatalf("the host's own teardown envelope did not arrive: %+v", events)
	}
}

func TestSpawnRefusesASecondHostForTheSameSession(t *testing.T) {
	manager, rec := newManager(t)
	script := writeScript(t, `
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{}}' >&3
while true; do sleep 0.05; done
`)
	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { manager.Kill("s1") })

	err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected a refusal naming the live host, got %v", err)
	}
	_ = rec
}

func TestExitReportsTheSignalThatKilledTheHost(t *testing.T) {
	manager, rec := newManager(t)
	script := writeScript(t, `
trap '' TERM
echo '{"session_id":"s1","seq":1,"kind":"session_ready","body":{}}' >&3
while true; do sleep 0.05; done
`)
	if err := manager.Spawn(SpawnOptions{SessionID: "s1", Command: []string{script}}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if events, _ := rec.snapshot(); len(events) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := manager.Kill("s1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	exit := waitForExit(t, rec)
	if exit.Signal != "killed" {
		t.Fatalf("expected the SIGKILL that swept the group to be reported, got %+v", exit)
	}
}

func pidFromBody(t *testing.T, value interface{}) int {
	t.Helper()
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		pid, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			t.Fatalf("child pid %q is not a number: %v", typed, err)
		}
		return pid
	default:
		t.Fatalf("child pid missing from envelope body (%T)", value)
		return 0
	}
}
