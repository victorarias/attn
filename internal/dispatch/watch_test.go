package dispatch

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// scriptedFetch replays a fixed sequence of snapshots, one per poll. Each entry
// is (dispatch, found, err). After the script is exhausted it keeps returning
// the last entry — but a correct watch must terminate before then.
func scriptedFetch(steps []fetchStep) (Fetcher, *int) {
	calls := 0
	return func() (*protocol.ChiefOfStaffDispatch, bool, error) {
		i := calls
		if i >= len(steps) {
			i = len(steps) - 1
		}
		calls++
		s := steps[i]
		return s.dispatch, s.found, s.err
	}, &calls
}

type fetchStep struct {
	dispatch *protocol.ChiefOfStaffDispatch
	found    bool
	err      error
}

func working() *protocol.ChiefOfStaffDispatch {
	return &protocol.ChiefOfStaffDispatch{ID: "d1", Label: "badge", Status: "working"}
}

func completed() *protocol.ChiefOfStaffDispatch {
	return &protocol.ChiefOfStaffDispatch{
		ID: "d1", Label: "badge", Status: "idle",
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateCompleted,
			Summary:   "done and green",
		},
	}
}

func runWatchScript(t *testing.T, steps []fetchStep) (code int, out string, calls int) {
	t.Helper()
	fetch, callsPtr := scriptedFetch(steps)
	var buf bytes.Buffer
	code = RunWatch(fetch, &buf, WatchOptions{
		DispatchID: "d1",
		Sleep:      func(_ time.Duration) {},
	})
	return code, buf.String(), *callsPtr
}

func TestRunWatch_ExitsOnTerminalCompletion(t *testing.T) {
	code, out, _ := runWatchScript(t, []fetchStep{
		{working(), true, nil},
		{working(), true, nil},
		{completed(), true, nil},
	})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		t.Fatalf("want exactly one emitted line, got %d: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "[done]") || !strings.Contains(lines[0], "done and green") {
		t.Errorf("line = %q, want a [done] line carrying the summary", lines[0])
	}
}

func TestRunWatch_StaysSilentThenExitsOnRoutineApproval(t *testing.T) {
	// pending_approval is the exclusion: a watch must emit nothing for it, then
	// exit cleanly once the dispatch actually completes.
	approval := &protocol.ChiefOfStaffDispatch{ID: "d1", Label: "badge", Status: string(protocol.SessionStatePendingApproval)}
	code, out, _ := runWatchScript(t, []fetchStep{
		{approval, true, nil},
		{approval, true, nil},
		{completed(), true, nil},
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "[done]") {
		t.Fatalf("approval must be silent; only the completion emits. got: %q", out)
	}
}

func TestRunWatch_FailsAndExitsNonZeroOnSessionClose(t *testing.T) {
	closed := &protocol.ChiefOfStaffDispatch{ID: "d1", Label: "badge", Status: SessionClosedStatus}
	code, out, _ := runWatchScript(t, []fetchStep{
		{working(), true, nil},
		{closed, true, nil},
	})
	if code == 0 {
		t.Errorf("exit = 0, want non-zero for a session that died without completing")
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "[failed]") {
		t.Fatalf("want a single [failed] line, got: %q", out)
	}
}

func TestRunWatch_BlockerEmitsButDoesNotExitThenCompletes(t *testing.T) {
	blocker := &protocol.ChiefOfStaffDispatch{
		ID: "d1", Label: "badge", Status: "waiting_input",
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateNeedsInput,
			Summary:   "need a decision",
			Request: &protocol.DispatchDecisionRequest{
				Status:            protocol.DispatchRequestStatusPending,
				Question:          "flag or not?",
				ExpectedResponder: "chief",
			},
		},
	}
	code, out, _ := runWatchScript(t, []fetchStep{
		{blocker, true, nil}, // emits blocker, keeps watching
		{blocker, true, nil}, // same blocker: deduped, silent
		{working(), true, nil},
		{completed(), true, nil}, // terminal
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines (blocker once, then done), got %d: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "[blocker]") || !strings.Contains(lines[0], "flag or not?") {
		t.Errorf("first line = %q, want a [blocker] carrying the question", lines[0])
	}
	if !strings.HasPrefix(lines[1], "[done]") {
		t.Errorf("second line = %q, want [done]", lines[1])
	}
}

func TestRunWatch_AlreadyTerminalOnFirstPollEmitsAndExits(t *testing.T) {
	code, out, calls := runWatchScript(t, []fetchStep{
		{completed(), true, nil},
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if calls != 1 {
		t.Errorf("polls = %d, want 1 (immediate terminal)", calls)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[done]") {
		t.Errorf("out = %q, want immediate [done]", out)
	}
}

func TestRunWatch_GoneAfterSeenIsTerminalFailure(t *testing.T) {
	code, out, _ := runWatchScript(t, []fetchStep{
		{working(), true, nil},
		{nil, false, nil}, // record vanished
	})
	if code == 0 {
		t.Errorf("exit = 0, want non-zero when a seen dispatch vanishes")
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[failed]") {
		t.Errorf("out = %q, want a [failed] line", out)
	}
}

func TestRunWatch_AbortsAfterRepeatedFetchErrors(t *testing.T) {
	boom := errors.New("daemon unreachable")
	fetch := func() (*protocol.ChiefOfStaffDispatch, bool, error) { return nil, false, boom }
	var buf bytes.Buffer
	code := RunWatch(fetch, &buf, WatchOptions{
		DispatchID: "d1",
		MaxErrors:  3,
		Sleep:      func(_ time.Duration) {},
	})
	if code == 0 {
		t.Errorf("exit = 0, want non-zero after repeated errors")
	}
	if !strings.Contains(buf.String(), "aborted") {
		t.Errorf("out = %q, want an abort line", buf.String())
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
