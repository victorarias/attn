package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

type tourWakeBackend struct {
	fakeSpawnBackend
	snapshot   ptybackend.AttachInfo
	onSnapshot func()
}

func (b *tourWakeBackend) Snapshot(_ context.Context, _ string) (ptybackend.AttachInfo, error) {
	if b.onSnapshot != nil {
		b.onSnapshot()
	}
	return b.snapshot, nil
}

func setupTourWakeTest(
	t *testing.T,
	agent string,
	state protocol.SessionState,
) (*Daemon, *tourWakeBackend, *protocol.TourRun) {
	t.Helper()

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	snapshot, ok := pty.ScreenSnapshotFromReplay([]byte("All done.\r\n› "), 80, 24)
	if !ok {
		t.Fatal("build empty editor snapshot")
	}
	backend := &tourWakeBackend{
		snapshot: ptybackend.AttachInfo{
			ScreenSnapshot:      snapshot.Payload,
			ScreenCols:          snapshot.Cols,
			ScreenRows:          snapshot.Rows,
			ScreenCursorX:       snapshot.CursorX,
			ScreenCursorY:       snapshot.CursorY,
			ScreenCursorVisible: snapshot.CursorVisible,
			ScreenSnapshotFresh: true,
		},
	}
	d.ptyBackend = backend
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "tour-session",
		Label:          "Tour agent",
		Agent:          agent,
		Directory:      t.TempDir(),
		WorkspaceID:    "tour-workspace",
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	run, err := d.store.CreateOrOpenTour(
		"tour-session",
		"Review tour",
		t.TempDir(),
		filepath.Join(t.TempDir(), "guide.yml"),
		"main",
		store.TourSnapshot{},
	)
	if err != nil {
		t.Fatalf("CreateOrOpenTour() error = %v", err)
	}
	return d, backend, run
}

func TestSubmitTourWakesIdleAgentWithDurableEventReference(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateWaitingInput)
	inputs := make(chan string, 1)
	backend.onInput = func(sessionID string, data []byte) {
		if sessionID != run.SessionID {
			t.Errorf("Input() session = %q, want %q", sessionID, run.SessionID)
		}
		inputs <- string(data)
	}

	event, _, err := d.submitTour(&protocol.SubmitTourMessage{
		Cmd:    protocol.CmdSubmitTour,
		TourID: run.TourID,
		Body:   "Feedback body must not reach the PTY.",
	})
	if err != nil {
		t.Fatalf("submitTour() error = %v", err)
	}

	var prompt string
	select {
	case prompt = <-inputs:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake prompt")
	}
	if !strings.HasSuffix(prompt, "\r") {
		t.Fatalf("wake input = %q, want atomic prompt with carriage return", prompt)
	}
	if !strings.Contains(prompt, `tour event --tour "`+run.TourID+`" --event "`+event.ID+`"`) {
		t.Fatalf("wake prompt missing durable event command: %q", prompt)
	}
	if strings.Contains(prompt, event.Markdown) {
		t.Fatalf("wake prompt included feedback body: %q", prompt)
	}

	stored, _, err := d.getTourEvent(&protocol.GetTourEventMessage{
		Cmd:     protocol.CmdGetTourEvent,
		TourID:  run.TourID,
		EventID: event.ID,
	})
	if err != nil {
		t.Fatalf("getTourEvent() error = %v", err)
	}
	if stored.Markdown != event.Markdown {
		t.Fatalf("stored event markdown = %q, want %q", stored.Markdown, event.Markdown)
	}
}

