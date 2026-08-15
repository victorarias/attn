package daemon

import (
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestTicketDeadlineFirstEventAfterQuietIsImmediateForEveryParticipant(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBundleWindowOverride = time.Hour
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	for _, sessionID := range []string{"assignee", "subscriber", "chief"} {
		deadline, immediate, err := d.ticketDeadline(sessionID, 1, now)
		if err != nil || !immediate || !deadline.Equal(now.Add(time.Second)) {
			t.Fatalf("%s deadline = %s immediate=%v err=%v", sessionID, deadline, immediate, err)
		}
	}
}

func TestTicketBurstBundlesIntoOneFollowupNudge(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Second
		d.ticketBundleWindowOverride = 10 * time.Minute
		stopDaemonBackground(t, d)
		chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
		d.setSelectedSession(chiefID)
		ticketID := boundTicketID(t, d, agentID)

		commentOnTicket(t, d, ticketID, "first")
		time.Sleep(time.Second)
		synctest.Wait()
		if got := nudgeCount(inputs(agentID)); got != 1 {
			t.Fatalf("first-event nudges = %d, want 1", got)
		}
		if bundles := callTicketInbox(t, d, agentID); len(bundles) != 1 || len(bundles[0].Events) != 1 {
			t.Fatalf("first inbox = %+v, want one event", bundles)
		}

		commentOnTicket(t, d, ticketID, "second")
		time.Sleep(4 * time.Minute)
		commentOnTicket(t, d, ticketID, "third")
		time.Sleep(5*time.Minute + 59*time.Second)
		synctest.Wait()
		if got := nudgeCount(inputs(agentID)); got != 1 {
			t.Fatalf("nudges before bundle deadline = %d, want 1", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := nudgeCount(inputs(agentID)); got != 2 {
			t.Fatalf("nudges after bundle deadline = %d, want 2", got)
		}
		bundles := callTicketInbox(t, d, agentID)
		if len(bundles) != 1 || len(bundles[0].Events) != 2 {
			t.Fatalf("bundled inbox = %+v, want the two burst events", bundles)
		}
	})
}

func TestCompletedTicketInsideBurstStaysBundled(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Second
		d.ticketBundleWindowOverride = 10 * time.Minute
		stopDaemonBackground(t, d)
		chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
		d.setSelectedSession(agentID)
		if bundles := callTicketInbox(t, d, chiefID); len(bundles) != 0 {
			t.Fatalf("initial chief inbox = %+v", bundles)
		}
		if err := d.store.SetTicketDeliveryAttention(d.ticketAttentionKey(chiefID), time.Now()); err != nil {
			t.Fatal(err)
		}

		callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateCompleted), "done")
		deadline := settledNudgeDeadline(t, d, chiefID)
		if want := time.Now().Add(10 * time.Minute); !deadline.Equal(want) {
			t.Fatalf("completed-ticket deadline = %s, want bundled %s", deadline, want)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := nudgeCount(inputs(chiefID)); got != 0 {
			t.Fatalf("completed-ticket nudges before bundle deadline = %d, want 0", got)
		}
	})
}

func TestTicketNudgeReturnsToImmediateAfterQuiet(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Second
		d.ticketBundleWindowOverride = 10 * time.Minute
		stopDaemonBackground(t, d)
		chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
		d.setSelectedSession(chiefID)
		ticketID := boundTicketID(t, d, agentID)

		commentOnTicket(t, d, ticketID, "first")
		time.Sleep(time.Second)
		synctest.Wait()
		_ = callTicketInbox(t, d, agentID)
		time.Sleep(10 * time.Minute)

		commentOnTicket(t, d, ticketID, "after quiet")
		deadline := settledNudgeDeadline(t, d, agentID)
		if want := time.Now().Add(time.Second); !deadline.Equal(want) {
			t.Fatalf("deadline after quiet = %s, want %s", deadline, want)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := nudgeCount(inputs(agentID)); got != 2 {
			t.Fatalf("nudges after quiet = %d, want 2", got)
		}
	})
}

func TestTicketWatchConsumesImmediatelyInsideBundleWindow(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBundleWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, _ := delegateForNotify(t, d, "claude")
	ticketID := boundTicketID(t, d, agentID)
	if err := d.store.SetTicketDeliveryAttention(d.ticketAttentionKey(agentID), time.Now()); err != nil {
		t.Fatal(err)
	}

	commentOnTicket(t, d, ticketID, "deliver through watch")
	deadline := currentNudgeDeadline(d, agentID)
	if deadline.Before(time.Now().Add(59 * time.Minute)) {
		t.Fatalf("countdown deadline = %s, want bundled", deadline)
	}

	watch := protocol.TicketInboxModeWatch
	bundles := callTicketInboxMode(t, d, agentID, &watch)
	if len(bundles) != 1 || bundles[0].TicketID != ticketID || len(bundles[0].Events) != 1 {
		t.Fatalf("watch bundles = %+v, want immediate unread activity", bundles)
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("watch consume left bundled countdown armed")
	}
}

