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

	event := store.TicketEvent{TicketID: ticketID, Kind: store.TicketEventCommented, CreatedAt: now}
	deadline, immediate, err := d.ticketDeadline(assignee, event, now)
	if err != nil || !immediate || !deadline.Equal(now.Add(time.Second)) {
		t.Fatalf("assignee deadline = %s immediate=%v err=%v", deadline, immediate, err)
	}
	observer := "observer"
	d.store.Add(&protocol.Session{ID: observer, Label: observer, Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})
	if err := d.store.SetTicketDeliveryAttention(observer, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	deadline, immediate, err = d.ticketDeadline(observer, event, now)
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

	event := store.TicketEvent{TicketID: ticketID, Kind: store.TicketEventCommented, CreatedAt: eventAt}
	deadline, immediate, err := d.ticketDeadline(observer, event, now)
	if err != nil || immediate || !deadline.Equal(eventAt.Add(time.Hour)) {
		t.Fatalf("first observer deadline = %s immediate=%v err=%v", deadline, immediate, err)
	}
}

func TestTicketDeadlineOnlyBypassesObserverBufferForDoneTransition(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Second
	d.ticketBufferWindowOverride = time.Hour
	_, assignee, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, assignee)
	observer := "observer"
	d.store.Add(&protocol.Session{ID: observer, Label: observer, Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	if err := d.store.SetTicketDeliveryAttention(observer, now); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		event     store.TicketEvent
		immediate bool
	}{
		{name: "done", event: store.TicketEvent{Kind: store.TicketEventStatusChanged, ToStatus: store.TicketStatusDone}, immediate: true},
		{name: "working", event: store.TicketEvent{Kind: store.TicketEventStatusChanged, ToStatus: store.TicketStatusWorking}},
		{name: "blocked", event: store.TicketEvent{Kind: store.TicketEventStatusChanged, ToStatus: store.TicketStatusBlocked}},
		{name: "in review", event: store.TicketEvent{Kind: store.TicketEventStatusChanged, ToStatus: store.TicketStatusInReview}},
		{name: "failed", event: store.TicketEvent{Kind: store.TicketEventStatusChanged, ToStatus: store.TicketStatusFailed}},
		{name: "comment", event: store.TicketEvent{Kind: store.TicketEventCommented}},
		{name: "attachment", event: store.TicketEvent{Kind: store.TicketEventAttachSubmitted}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.event.TicketID = ticketID
			tt.event.CreatedAt = now
			deadline, immediate, err := d.ticketDeadline(observer, tt.event, now)
			if err != nil {
				t.Fatal(err)
			}
			if immediate != tt.immediate {
				t.Fatalf("immediate = %v, want %v", immediate, tt.immediate)
			}
			want := now.Add(time.Hour)
			if tt.immediate {
				want = now.Add(time.Second)
			}
			if !deadline.Equal(want) {
				t.Fatalf("deadline = %s, want %s", deadline, want)
			}
		})
	}
}

