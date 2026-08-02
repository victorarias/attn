package pty

import (
	"os"
	"testing"
	"time"
)

func TestShellArbiterPollEmitsOnChangeAndKeepalive(t *testing.T) {
	t.Parallel()

	const shellPgid = 100
	const commandPgid = 200
	arbiter := newShellSignalArbiter(shellPgid)
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	obs, ok := arbiter.ObservePoll(shellPgid, at)
	if !ok {
		t.Fatal("first reading must emit")
	}
	if obs.Claim != claimNotBusy || obs.Source != SourceHeartbeat {
		t.Fatalf("at-prompt reading = %+v, want not_busy heartbeat", obs)
	}

	// The same level inside the keepalive window is not news.
	if _, ok := arbiter.ObservePoll(shellPgid, at.Add(200*time.Millisecond)); ok {
		t.Fatal("unchanged level inside keepalive must not emit")
	}

	// A change emits immediately, whatever the keepalive says.
	obs, ok = arbiter.ObservePoll(commandPgid, at.Add(400*time.Millisecond))
	if !ok || obs.Claim != claimBusy {
		t.Fatalf("foreground handoff = (%+v, %v), want busy emission", obs, ok)
	}

	// An unchanged level re-states itself once the keepalive elapses: the
	// re-emission is what keeps the heartbeat fresh while a command runs.
	if _, ok := arbiter.ObservePoll(commandPgid, at.Add(600*time.Millisecond)); ok {
		t.Fatal("unchanged busy inside keepalive must not emit")
	}
	obs, ok = arbiter.ObservePoll(commandPgid, at.Add(400*time.Millisecond+heartbeatKeepalive))
	if !ok || obs.Claim != claimBusy {
		t.Fatalf("keepalive re-emission = (%+v, %v), want busy", obs, ok)
	}

	// The command ends; the shell takes the foreground back.
	obs, ok = arbiter.ObservePoll(shellPgid, at.Add(2*time.Second))
	if !ok || obs.Claim != claimNotBusy {
		t.Fatalf("prompt return = (%+v, %v), want not_busy emission", obs, ok)
	}
}

// osc133Bytes builds one complete BEL-terminated marker.
func osc133Bytes(payload string) []byte {
	return []byte("\x1b]133;" + payload + "\x07")
}

func TestShellArbiterMarkersAreImmediateEdges(t *testing.T) {
	t.Parallel()

	const shellPgid = 100
	arbiter := newShellSignalArbiter(shellPgid)
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// At the prompt.
	if _, ok := arbiter.ObservePoll(shellPgid, at); !ok {
		t.Fatal("prompt poll must emit")
	}

	// A command starts: the C marker is a busy edge, no poll needed, and it
	// carries the cmdline when the integration provides one.
	out := arbiter.ObserveOutput(osc133Bytes("C;cmdline_url=sleep%201"), at.Add(10*time.Millisecond))
	if len(out) != 1 || out[0].Claim != claimBusy {
		t.Fatalf("pre-exec marker = %+v, want one busy edge", out)
	}
	if out[0].Detail != "command started: sleep 1" {
		t.Fatalf("pre-exec detail = %q, want the decoded cmdline", out[0].Detail)
	}

	// It exits: the D marker is a not_busy edge carrying the exit code —
	// well inside the keepalive window, because edges are the point.
	out = arbiter.ObserveOutput(osc133Bytes("D;1"), at.Add(120*time.Millisecond))
	if len(out) != 1 || out[0].Claim != claimNotBusy {
		t.Fatalf("command-end marker = %+v, want one not_busy edge", out)
	}
	if out[0].Detail != "command exited 1" {
		t.Fatalf("command-end detail = %q, want the exit code", out[0].Detail)
	}

	// The prompt-start marker that follows repeats the level and is deduped.
	if out = arbiter.ObserveOutput(osc133Bytes("A"), at.Add(130*time.Millisecond)); len(out) != 0 {
		t.Fatalf("prompt marker right after command end = %+v, want deduped", out)
	}

	// A marker split across chunks still parses (segmenter carry).
	first := osc133Bytes("C")
	out = arbiter.ObserveOutput(first[:3], at.Add(2*time.Second))
	if len(out) != 0 {
		t.Fatalf("partial marker = %+v, want nothing yet", out)
	}
	out = arbiter.ObserveOutput(first[3:], at.Add(2*time.Second))
	if len(out) != 1 || out[0].Claim != claimBusy {
		t.Fatalf("completed split marker = %+v, want one busy edge", out)
	}
}

