package daemon

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestTicketDeadlineBuffersObserverButNotAssignee(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBufferWindowOverride = time.Hour
	_, assignee, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, assignee)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)

	deadline, immediate, err := d.ticketDeadline(assignee, ticketID, now, now)
	if err != nil || !immediate || !deadline.Equal(now.Add(time.Second)) {
		t.Fatalf("assignee deadline = %s immediate=%v err=%v", deadline, immediate, err)
	}
	observer := "observer"
	d.store.Add(&protocol.Session{ID: observer, Label: observer, Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})
	if err := d.store.SetTicketDeliveryAttention(observer, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	deadline, immediate, err = d.ticketDeadline(observer, ticketID, now, now)
	if err != nil || immediate || !deadline.Equal(now.Add(50*time.Minute)) {
		t.Fatalf("observer deadline = %s immediate=%v err=%v", deadline, immediate, err)
	}
}

func TestTicketDeadlineBuffersFirstObserverEventFromEventTime(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBufferWindowOverride = time.Hour
	_, assignee, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, assignee)
	observer := "new-observer"
	d.store.Add(&protocol.Session{ID: observer, Label: observer, Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})
	eventAt := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	now := eventAt.Add(5 * time.Minute)

	deadline, immediate, err := d.ticketDeadline(observer, ticketID, eventAt, now)
	if err != nil || immediate || !deadline.Equal(eventAt.Add(time.Hour)) {
		t.Fatalf("first observer deadline = %s immediate=%v err=%v", deadline, immediate, err)
	}
}

func TestMutationCatchUpRebuildsRemainingUnreadFromNewAttention(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBufferWindowOverride = time.Hour
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
		if err == nil && len(outcome.ConflictEvents) > 0 {
			d.afterTicketMutationCatchUpLocked(sessionID, outcome.ConflictEvents)
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
	if result.err != nil || len(result.outcome.ConflictEvents) == 0 {
		t.Fatalf("mutation outcome = %+v err=%v, want catch-up", result.outcome, result.err)
	}

	newDeadline := currentNudgeDeadline(d, sessionID)
	if newDeadline.Before(now.Add(59 * time.Minute)) {
		t.Fatalf("rebuilt deadline = %s, want fresh attention window", newDeadline)
	}
}

func TestExplicitInboxConsumesDuringObserverBuffer(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBufferWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
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
	deadline := waitForNudgeDeadline(t, d, chiefID)
	if deadline.Before(time.Now().Add(59 * time.Minute)) {
		t.Fatalf("needs-input observer deadline = %s, want buffered", deadline)
	}

	bundles := callTicketInbox(t, d, chiefID)
	if len(bundles) != 1 || bundles[0].TicketID != ticketID {
		t.Fatalf("explicit inbox = %+v, want immediate catch-up", bundles)
	}
	if currentNudgeTimer(d, chiefID) != nil {
		t.Fatal("explicit inbox left the buffered countdown armed")
	}
}