func TestDeliveredUnreadDoesNotRearmUntilNewActivity(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Second
		d.ticketBundleWindowOverride = 10 * time.Minute
		stopDaemonBackground(t, d)
		chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
		d.setSelectedSession(chiefID)
		ticketID := boundTicketID(t, d, agentID)

		commentOnTicket(t, d, ticketID, "first")
		time.Sleep(time.Second)
		synctest.Wait()
		if got := nudgeCount(inputs(agentID)); got != 1 {
			t.Fatalf("initial nudges = %d, want 1", got)
		}

		d.notifyUnreadTicketSession(agentID, time.Now())
		if deadline := currentNudgeDeadline(d, agentID); !deadline.IsZero() {
			t.Fatalf("already delivered unread event rearmed for %s", deadline)
		}

		commentOnTicket(t, d, ticketID, "new activity")
		deadline := settledNudgeDeadline(t, d, agentID)
		if want := time.Now().Add(10 * time.Minute); !deadline.Equal(want) {
			t.Fatalf("new activity deadline = %s, want %s", deadline, want)
		}
	})
}

// Boundary-bound: the claim under test is that the catch-up goroutine stays
// parked on d.deliveryMu while reconstruction holds it. A goroutine blocked on a
// sync.Mutex is explicitly NOT durably blocked, so a bubble cannot tell "still
// waiting for the lock" from "about to run" — synctest.Wait would never return.
// The 100ms here is the wall-clock price of an assertion the fake clock cannot
// make.
func TestMutationCatchUpRebuildsRemainingUnreadFromNewAttention(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBundleWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	sessionID := "observer"
	d.store.Add(&protocol.Session{ID: sessionID, Label: sessionID, Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})
	now := time.Now()
	for _, ticketID := range []string{"first", "remaining"} {
		if _, err := d.store.CreateTicket(store.Ticket{ID: ticketID, Title: ticketID, Status: store.TicketStatusWorking}, "creator", now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := d.store.AddTicketSubscription(sessionID, ticketID, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.store.AddTicketComment(ticketID, "creator", "update", now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.store.SetTicketDeliveryAttention(sessionID, now.Add(-50*time.Minute)); err != nil {
		t.Fatal(err)
	}
	staleDeadline := make(chan time.Time, 1)
	resumeStaleRebuild := make(chan struct{})
	var pauseOnce sync.Once
	d.ticketRebuildBeforeArmHook = func(gotSessionID string, deadline time.Time) {
		if gotSessionID != sessionID {
			return
		}
		pauseOnce.Do(func() {
			staleDeadline <- deadline
			<-resumeStaleRebuild
		})
	}
	rebuildDone := make(chan struct{})
	go func() {
		d.notifyUnreadTicketSession(sessionID, now)
		close(rebuildDone)
	}()
	oldDeadline := <-staleDeadline
	if oldDeadline.IsZero() || oldDeadline.After(now.Add(11*time.Minute)) {
		t.Fatalf("old deadline = %s, want about ten minutes", oldDeadline)
	}

	type catchUpResult struct {
		outcome store.TicketMutationOutcome
		err     error
	}
	catchUpStarted := make(chan struct{})
	catchUpDone := make(chan catchUpResult, 1)
	go func() {
		close(catchUpStarted)
		d.deliveryMu.Lock()
		_, outcome, err := d.store.AddTicketCommentWithOptions(
			"first", sessionID, "should not land", d.ticketMutationOptions(sessionID), now,
		)
		if err == nil && len(outcome.CatchUp) > 0 {
			d.afterTicketMutationCatchUpLocked(sessionID, outcome.CatchUp)
		}
		d.deliveryMu.Unlock()
		catchUpDone <- catchUpResult{outcome: outcome, err: err}
	}()
	<-catchUpStarted
	select {
	case result := <-catchUpDone:
		t.Fatalf("catch-up crossed paused reconstruction: outcome=%+v err=%v", result.outcome, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resumeStaleRebuild)
	<-rebuildDone
	result := <-catchUpDone
	if result.err != nil || len(result.outcome.CatchUp) == 0 {
		t.Fatalf("mutation outcome = %+v err=%v, want catch-up", result.outcome, result.err)
	}

	newDeadline := currentNudgeDeadline(d, sessionID)
	if newDeadline.Before(now.Add(59 * time.Minute)) {
		t.Fatalf("rebuilt deadline = %s, want fresh attention window", newDeadline)
	}
}

func TestExplicitInboxConsumesDuringBundleWindow(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Second
		d.ticketBundleWindowOverride = time.Hour
		stopDaemonBackground(t, d)
		chiefID, agentID, _ := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		if bundles := callTicketInbox(t, d, chiefID); len(bundles) != 0 {
			t.Fatalf("initial chief inbox = %+v", bundles)
		}
		if err := d.store.SetTicketDeliveryAttention(d.ticketAttentionKey(chiefID), time.Now()); err != nil {
			t.Fatal(err)
		}
		d.setSelectedSession(agentID)
		callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateNeedsInput), "need a decision")
		deadline := settledNudgeDeadline(t, d, chiefID)
		if deadline.Before(time.Now().Add(59 * time.Minute)) {
			t.Fatalf("needs-input deadline = %s, want bundled", deadline)
		}

		bundles := callTicketInbox(t, d, chiefID)
		if len(bundles) != 1 || bundles[0].TicketID != ticketID {
			t.Fatalf("explicit inbox = %+v, want immediate catch-up", bundles)
		}
		if currentNudgeTimer(d, chiefID) != nil {
			t.Fatal("explicit inbox left the bundled countdown armed")
		}
	})
}
