package daemon

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

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

func TestDwellGateDropsATransitionThatStoppedBeingTheAnswer(t *testing.T) {
	g := newDwellGate()
	now := time.Now()

	g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now)
	if !g.ready("s", protocol.SessionStateWorking, 0, now.Add(time.Second)) {
		t.Fatal("a dwell-free transition was held")
	}
	if g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now.Add(2*time.Minute)) {
		t.Fatal("a fresh approval inherited the abandoned clock and published immediately")
	}
}

func TestDwellGateClearsAWaitWhenTheDwellGoesToZero(t *testing.T) {
	g := newDwellGate()
	now := time.Now()

	g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now)
	if !g.ready("s", protocol.SessionStatePendingApproval, 0, now.Add(time.Second)) {
		t.Fatal("a zero dwell was held")
	}
	if g.ready("s", protocol.SessionStatePendingApproval, time.Minute, now.Add(2*time.Second)) {
		t.Fatal("a re-armed dwell published immediately: the old wait was still counting")
	}
}

func TestSpawnFilesWhoAnswersApprovals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		autoApprove bool
		yolo        bool
		want        bool
		wantRoute   launchcontract.ApprovalRoute
	}{
		{name: "guardian", autoApprove: true, want: true, wantRoute: launchcontract.ApprovalRouteReviewer},
		{name: "user answers", autoApprove: false, want: false, wantRoute: launchcontract.ApprovalRouteUser},
		{name: "yolo outranks auto-approve", autoApprove: true, yolo: true, want: false, wantRoute: launchcontract.ApprovalRouteBypass},
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
			intent, ok := d.store.LaunchIntent(msg.ID)
			if !ok || intent.ApprovalRoute != tc.wantRoute {
				t.Fatalf("ApprovalRoute = %q, ok=%v, want %q", intent.ApprovalRoute, ok, tc.wantRoute)
			}
		})
	}
}

// Codex sends `permission_mode: default` on every turn as payload filler, while
// its reviewer is set by the approvals_reviewer flag at launch.
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

func TestAGuardianAnsweredApprovalIsNeverPublished(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-guardian"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)

	now := time.Now()
	d.recordReviewerEvidence(id, true)
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "approval", At: now, Detail: "Action Required"})

	d.resolveAllSessions(now.Add(time.Second))
	if state := d.store.Get(id).State; state == protocol.SessionStatePendingApproval {
		t.Fatal("the guardian's approval was shown to the user")
	}

	answeredAt := now.Add(2 * time.Second)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: answeredAt})
	d.resolveAllSessions(answeredAt.Add(time.Second))
	if state := d.store.Get(id).State; state != protocol.SessionStateWorking {
		t.Fatalf("state %q, want working once the guardian answered", state)
	}
}

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

	d.closeSession(id, store.SessionClose{By: store.SessionClosedByUser})

	if d.dwellGate().waiting(id) {
		t.Fatal("the closed session's dwell is still pending")
	}
	d.resolveAllSessions(now.Add(2 * time.Second))
	if d.dwellGate().waiting(id) {
		t.Fatal("the dwell survived a later tick")
	}
}

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
