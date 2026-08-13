package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/toolhome"
)

// haltedTurnCase is one agent's captured halt, plus whatever its real transcript
// finder needs in order to find the file.
type haltedTurnCase struct {
	name  string
	agent protocol.SessionAgent
	// seed writes the transcript the watcher must discover, and returns its path.
	// Discovery is per-agent, and the watcher resolves the agent-native id through
	// each real finder, so the fixture has to satisfy that exact lookup.
	seed func(t *testing.T, home, dir, sessionID string) string
	// abort is the captured line each agent writes when the user hits ESC, dated
	// at `at`. The date is part of the contract: an undated halt is thrown away.
	abort func(at time.Time) string
}

func haltedTurnCases() []haltedTurnCase {
	return []haltedTurnCase{
		{
			name:  "claude",
			agent: protocol.SessionAgentClaude,
			seed: func(t *testing.T, home, dir, sessionID string) string {
				projects := filepath.Join(home, ".claude", "projects", "a-project")
				if err := os.MkdirAll(projects, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				path := filepath.Join(projects, sessionID+".jsonl")
				writeLine(t, path, `{"type":"user","message":{"role":"user","content":"write an essay"}}`)
				return path
			},
			abort: func(at time.Time) string {
				return fmt.Sprintf(
					`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"interruptedMessageId":"msg_011Cdcnp6M5nmsdZuTzGRmaF","timestamp":%q}`,
					at.UTC().Format(time.RFC3339Nano),
				)
			},
		},
		{
			name:  "codex",
			agent: protocol.SessionAgentCodex,
			seed: func(t *testing.T, home, dir, sessionID string) string {
				sessions := filepath.Join(home, ".codex", "sessions", "2026", "08", "01")
				if err := os.MkdirAll(sessions, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				path := filepath.Join(sessions, "rollout-"+sessionID+".jsonl")
				stamp := time.Now().UTC().Format(time.RFC3339Nano)
				writeLine(t, path, fmt.Sprintf(
					`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"cwd":%q,"timestamp":%q}}`,
					stamp, sessionID, dir, stamp,
				))
				return path
			},
			abort: func(at time.Time) string {
				return fmt.Sprintf(
					`{"timestamp":%q,"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"019fbf55-ef48","reason":"interrupted"}}`,
					at.UTC().Format(time.RFC3339Nano),
				)
			},
		},
		{
			name:  "copilot",
			agent: protocol.SessionAgentCopilot,
			seed: func(t *testing.T, home, dir, sessionID string) string {
				state := filepath.Join(home, ".copilot", "session-state", sessionID)
				if err := os.MkdirAll(state, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(state, "workspace.yaml"), []byte("cwd: "+dir+"\n"), 0o644); err != nil {
					t.Fatalf("write workspace.yaml: %v", err)
				}
				path := filepath.Join(state, "events.jsonl")
				writeLine(t, path, fmt.Sprintf(
					`{"type":"session.start","data":{"sessionId":%q,"startTime":%q}}`,
					sessionID, time.Now().UTC().Format(time.RFC3339Nano),
				))
				writeLine(t, path, `{"type":"assistant.turn_start","data":{"turnId":"0"}}`)
				return path
			},
			abort: func(at time.Time) string {
				return fmt.Sprintf(
					`{"type":"abort","timestamp":%q,"data":{"reason":"user_initiated"}}`,
					at.UTC().Format(time.RFC3339Nano),
				)
			},
		},
	}
}

// Halting a turn is the one ending no agent reports. Measured live: claude
// 2.1.220 with all 31 of its hook events wired to a logger, codex 0.146.0 with
// attn's own trusted-hash hook overrides, and copilot 1.0.77 — all interrupted
// with ESC mid-turn, and none of them produce a Stop, a StopFailure, a
// Notification, or anything else, for as long as you wait. What each does write,
// in the same second, is a line in its transcript, so that is what the watcher
// reads.
//
// These drive the real watcher against a real file rather than calling the
// parser directly: discovery, tailing, the per-agent behavior, and the evidence
// write are one path, and the bug this fixes lived in the fact that nothing
// connected them.
func TestTheWatcherSettlesATurnTheUserHalted(t *testing.T) {
	for _, tc := range haltedTurnCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(toolhome.EnvVar, t.TempDir())
			home, _ := toolhome.Dir()

			d := newTraceDaemon(t)
			synctest.Test(t, func(t *testing.T) {
				stopDaemonBackground(t, d)
				id := "sess-halted-" + tc.name
				addCharacterizationSession(t, d, id, tc.agent, protocol.SessionStateWorking)
				session := d.store.Get(id)

				// The turn the user is about to halt. Its closing hook is the one that
				// never arrives.
				d.recordBracketEvidence(id, protocol.StateWorking)

				startedAt := time.Now()
				path := tc.seed(t, home, session.Directory, id)
				d.store.SetResumeSessionID(id, id)
				d.startTranscriptWatcher(id, tc.agent, session.Directory, startedAt)
				t.Cleanup(func() { d.stopTranscriptWatcher(id) })

				// Let the watcher find the file before the abort lands, so the test
				// exercises the tail rather than the bootstrap read.
				requireTranscriptDiscovery(t, d, id)
				writeLine(t, path, tc.abort(time.Now()))

				evidence := requireAbortEvidence(t, d, id)
				if evidence.TurnOpen || evidence.ToolOpen {
					t.Fatalf("the halted turn left a bracket open: %+v", evidence)
				}

				d.resolveAllSessions(time.Now())
				if state := d.store.Get(id).State; state != protocol.SessionStateIdle {
					t.Fatalf("state %q, want idle: a halted turn is over the moment it is halted", state)
				}
				if got := sessionstate.Resolve(evidence, sessionstate.PolicyFor(string(tc.agent)), time.Now()); got.Reason != sessionstate.ReasonTurnAborted {
					t.Fatalf("reason %q, want turn_aborted: the diagnosis is half the point", got.Reason)
				}
			})
		})
	}
}

