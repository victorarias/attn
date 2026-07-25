package daemon

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
)

// The gate's own rules, stated without a daemon around them.
func TestDwellGateHoldsATransitionUntilItHasBeenTrueLongEnough(t *testing.T) {
	g := newDwellGate()
	now := time.Now()

	if g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now) {
		t.Fatal("published on the first tick: nothing has been true for a minute yet")
	}
	if g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now.Add(59*time.Second)) {
		t.Fatal("published a second early")
	}
	if !g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now.Add(time.Minute)) {
		t.Fatal("still held once the dwell had elapsed")
	}
}

// A different answer replaces the wait rather than extending it. This is what
// cancels a guardian-answered approval: the resolver starts saying `working`,
// and the approval it was holding is dropped without ever being shown.
func TestDwellGateDropsATransitionThatStoppedBeingTheAnswer(t *testing.T) {
	g := newDwellGate()
	now := time.Now()

	g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now)
	// `working` carries no dwell, so it publishes at once...
	if !g.ready("s", protocol.SessionStateWorking, 0, now.Add(time.Second)) {
		t.Fatal("a dwell-free transition was held")
	}
	// ...and the approval's clock is gone, not merely paused. A later approval
	// starts its own minute instead of inheriting the first one's.
	if g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now.Add(2*time.Minute)) {
		t.Fatal("a fresh approval inherited the abandoned clock and published immediately")
	}
}

// A zero dwell is the no-reviewer case, and it must not merely pass — it has to
// clear a wait in progress, or a session that drops its reviewer keeps serving
// out a dwell nothing is asking for any more.
func TestDwellGateClearsAWaitWhenTheDwellGoesToZero(t *testing.T) {
	g := newDwellGate()
	now := time.Now()

	g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now)
	if !g.ready("s", protocol.SessionStatePendingApproval, 0, now.Add(time.Second)) {
		t.Fatal("a zero dwell was held")
	}
	// Re-arming finds no clock to resume, so it starts a fresh one rather than
	// publishing on the strength of the wait the zero dwell passed through.
	if g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now.Add(2*time.Second)) {
		t.Fatal("a re-armed dwell published immediately: the old wait was still counting")
	}
}

// The wire the whole dwell hangs off for codex, which reports no permission
// mode to the daemon at any point in its life: unless the spawn files who is
// reviewing, the resolver never learns there is a guardian at all and every
// guardian-answered request flashes.
func TestSpawnFilesWhoAnswersApprovals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		autoApprove bool
		yolo        bool
		want        bool
	}{
		{name: "guardian", autoApprove: true, want: true},
		{name: "user answers", autoApprove: false, want: false},
		// Yolo removes the approval gate rather than delegating it, so there is
		// nothing for a reviewer to answer and nothing to dwell on.
		{name: "yolo outranks auto-approve", autoApprove: true, yolo: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			t.Cleanup(func() { _ = d.store.Close() })
			d.ptyBackend = &fakeSpawnBackend{}
			cwd := t.TempDir()
			addTestWorkspace(d, "workspace", cwd)
			d.store.SetSetting(SettingAutoApproveEnabled, strconv.FormatBool(tc.autoApprove))

			msg := &protocol.SpawnSessionMessage{
				Cmd:         protocol.CmdSpawnSession,
				ID:          "spawn-reviewer-" + tc.name,
				Cwd:         cwd,
				Agent:       "codex",
				WorkspaceID: "workspace",
				Cols:        80,
				Rows:        24,
				YoloMode:    protocol.Ptr(tc.yolo),
			}
			client := spawnTestClient()
			d.handleSpawnSession(client, msg)
			expectSpawnResult(t, client, msg.ID, true)

			if got := evidenceOf(t, d, msg.ID).ReviewerInLoop; got != tc.want {
				t.Fatalf("ReviewerInLoop = %v, want %v", got, tc.want)
			}
		})
	}
}