func TestAskTourWakeExplainsQuestionReplyMechanics(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentClaude, protocol.SessionStateIdle)
	inputs := make(chan string, 1)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	event, _, err := d.askTour(&protocol.AskTourMessage{
		Cmd:    protocol.CmdAskTour,
		TourID: run.TourID,
		Body:   "Why is this invariant needed?",
		Context: protocol.TourQuestionContext{
			Path:   "internal/example.go",
			Source: "file",
		},
	})
	if err != nil {
		t.Fatalf("askTour() error = %v", err)
	}

	select {
	case prompt := <-inputs:
		if !strings.Contains(prompt, `tour event --tour "`+run.TourID+`" --event "`+event.ID+`"`) ||
			!strings.Contains(prompt, "Answer it with `attn tour reply`") {
			t.Fatalf("question wake prompt = %q", prompt)
		}
		if !strings.HasSuffix(prompt, "\r") {
			t.Fatalf("question wake input = %q, want atomic prompt with carriage return", prompt)
		}
		if strings.Contains(prompt, "Why is this invariant needed?") {
			t.Fatalf("question wake prompt included question body: %q", prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for question wake prompt")
	}
}

func TestSubmitTourWakesAgentForFinalFeedback(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	inputs := make(chan string, 1)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	event, _, err := d.submitTour(&protocol.SubmitTourMessage{
		Cmd:    protocol.CmdSubmitTour,
		TourID: run.TourID,
		Body:   "Please address this before merging.",
		Finish: true,
	})
	if err != nil {
		t.Fatalf("submitTour() error = %v", err)
	}

	select {
	case prompt := <-inputs:
		if !strings.Contains(prompt, `tour event --tour "`+run.TourID+`" --event "`+event.ID+`"`) ||
			!strings.Contains(prompt, "final review feedback") ||
			!strings.Contains(prompt, "The Tour has ended.") {
			t.Fatalf("final feedback wake prompt = %q", prompt)
		}
		if !strings.HasSuffix(prompt, "\r") {
			t.Fatalf("final feedback wake input = %q, want atomic prompt with carriage return", prompt)
		}
		if strings.Contains(prompt, event.Markdown) {
			t.Fatalf("final feedback wake prompt included feedback body: %q", prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final feedback wake prompt")
	}
}

func TestTourWakeSuppressesRapidFollowUpUntilAgentStarts(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	var mu sync.Mutex
	inputs := make([]string, 0, 2)
	backend.onInput = func(_ string, data []byte) {
		mu.Lock()
		inputs = append(inputs, string(data))
		mu.Unlock()
	}

	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-one", TourID: run.TourID, Kind: "feedback", Markdown: "First",
	})
	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-two", TourID: run.TourID, Kind: "feedback", Markdown: "Second",
	})

	mu.Lock()
	if len(inputs) != 1 || !strings.HasSuffix(inputs[0], "\r") {
		t.Fatalf("PTY inputs = %q, want one atomic wake", inputs)
	}
	mu.Unlock()

	d.markRunStartedIfNeeded(run.SessionID)
	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-after-stale-working", TourID: run.TourID, Kind: "feedback", Markdown: "Still suppressed",
	})

	mu.Lock()
	if len(inputs) != 1 {
		t.Fatalf("PTY inputs = %q, stale working handler cleared the wake latch", inputs)
	}
	mu.Unlock()

	d.updateStateWithPTYLock(run.SessionID, protocol.StateWorking)
	d.markRunStartedIfNeeded(run.SessionID)
	d.updateStateWithPTYLock(run.SessionID, protocol.StateIdle)
	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-three", TourID: run.TourID, Kind: "feedback", Markdown: "Third",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("PTY inputs = %q, want a new wake after the agent started", inputs)
	}
	if !strings.Contains(inputs[1], `"event-three"`) || !strings.HasSuffix(inputs[1], "\r") {
		t.Fatalf("second wake input = %q, want event-three with carriage return", inputs[1])
	}
}