func TestWatcherInvalidatesMessagesWhileSessionStaysWorking(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	home, _ := toolhome.Dir()
	d := newTraceDaemon(t)

	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "sess-working-commentary"
		addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
		session := d.store.Get(id)

		var seed func(t *testing.T, home, dir, sessionID string) string
		for _, tc := range haltedTurnCases() {
			if tc.name == "codex" {
				seed = tc.seed
			}
		}
		path := seed(t, home, session.Directory, id)
		d.store.SetResumeSessionID(id, id)
		d.startTranscriptWatcher(id, protocol.SessionAgentCodex, session.Directory, time.Now())
		requireTranscriptDiscovery(t, d, id)
		baseline, err := d.store.BusEventsSince(0, 100)
		if err != nil {
			t.Fatalf("BusEventsSince baseline: %v", err)
		}
		var afterSeq int64
		if len(baseline) > 0 {
			afterSeq = baseline[len(baseline)-1].Seq
		}

		// Codex can write the same completed prose in paired native records. One
		// transcript poll invalidates once, not once per line.
		writeLine(t, path, `{"type":"event_msg","payload":{"type":"agent_message","message":"Checking the current renderer."}}`)
		writeLine(t, path, `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checking the current renderer."}]}}`)
		advancePolls(2)

		events, err := d.store.BusEventsSince(afterSeq, 10)
		if err != nil {
			t.Fatalf("BusEventsSince: %v", err)
		}
		var invalidations int
		for _, event := range events {
			if event.Name == FactSessionAssistantWindowChanged && event.Subject == id {
				invalidations++
			}
		}
		if invalidations != 1 {
			t.Fatalf("assistant-message invalidations = %d, want 1; events=%+v", invalidations, events)
		}
		if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
			t.Fatalf("state = %q, want working throughout", state)
		}
	})
}