// The one place the poll and the markers disagree: a foreground program that
// contains an inner shell at its prompt (ssh to a host whose remote shell
// integration passes markers through, a nested shell). The prompt verdict must
// hold while that program keeps the foreground — not flap against the poll —
// and must die the moment the foreground moves.
func TestShellArbiterInnerShellPromptOwnsTheClaimWhileItsProgramHoldsForeground(t *testing.T) {
	t.Parallel()

	const shellPgid = 100
	const sshPgid = 300
	const laterPgid = 400
	arbiter := newShellSignalArbiter(shellPgid)
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// ssh owns the foreground: busy.
	if obs, ok := arbiter.ObservePoll(sshPgid, at); !ok || obs.Claim != claimBusy {
		t.Fatalf("ssh foreground = (%+v, %v), want busy", obs, ok)
	}

	// The remote shell draws its prompt; the verdict binds to ssh's pgid.
	out := arbiter.ObserveOutput(osc133Bytes("A"), at.Add(100*time.Millisecond))
	if len(out) != 1 || out[0].Claim != claimNotBusy {
		t.Fatalf("remote prompt marker = %+v, want not_busy", out)
	}

	// Polls keep seeing ssh in the foreground, and must NOT flip back to busy:
	// the inner prompt still stands. (Keepalive re-states it.)
	if _, ok := arbiter.ObservePoll(sshPgid, at.Add(500*time.Millisecond)); ok {
		t.Fatal("agreeing poll inside keepalive must not emit")
	}
	obs, ok := arbiter.ObservePoll(sshPgid, at.Add(100*time.Millisecond+heartbeatKeepalive))
	if !ok || obs.Claim != claimNotBusy {
		t.Fatalf("poll during inner prompt = (%+v, %v), want not_busy keepalive", obs, ok)
	}

	// A remote command starts: busy again, from the marker alone.
	out = arbiter.ObserveOutput(osc133Bytes("C"), at.Add(3*time.Second))
	if len(out) != 1 || out[0].Claim != claimBusy {
		t.Fatalf("remote pre-exec = %+v, want busy edge", out)
	}
	// …and its prompt returns.
	out = arbiter.ObserveOutput(osc133Bytes("D;0"), at.Add(4*time.Second))
	if len(out) != 1 || out[0].Claim != claimNotBusy {
		t.Fatalf("remote command end = %+v, want not_busy edge", out)
	}

	// The foreground moves to a different program: the old verdict no longer
	// describes what is running, so the poll rules again.
	obs, ok = arbiter.ObservePoll(laterPgid, at.Add(5*time.Second))
	if !ok || obs.Claim != claimBusy {
		t.Fatalf("new foreground after inner prompt = (%+v, %v), want busy", obs, ok)
	}

	// And the local shell reclaiming the foreground is always definitive.
	obs, ok = arbiter.ObservePoll(shellPgid, at.Add(6*time.Second))
	if !ok || obs.Claim != claimNotBusy {
		t.Fatalf("local prompt return = (%+v, %v), want not_busy", obs, ok)
	}
}

// A prompt verdict must not leak onto a command the user starts right after
// the prompt. The failure this pins down: integration that emits A but not C
// (degraded bash), a prompt marker, then a command launched before the next
// poll — the verdict is bound to the *previous* foreground pgid, so the new
// command's fresh pgid reads busy.
func TestShellArbiterPromptVerdictDoesNotLeakOntoTheNextCommand(t *testing.T) {
	t.Parallel()

	const shellPgid = 100
	const firstCmdPgid = 300
	const secondCmdPgid = 400
	arbiter := newShellSignalArbiter(shellPgid)
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// A command runs (poll saw it), then ends; its prompt markers arrive while
	// the last polled foreground is still the dead command's pgid.
	if _, ok := arbiter.ObservePoll(firstCmdPgid, at); !ok {
		t.Fatal("first command poll must emit")
	}
	out := arbiter.ObserveOutput(osc133Bytes("A"), at.Add(100*time.Millisecond))
	if len(out) != 1 || out[0].Claim != claimNotBusy {
		t.Fatalf("prompt marker = %+v, want not_busy", out)
	}

	// The user immediately launches another command — no C marker (degraded
	// integration). The poll sees a foreground pgid that is neither the shell
	// nor the verdict's owner: busy.
	obs, ok := arbiter.ObservePoll(secondCmdPgid, at.Add(400*time.Millisecond))
	if !ok || obs.Claim != claimBusy {
		t.Fatalf("next command = (%+v, %v), want busy despite prompt verdict", obs, ok)
	}
}

// The end-to-end read: a real interactive shell on a real PTY hands the
// foreground to a command and takes it back, and the poller reports both
// transitions. bash is the shell because it enables job control for an
// interactive session unconditionally; the pane path launches whatever the
// user's login shell is, which behaves the same for the same reason.
func TestShellForegroundPollerObservesARealCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash unavailable")
	}

	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	const id = "shell-fg-poller"
	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-shell",
		ExternalCommand: []string{"/bin/bash", "--noprofile", "--norc", "-i"},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	s, err := m.getSession(id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}

	claims := make(chan string, 64)
	s.shellSignals = newShellSignalArbiter(s.childProcessGroup())
	s.onState = func(obs Observation) {
		claims <- obs.Claim
	}
	go s.runShellForegroundPoller(25 * time.Millisecond)

	waitForClaim := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case claim := <-claims:
				if claim == want {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q", want)
			}
		}
	}

	// The shell holds the foreground at its prompt.
	waitForClaim(claimNotBusy)

	if err := s.input([]byte("sleep 1\r")); err != nil {
		t.Fatalf("input() error: %v", err)
	}
	// The command's process group owns the foreground while it runs…
	waitForClaim(claimBusy)
	// …and the shell takes it back when it exits.
	waitForClaim(claimNotBusy)
}