func TestConcurrentTourSubmissionsWakeOldestEvent(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	inputs := make(chan string, 2)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	start := make(chan struct{})
	events := make(chan *protocol.TourEvent, 2)
	errors := make(chan error, 2)
	var submitWG sync.WaitGroup
	for _, body := range []string{"First concurrent feedback", "Second concurrent feedback"} {
		submitWG.Add(1)
		go func(body string) {
			defer submitWG.Done()
			<-start
			event, _, err := d.submitTour(&protocol.SubmitTourMessage{
				Cmd:    protocol.CmdSubmitTour,
				TourID: run.TourID,
				Body:   body,
			})
			if err != nil {
				errors <- err
				return
			}
			events <- event
		}(body)
	}
	close(start)
	submitWG.Wait()
	close(events)
	close(errors)
	for err := range errors {
		t.Fatalf("submitTour() error = %v", err)
	}

	var oldest *protocol.TourEvent
	for event := range events {
		if oldest == nil || event.Seq < oldest.Seq {
			oldest = event
		}
	}
	if oldest == nil {
		t.Fatal("no Tour events returned")
	}

	select {
	case input := <-inputs:
		if !strings.Contains(input, `--event "`+oldest.ID+`"`) {
			t.Fatalf("wake input = %q, want oldest event seq=%d id=%s", input, oldest.Seq, oldest.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for concurrent Tour wake")
	}
	select {
	case input := <-inputs:
		t.Fatalf("unexpected second wake input: %q", input)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTourWakeRetriesAfterInFlightTimeout(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	inputs := make(chan string, 2)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-one", TourID: run.TourID, Kind: "feedback", Markdown: "First",
	})
	select {
	case <-inputs:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial Tour wake")
	}

	d.ptyInputLocksMu.Lock()
	d.tourWakeInFlight[run.SessionID] = tourWakeCommit{
		tourID:    run.TourID,
		startedAt: time.Now().Add(-tourWakeInFlightTimeout),
	}
	d.ptyInputLocksMu.Unlock()
	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-two", TourID: run.TourID, Kind: "feedback", Markdown: "Second",
	})

	select {
	case input := <-inputs:
		if !strings.Contains(input, `"event-two"`) || !strings.HasSuffix(input, "\r") {
			t.Fatalf("retry wake input = %q, want event-two with carriage return", input)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake retry")
	}
}

func TestTourWakeDoesNotSuppressSuccessorTour(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	inputs := make(chan string, 2)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-one", TourID: run.TourID, Kind: "finish", Finish: true, Markdown: "Final",
	})
	successor := *run
	successor.TourID = "tour-successor"
	d.wakeTourAgent(&successor, &protocol.TourEvent{
		ID: "event-two", TourID: successor.TourID, Kind: "question", Markdown: "Question",
	})

	readInput := func(label string) string {
		t.Helper()
		select {
		case input := <-inputs:
			return input
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s wake", label)
			return ""
		}
	}
	first := readInput("predecessor")
	second := readInput("successor")
	if !strings.Contains(first, `"event-one"`) || !strings.Contains(second, `"event-two"`) {
		t.Fatalf("wake inputs = %q, %q, want predecessor then successor events", first, second)
	}
}

func TestTourWakeSerializesWorkingStatePublication(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	inputStarted := make(chan struct{})
	releaseInput := make(chan struct{})
	backend.onInput = func(_ string, _ []byte) {
		close(inputStarted)
		<-releaseInput
	}

	wakeDone := make(chan struct{})
	go func() {
		defer close(wakeDone)
		d.wakeTourAgent(run, &protocol.TourEvent{
			ID: "event-one", TourID: run.TourID, Kind: "feedback", Markdown: "First",
		})
	}()
	select {
	case <-inputStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake input")
	}

	stateDone := make(chan struct{})
	go func() {
		defer close(stateDone)
		d.updateStateWithPTYLock(run.SessionID, protocol.StateWorking)
	}()
	select {
	case <-stateDone:
		t.Fatal("working state published while Tour wake held the PTY input lock")
	case <-time.After(50 * time.Millisecond):
	}
	if session := d.store.Get(run.SessionID); session == nil || session.State != protocol.SessionStateIdle {
		t.Fatalf("session state during wake = %+v, want idle", session)
	}

	close(releaseInput)
	select {
	case <-wakeDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake completion")
	}
	select {
	case <-stateDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for working state publication")
	}
	if session := d.store.Get(run.SessionID); session == nil || session.State != protocol.SessionStateWorking {
		t.Fatalf("session state after wake = %+v, want working", session)
	}
}