// Codex's hooks send `permission_mode: default` on every turn as payload
// filler, while its reviewer is set by the approvals_reviewer flag at launch.
// Believing that field would retire the spawn-time fact on the session's first
// turn and take the dwell with it — which is exactly what a live guardian run
// showed before this guard existed.
func TestCodexPermissionModeDoesNotRetireTheSpawnTimeReviewer(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-codex-mode"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	d.recordReviewerEvidence(id, true)
	d.recordReviewerEvidenceFromPermissionMode(id, "default")

	if !evidenceOf(t, d, id).ReviewerInLoop {
		t.Fatal("codex's filler permission mode retired the reviewer recorded at spawn")
	}
}

// Claude's mode is a genuine report, and a user switching back to answering for
// themselves mid-session must take effect.
func TestClaudePermissionModeStillRetiresTheReviewer(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-claude-mode"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	d.recordReviewerEvidence(id, true)
	d.recordReviewerEvidenceFromPermissionMode(id, "default")

	if evidenceOf(t, d, id).ReviewerInLoop {
		t.Fatal("claude reported default and kept its reviewer")
	}
}

// The reported bug, end to end: an unattended codex run asks permission on a
// tool call, its guardian answers in milliseconds, and the user must never see
// the session demand attention for it.
func TestAGuardianAnsweredApprovalIsNeverPublished(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-guardian"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.recordReviewerEvidence(id, true)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "approval", At: now, Detail: "Action Required"})

	// A tick while the guardian is thinking. The approval is the resolver's
	// answer, and it is deliberately not published.
	d.resolveAllSessions(now.Add(time.Second))
	if state := d.store.Get(id).State; state == protocol.SessionStatePendingApproval {
		t.Fatal("the guardian's approval was shown to the user")
	}

	// The guardian answers: codex paints a busy frame again, which retires the
	// approval, and the session was never yellow.
	answeredAt := now.Add(2 * time.Second)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: answeredAt})
	d.resolveAllSessions(answeredAt.Add(time.Second))
	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working once the guardian answered", state)
	}
}

// The other half: a guardian that does not answer must not hide the request
// forever. The dwell delays the color, it does not suppress it.
func TestAnUnansweredApprovalIsPublishedOnceTheDwellElapses(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-guardian-silent"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.recordReviewerEvidence(id, true)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "approval", At: now, Detail: "Action Required"})

	policy := sessionstate.PolicyFor(string(protocol.SessionAgentCodex))
	d.resolveAllSessions(now.Add(time.Second))
	d.resolveAllSessions(now.Add(time.Second + policy.GuardianDwell))

	if state := d.store.Get(id).State; state != protocol.SessionStatePendingApproval {
		t.Fatalf("state %q, want pending_approval: the dwell delays the request, it does not swallow it", state)
	}
}

// A session closed mid-dwell must not leave its pending transition behind. The
// resolve loop's own cleanup cannot do it: that loop walks the evidence table,
// and removal forgets the evidence row, so the tick never visits this session
// again. Without a clear in the removal path the entry outlives the daemon's
// interest in it, and enough short-lived unattended sessions turn a transient UX
// gate into permanent state.
func TestClosingASessionMidDwellLeavesNothingBehind(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-dwell-closed"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.recordReviewerEvidence(id, true)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "approval", At: now, Detail: "Action Required"})

	d.resolveAllSessions(now.Add(time.Second))
	if !d.dwellGate().waiting(id) {
		t.Fatal("no dwell was armed, so the test cannot show one being cleaned up")
	}

	d.dropSessionRecord(id)

	if d.dwellGate().waiting(id) {
		t.Fatal("the closed session's dwell is still pending")
	}
	// And the tick that would have cleaned it up never comes, which is the whole
	// reason the removal path has to do it.
	d.resolveAllSessions(now.Add(2 * time.Second))
	if d.dwellGate().waiting(id) {
		t.Fatal("the dwell survived a later tick")
	}
}

// With nobody but the user to answer, the request is the user's the instant it
// arrives. A dwell here would be a regression, not a refinement.
func TestAnApprovalWithNoReviewerPublishesImmediately(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-no-reviewer"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.recordReviewerEvidence(id, false)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "approval", At: now, Detail: "Action Required"})

	d.resolveAllSessions(now.Add(time.Second))

	if state := d.store.Get(id).State; state != protocol.SessionStatePendingApproval {
		t.Fatalf("state %q, want pending_approval on the first tick", state)
	}
}