func TestWatcherLifecycleRequiresExactNativeTranscriptIdentity(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	home, _ := toolhome.Dir()
	d := newTraceDaemon(t)

	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "sess-exact-lifecycle"
		nativeID := "native-exact-lifecycle"
		addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
		session := d.store.Get(id)

		// A rollout in the same cwd is not authority for this session.
		var seed func(t *testing.T, home, dir, sessionID string) string
		for _, tc := range haltedTurnCases() {
			if tc.name == "codex" {
				seed = tc.seed
			}
		}
		seed(t, home, session.Directory, "neighboring-native-session")

		d.startTranscriptWatcher(id, protocol.SessionAgentCodex, session.Directory, time.Now())
		d.watchersMu.Lock()
		watcher := d.transcriptWatch[id]
		d.watchersMu.Unlock()
		if got := watcher.snapshot().Status; got != protocol.SessionMessageWindowStatusDiscovering {
			t.Fatalf("initial status = %q, want discovering", got)
		}

		advancePolls(5)
		if got := watcher.snapshot(); got.Status != protocol.SessionMessageWindowStatusUnavailable || len(got.Messages) != 0 {
			t.Fatalf("without native identity = %+v, want unavailable and empty", got)
		}

		path := seed(t, home, session.Directory, nativeID)
		writeLine(t, path, `{"type":"event_msg","payload":{"type":"agent_message","message":"Exact live commentary."}}`)
		d.store.SetResumeSessionID(id, nativeID)
		advancePolls(2)
		ready := watcher.snapshot()
		if ready.Status != protocol.SessionMessageWindowStatusReady || len(ready.Messages) != 1 || ready.Messages[0].Content != "Exact live commentary." {
			t.Fatalf("exact transcript snapshot = %+v", ready)
		}

		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		advancePolls(1)
		if got := watcher.snapshot(); got.Status != protocol.SessionMessageWindowStatusUnavailable || len(got.Messages) != 0 {
			t.Fatalf("after transcript disappearance = %+v, want unavailable and empty", got)
		}

		d.stopTranscriptWatcher(id)
		synctest.Wait()
		select {
		case <-watcher.doneCh:
		default:
			t.Fatal("watcher did not stop")
		}
	})
}

// The watcher re-reads history as a matter of course — it rewinds a bootstrap
// window behind the end of the file the moment it discovers one, and starts over
// at offset zero whenever a transcript shrinks. A codex session resumed onto an
// existing rollout replays every halt in that window. None of them are this
// session's, and filing one settles a session that is working right now.
func TestAHaltFromBeforeTheSessionStartedIsIgnored(t *testing.T) {
	for _, tc := range haltedTurnCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(toolhome.EnvVar, t.TempDir())
			home, _ := toolhome.Dir()

			d := newTraceDaemon(t)
			synctest.Test(t, func(t *testing.T) {
				stopDaemonBackground(t, d)
				id := "sess-replayed-halt-" + tc.name
				addCharacterizationSession(t, d, id, tc.agent, protocol.SessionStateWorking)
				session := d.store.Get(id)
				d.recordBracketEvidence(id, protocol.StateWorking)

				// The transcript already holds a halt from an earlier life, and the
				// watcher starts after it — exactly what a resume looks like.
				startedAt := time.Now()
				path := tc.seed(t, home, session.Directory, id)
				writeLine(t, path, tc.abort(startedAt.Add(-2*time.Hour)))

				d.store.SetResumeSessionID(id, id)
				d.startTranscriptWatcher(id, tc.agent, session.Directory, startedAt)
				t.Cleanup(func() { d.stopTranscriptWatcher(id) })
				requireTranscriptDiscovery(t, d, id)

				// Long enough for several polls to have read the bootstrap window.
				advancePolls(4)

				got, ok := d.evidenceTable().snapshot(id)
				if !ok {
					t.Fatal("no evidence recorded")
				}
				if got.LastHarnessEvent != nil && got.LastHarnessEvent.Claim == sessionstate.ClaimTurnAborted {
					t.Fatal("a halt replayed out of history settled a session that is working")
				}
				if !got.TurnOpen {
					t.Fatal("the open turn was closed by a halt from a previous session")
				}
			})
		})
	}
}