func TestTourWakeIgnoresStateObservedBeforeCommit(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	backend.onSnapshot = func() {
		close(snapshotStarted)
		<-releaseSnapshot
	}
	inputs := make(chan string, 2)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	wakeDone := make(chan struct{})
	go func() {
		defer close(wakeDone)
		d.wakeTourAgent(run, &protocol.TourEvent{
			ID: "event-one", TourID: run.TourID, Kind: "feedback", Markdown: "First",
		})
	}()
	select {
	case <-snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake snapshot")
	}

	stateDone := make(chan struct{})
	go func() {
		defer close(stateDone)
		d.updateStateWithPTYLock(run.SessionID, protocol.StateWorking)
	}()
	time.Sleep(20 * time.Millisecond)
	close(releaseSnapshot)

	select {
	case <-wakeDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake")
	}
	select {
	case <-stateDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale state publication")
	}
	select {
	case <-inputs:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial Tour wake input")
	}

	d.updateStateWithPTYLock(run.SessionID, protocol.StateIdle)
	d.wakeTourAgent(run, &protocol.TourEvent{
		ID: "event-two", TourID: run.TourID, Kind: "feedback", Markdown: "Second",
	})
	select {
	case input := <-inputs:
		t.Fatalf("stale state observation cleared wake latch: %q", input)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTourWakeDoesNotSubmitIntoNonEmptyEditor(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	snapshot, ok := pty.ScreenSnapshotFromReplay([]byte("All done.\r\n› drafted response"), 80, 24)
	if !ok {
		t.Fatal("build non-empty editor snapshot")
	}
	backend.snapshot = ptybackend.AttachInfo{
		ScreenSnapshot:      snapshot.Payload,
		ScreenCols:          snapshot.Cols,
		ScreenRows:          snapshot.Rows,
		ScreenCursorX:       snapshot.CursorX,
		ScreenCursorY:       snapshot.CursorY,
		ScreenCursorVisible: snapshot.CursorVisible,
		ScreenSnapshotFresh: true,
	}
	inputs := make(chan string, 1)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	if _, _, err := d.submitTour(&protocol.SubmitTourMessage{
		Cmd:    protocol.CmdSubmitTour,
		TourID: run.TourID,
		Body:   "Review feedback.",
	}); err != nil {
		t.Fatalf("submitTour() error = %v", err)
	}
	select {
	case input := <-inputs:
		t.Fatalf("unexpected PTY input: %q", input)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTourWakeDoesNotRaceRecentUserInput(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	var mu sync.Mutex
	inputs := make([]string, 0, 2)
	backend.onInput = func(_ string, data []byte) {
		mu.Lock()
		inputs = append(inputs, string(data))
		mu.Unlock()
	}

	d.handlePtyInput(nil, &protocol.PtyInputMessage{
		Cmd:    protocol.CmdPtyInput,
		ID:     run.SessionID,
		Data:   "x",
		Source: protocol.Ptr("user"),
	})
	d.wakeTourAgent(run, &protocol.TourEvent{
		ID:       "event-after-input",
		TourID:   run.TourID,
		Kind:     "feedback",
		Markdown: "Review feedback.",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 1 || inputs[0] != "x" {
		t.Fatalf("PTY inputs = %q, want only the user's input", inputs)
	}
}

func TestTourWakeYieldsToInputIntentBeforeCommit(t *testing.T) {
	d, backend, run := setupTourWakeTest(t, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	backend.onSnapshot = func() {
		close(snapshotStarted)
		<-releaseSnapshot
	}
	inputs := make(chan string, 2)
	backend.onInput = func(_ string, data []byte) {
		inputs <- string(data)
	}

	wakeDone := make(chan struct{})
	go func() {
		defer close(wakeDone)
		d.wakeTourAgent(run, &protocol.TourEvent{
			ID: "event-before-input", TourID: run.TourID, Kind: "feedback", Markdown: "Review feedback.",
		})
	}()
	select {
	case <-snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake snapshot")
	}

	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		d.handlePtyInput(nil, &protocol.PtyInputMessage{
			Cmd:    protocol.CmdPtyInput,
			ID:     run.SessionID,
			Data:   "x",
			Source: protocol.Ptr("user"),
		})
	}()
	deadline := time.Now().Add(time.Second)
	for d.lastPTYInputTime(run.SessionID).IsZero() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d.lastPTYInputTime(run.SessionID).IsZero() {
		t.Fatal("user input intent was not recorded before acquiring the PTY lock")
	}
	close(releaseSnapshot)

	select {
	case <-wakeDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Tour wake")
	}
	select {
	case <-inputDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user input")
	}
	select {
	case input := <-inputs:
		if input != "x" {
			t.Fatalf("PTY input = %q, want only user input", input)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user PTY input")
	}
	select {
	case input := <-inputs:
		t.Fatalf("unexpected Tour wake input after user intent: %q", input)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTourWakeDoesNotInterruptWorkingOrShellSessions(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		state protocol.SessionState
	}{
		{name: "working agent", agent: protocol.SessionAgentCodex, state: protocol.SessionStateWorking},
		{name: "shell session", agent: protocol.SessionAgentShell, state: protocol.SessionStateIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, backend, run := setupTourWakeTest(t, tt.agent, tt.state)
			inputs := make(chan string, 1)
			backend.onInput = func(_ string, data []byte) {
				inputs <- string(data)
			}
			if _, _, err := d.submitTour(&protocol.SubmitTourMessage{
				Cmd:    protocol.CmdSubmitTour,
				TourID: run.TourID,
				Body:   "Review feedback.",
			}); err != nil {
				t.Fatalf("submitTour() error = %v", err)
			}
			select {
			case input := <-inputs:
				t.Fatalf("unexpected PTY input: %q", input)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}
