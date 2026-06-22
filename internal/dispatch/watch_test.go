package dispatch

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestRunWatch_SessionCloseIsNeutralEndedExitZero(t *testing.T) {
	// Silence is not failure: a session that closes without a structured report
	// is the neutral [ended], exit 0 — it emits and exits (never hangs), but
	// asserts no outcome.
	closed := &protocol.ChiefOfStaffDispatch{ID: "d1", Label: "badge", Status: SessionClosedStatus}
	code, out, _ := runWatchScript(t, []fetchStep{
		{working(), true, nil},
		{closed, true, nil},
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0 — close-without-report is neutral, not failure", code)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "[ended]") {
		t.Fatalf("want a single [ended] line, got: %q", out)
	}
}

func TestRunWatch_ExplicitFailureExitsNonZero(t *testing.T) {
	// Only an explicitly declared failure is [failed], exit 1.
	failed := &protocol.ChiefOfStaffDispatch{
		ID: "d1", Label: "badge", Status: "working",
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateFailed,
			Summary:   "build is red",
		},
	}
	code, out, _ := runWatchScript(t, []fetchStep{
		{working(), true, nil},
		{failed, true, nil},
	})
	if code == 0 {
		t.Errorf("exit = 0, want non-zero for an explicitly reported failure")
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "[failed]") {
		t.Fatalf("want a single [failed] line, got: %q", out)
	}
}

func TestRunWatch_DeadSessionWithStaleBlockerDoesNotHang(t *testing.T) {
	// A needs_input report on a session that then closes must resolve to a
	// neutral terminal — not stay a (non-terminal) blocker and hang forever.
	stuck := &protocol.ChiefOfStaffDispatch{
		ID: "d1", Label: "badge", Status: SessionClosedStatus,
		StructuredReport: &protocol.DispatchReport{
			WorkState: protocol.DispatchWorkStateNeedsInput,
			Summary:   "was waiting on a decision",
		},
	}
	code, out, _ := runWatchScript(t, []fetchStep{
		{stuck, true, nil},
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[ended]") {
		t.Fatalf("dead session with a stale blocker must end neutrally, got: %q", out)
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

func TestRunWatch_GoneAfterSeenIsNeutralEnded(t *testing.T) {
	// A vanished record is absence, not failure → neutral [ended], exit 0, and
	// crucially it still terminates rather than hanging.
	code, out, _ := runWatchScript(t, []fetchStep{
		{working(), true, nil},
		{nil, false, nil}, // record vanished
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0 — a vanished record is neutral, not failure", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[ended]") {
		t.Errorf("out = %q, want an [ended] line", out)
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

func TestOneLineCollapsesAndTruncatesOnRuneBoundary(t *testing.T) {
	if got := oneLine("a\n  b\tc"); got != "a b c" {
		t.Errorf("oneLine collapse = %q, want %q", got, "a b c")
	}
	// A long run of multi-byte runes must truncate without producing invalid
	// UTF-8 (byte slicing would split a rune).
	long := strings.Repeat("é", 400)
	got := oneLine(long)
	if !utf8.ValidString(got) {
		t.Errorf("truncated output is not valid UTF-8: %q", got)
	}
	if r := []rune(got); len(r) != summaryLineLimit {
		t.Errorf("truncated rune length = %d, want %d", len(r), summaryLineLimit)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated output should end with ellipsis, got %q", got[len(got)-4:])
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