// An undated halt cannot be told from one replayed out of history, so it is not
// believed. Every agent attn watches dates its lines; losing the feature loudly
// on a format that stops doing so beats settling live sessions on last week's ESC.
func TestAnUndatedHaltIsIgnored(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	home, _ := toolhome.Dir()

	d := newTraceDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "sess-undated-halt"
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		session := d.store.Get(id)
		d.recordBracketEvidence(id, protocol.StateWorking)

		projects := filepath.Join(home, ".claude", "projects", "a-project")
		if err := os.MkdirAll(projects, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := filepath.Join(projects, id+".jsonl")
		writeLine(t, path, `{"type":"user","message":{"role":"user","content":"write an essay"}}`)

		d.store.SetResumeSessionID(id, id)
		d.startTranscriptWatcher(id, protocol.SessionAgentClaude, session.Directory, time.Now())
		t.Cleanup(func() { d.stopTranscriptWatcher(id) })
		requireTranscriptDiscovery(t, d, id)

		writeLine(t, path, `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"interruptedMessageId":"msg_01"}`)
		advancePolls(4)

		got, ok := d.evidenceTable().snapshot(id)
		if !ok {
			t.Fatal("no evidence recorded")
		}
		if got.LastHarnessEvent != nil && got.LastHarnessEvent.Claim == sessionstate.ClaimTurnAborted {
			t.Fatal("an undated halt was filed as though it had just happened")
		}
	})
}

// The halt is dated by the agent, not by the poll that read it. Half a second of
// polling is long enough for the user to have typed again, and a halt filed with
// the read time outranks the busy frames of the turn that followed it.
func TestAHaltIsDatedByTheAgentNotTheRead(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-halt-dating"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	halted := time.Now().Add(-3 * time.Second)
	d.recordTurnAbortedEvidence(id, "[Request interrupted by user]", halted, time.Now())

	got, ok := d.evidenceTable().snapshot(id)
	if !ok || got.LastHarnessEvent == nil {
		t.Fatal("the halt was not recorded")
	}
	if !got.LastHarnessEvent.ObservedAt.Equal(halted) {
		t.Fatalf("observed at %s, want the agent's own %s", got.LastHarnessEvent.ObservedAt, halted)
	}
	// The movement clock still has to advance to now, or a halt read out of
	// history would age the session into `stuck`.
	if got.LastMovement.Before(halted.Add(time.Second)) {
		t.Fatalf("last movement %s, want the read time: a late read must not age the session", got.LastMovement)
	}

	// The user typed again right after halting, and the agent is visibly running.
	// The halt must not outrank that.
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: time.Now()})
	evidence, _ := d.evidenceTable().snapshot(id)
	if got := sessionstate.Resolve(evidence, sessionstate.PolicyFor(string(protocol.SessionAgentClaude)), time.Now()); got.Reason == sessionstate.ReasonTurnAborted {
		t.Fatalf("a halt the agent has already run past still settled the session: %+v", got)
	}
}

// Copilot aborts for its own reasons too, and those are not the user halting the
// turn — but they end it just the same, and the bracket they leave behind is the
// watcher's own: nothing but this closes it, because copilot's turn_start is what
// opened it and no `assistant.turn_end` follows an abort.
//
// It is not a bracket that expires. Copilot paints no heartbeat, so the resolver's
// stale test can never retire it — `heartbeatSilentFor` answers "not silent"
// forever for a session that has nothing to have gone quiet from — and the session
// runs out the stuck timer and reports `unknown`. So the abort has to say the turn
// is over, without saying the user did it.
func TestACopilotAbortNobodyAskedForStillClosesTheTurn(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	home, _ := toolhome.Dir()

	d := newTraceDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "sess-copilot-tool-failure"
		addCharacterizationSession(t, d, id, protocol.SessionAgentCopilot, protocol.SessionStateWorking)
		session := d.store.Get(id)
		d.recordBracketEvidence(id, protocol.StateWorking)

		var seed func(t *testing.T, home, dir, sessionID string) string
		for _, tc := range haltedTurnCases() {
			if tc.name == "copilot" {
				seed = tc.seed
			}
		}
		path := seed(t, home, session.Directory, id)

		d.store.SetResumeSessionID(id, id)
		d.startTranscriptWatcher(id, protocol.SessionAgentCopilot, session.Directory, time.Now())
		t.Cleanup(func() { d.stopTranscriptWatcher(id) })
		requireTranscriptDiscovery(t, d, id)

		writeLine(t, path, fmt.Sprintf(
			`{"type":"abort","timestamp":%q,"data":{"reason":"tool_failure"}}`,
			time.Now().UTC().Format(time.RFC3339Nano),
		))

		got := requireClosedTurnBracket(t, d, id)
		if got.LastHarnessEvent != nil && got.LastHarnessEvent.Claim == sessionstate.ClaimTurnAborted {
			t.Fatal("copilot giving up on its own was filed as the user halting the turn")
		}

		// The bracket is what pins the session, and for copilot it pins it from the
		// moment the abort lands: with no heartbeat to go silent, the resolver's stale
		// test cannot retire the bracket at all, so `bracket_open` holds until the
		// stuck timer converts it to `unknown`. Settling the turn is the classifier's
		// job — this only has to prove nothing is still claiming the turn is running.
		policy := sessionstate.PolicyFor(string(protocol.SessionAgentCopilot))
		if reason := sessionstate.Resolve(got, policy, time.Now()).Reason; reason == sessionstate.ReasonBracketOpen {
			t.Fatalf("reason %q: the abandoned turn held the session open", reason)
		}
	})
}