func TestDoneTransitionPullsObserversForwardAndPiggybacksBufferedActivity(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Minute
	d.ticketBufferWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	chiefID, assignees, _ := delegateMany(t, d, "codex", "finish the design", "investigate the follow-up")
	assignee, otherAssignee := assignees[0], assignees[1]
	ticketID := boundTicketID(t, d, assignee)
	otherTicketID := boundTicketID(t, d, otherAssignee)
	d.setSelectedSession(assignee)

	subscriberID := "subscriber"
	d.store.Add(&protocol.Session{ID: subscriberID, Label: subscriberID, Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})
	for _, subscribedTicketID := range []string{ticketID, otherTicketID} {
		if err := d.store.AddTicketSubscription(subscriberID, subscribedTicketID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, observer := range []string{chiefID, subscriberID} {
		_ = callTicketInbox(t, d, observer)
		if err := d.store.SetTicketDeliveryAttention(d.ticketAttentionKey(observer), time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := d.store.AddTicketComment(ticketID, assignee, "implementation finished", time.Now()); err != nil {
		t.Fatal(err)
	}
	d.notifyTicketObservers(ticketID)
	if _, err := d.store.AddTicketComment(otherTicketID, otherAssignee, "related investigation update", time.Now()); err != nil {
		t.Fatal(err)
	}
	d.notifyTicketObservers(otherTicketID)
	for _, observer := range []string{chiefID, subscriberID} {
		deadline := waitForNudgeDeadline(t, d, observer)
		if deadline.Before(time.Now().Add(59 * time.Minute)) {
			t.Fatalf("%s comment deadline = %s, want buffered", observer, deadline)
		}
	}

	callSetTicketStatus(t, d, assignee, string(protocol.DispatchWorkStateCompleted), "accepted plan attached")
	for _, observer := range []string{chiefID, subscriberID} {
		waitForNudgeDeadlineBefore(t, d, observer, time.Now().Add(61*time.Second))
	}
	// A daemon restart loses only the in-memory countdown. Rebuilding from the
	// durable unread completion event must retain immediate eligibility.
	d.cancelNudgeCountdown(chiefID, "simulate daemon restart")
	d.notifyUnreadTicketSession(chiefID, time.Now())
	waitForNudgeDeadlineBefore(t, d, chiefID, time.Now().Add(61*time.Second))

	watch := protocol.TicketInboxModeWatch
	chiefBundles := callTicketInboxMode(t, d, chiefID, &watch)
	assertDoneFlushBundles(t, chiefBundles, ticketID, assignee, otherTicketID, otherAssignee)
	subscriberBundles := callTicketInbox(t, d, subscriberID)
	assertDoneFlushBundles(t, subscriberBundles, ticketID, assignee, otherTicketID, otherAssignee)
	if again := callTicketInbox(t, d, chiefID); len(again) != 0 {
		t.Fatalf("chief replayed completion = %+v", again)
	}
	if again := callTicketInbox(t, d, subscriberID); len(again) != 0 {
		t.Fatalf("subscriber replayed completion = %+v", again)
	}
}

func waitForNudgeDeadlineBefore(t *testing.T, d *Daemon, sessionID string, before time.Time) time.Time {
	t.Helper()
	limit := time.Now().Add(time.Second)
	for time.Now().Before(limit) {
		if deadline := currentNudgeDeadline(d, sessionID); !deadline.IsZero() && deadline.Before(before) {
			return deadline
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s deadline = %s, want before %s", sessionID, currentNudgeDeadline(d, sessionID), before)
	return time.Time{}
}

func assertDoneFlushBundles(t *testing.T, bundles []protocol.TicketEventBundle, ticketID, assignee, otherTicketID, otherAssignee string) {
	t.Helper()
	if len(bundles) != 2 {
		t.Fatalf("bundles = %+v, want completed ticket plus other buffered ticket", bundles)
	}
	byTicket := make(map[string]protocol.TicketEventBundle, len(bundles))
	for _, bundle := range bundles {
		byTicket[bundle.TicketID] = bundle
	}
	completed := byTicket[ticketID]
	if len(completed.Events) != 2 {
		t.Fatalf("completed bundle = %+v, want buffered comment plus done event", completed)
	}
	comment, done := completed.Events[0], completed.Events[1]
	if comment.Kind != protocol.TicketEventKindCommented || comment.Comment == nil || *comment.Comment != "implementation finished" {
		t.Fatalf("piggybacked event = %+v, want original comment provenance", comment)
	}
	if done.Kind != protocol.TicketEventKindStatusChanged || done.Author != assignee || done.ToStatus == nil || *done.ToStatus != protocol.TicketStatusDone || done.Comment == nil || *done.Comment != "accepted plan attached" {
		t.Fatalf("completion event = %+v, want full done provenance", done)
	}
	other := byTicket[otherTicketID]
	if len(other.Events) != 1 || other.Events[0].Kind != protocol.TicketEventKindCommented || other.Events[0].Author != otherAssignee || other.Events[0].Comment == nil || *other.Events[0].Comment != "related investigation update" {
		t.Fatalf("other buffered bundle = %+v, want cross-ticket update flushed intact", other)
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