// requireClosedTurnBracket asserts the watcher has closed the evidence brackets,
// which for copilot is the only thing that ends an abandoned turn.
func requireClosedTurnBracket(t *testing.T, d *Daemon, sessionID string) sessionstate.Evidence {
	t.Helper()
	advancePolls(2)
	got, ok := d.evidenceTable().snapshot(sessionID)
	if !ok || got.TurnOpen || got.ToolOpen {
		t.Fatal("the abandoned turn left its bracket open")
	}
	return got
}

// A user who quotes the marker back at claude — asking about it, pasting a log —
// must not settle their own session. The dedicated field is what tells the two
// apart, and this is the case that would make the fix worse than the bug.
func TestATranscriptLineThatMerelyMentionsTheMarkerIsNotAHalt(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	home, _ := toolhome.Dir()

	d := newTraceDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "sess-marker-mention"
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		session := d.store.Get(id)
		d.recordBracketEvidence(id, protocol.StateWorking)

		projects := filepath.Join(home, ".claude", "projects", "a-project")
		if err := os.MkdirAll(projects, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := filepath.Join(projects, id+".jsonl")
		writeLine(t, path, `{"type":"user","message":{"role":"user","content":"why do i see [Request interrupted by user] in my logs?"}}`)

		d.store.SetResumeSessionID(id, id)
		d.startTranscriptWatcher(id, protocol.SessionAgentClaude, session.Directory, time.Now())
		t.Cleanup(func() { d.stopTranscriptWatcher(id) })
		requireTranscriptDiscovery(t, d, id)

		// Long enough for several polls to have read the line and decided nothing.
		advancePolls(4)

		got, ok := d.evidenceTable().snapshot(id)
		if !ok {
			t.Fatal("no evidence recorded")
		}
		if got.LastHarnessEvent != nil && got.LastHarnessEvent.Claim == sessionstate.ClaimTurnAborted {
			t.Fatal("a prompt that quotes the marker was read as the user halting the turn")
		}
		if !got.TurnOpen {
			t.Fatal("the turn was closed by a line that only talked about interrupts")
		}
	})
}

func writeLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// requireTranscriptDiscovery advances past enough of the watcher's own polls for
// it to have adopted a transcript, which is the point from which appended lines
// are tailed. The watcher is a plain time.Ticker over the filesystem — no
// fsnotify — so it runs at its shipped 500ms interval inside a bubble and two
// ticks of fake time cost nothing.
func requireTranscriptDiscovery(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	advancePolls(2)
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.snapshot().Status != protocol.SessionMessageWindowStatusReady {
		t.Fatal("watcher never discovered the transcript")
	}
}

// advancePolls runs n of the transcript watcher's polls to completion.
func advancePolls(n int) {
	time.Sleep(time.Duration(n) * transcriptPollInterval)
	synctest.Wait()
}

func requireAbortEvidence(t *testing.T, d *Daemon, sessionID string) sessionstate.Evidence {
	t.Helper()
	advancePolls(2)
	got, ok := d.evidenceTable().snapshot(sessionID)
	if !ok || got.LastHarnessEvent == nil || got.LastHarnessEvent.Claim != sessionstate.ClaimTurnAborted {
		t.Fatal("the halted turn was never filed as evidence")
	}
	return got
}
