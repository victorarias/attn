package main

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// `attn app runtime` is about the shared process, and the commands that could be
// mistaken for per-app ones have to say why they are not.

func TestRuntimeRestartRefusesAnAppNameAndExplainsWhy(t *testing.T) {
	err := appRuntimeRestartTakesNoName("greeter")
	if err == nil {
		t.Fatal("naming an app was accepted")
	}
	msg := err.Error()
	// Three things a reader needs: that the name is the problem, why there is
	// nothing per-app to restart, and what to run instead.
	for _, want := range []string{`"greeter"`, "one shared runtime", "attn app disable greeter"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal does not contain %q: %s", want, msg)
		}
	}
}

// A runtime the daemon has never started is rendered as that, not as "stopped":
// the two send a reader to different places.
func TestRuntimeCellDistinguishesNeverStartedFromParked(t *testing.T) {
	got := appRuntimeCell(nil)
	if !strings.Contains(got, "not started") {
		t.Fatalf("a runtime that has never run renders as %q", got)
	}
	// It must not claim a cause. A daemon whose apps are quiet and a daemon whose
	// runtime binary is missing both arrive here with no snapshot — the host is
	// resolved before the supervisor is touched — so naming the first would be a
	// lie exactly when every dispatch is failing. It points at the surface that
	// can tell them apart instead.
	if !strings.Contains(got, "attn app runtime status") {
		t.Fatalf("the never-started sentence does not point at what can answer: %q", got)
	}
	if strings.Contains(got, "due a fact") {
		t.Fatalf("the never-started sentence still claims a cause it cannot know: %q", got)
	}
	parked := appRuntimeCell(&protocol.AppRuntimeInfo{Phase: "parked", Generation: 4})
	if !strings.Contains(parked, "PARKED") || !strings.Contains(parked, "attn app runtime restart") {
		t.Fatalf("a parked runtime renders as %q, which does not name the way back", parked)
	}
	running := appRuntimeCell(&protocol.AppRuntimeInfo{Phase: "connected", Connected: true, Generation: 2})
	if !strings.Contains(running, "running") {
		t.Fatalf("a connected runtime renders as %q", running)
	}
	starting := appRuntimeCell(&protocol.AppRuntimeInfo{Phase: "starting", Generation: 1})
	if !strings.Contains(starting, "not connected") {
		t.Fatalf("a runtime that has not dialed back renders as %q", starting)
	}
}

// A handler's error carries the JavaScript stack that threw it. Both places it
// is rendered have to survive that: the aligned table takes one line, the stall
// block takes all of them inside its column.
func TestAMultiLineHandlerErrorSurvivesBothRenderings(t *testing.T) {
	stack := "Error: the ticket store is unreachable\n    at onTicket (bundle.js:5:18)\n    at M (native:6:1)"

	row := firstErrorLine(stack)
	if strings.Contains(row, "\n") {
		t.Fatalf("the table cell is multi-line: %q", row)
	}
	if !strings.HasSuffix(row, "…") {
		t.Fatalf("the table cell %q does not say it was cut", row)
	}

	block := indentBlock("  > ", stack)
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		if !strings.HasPrefix(line, "  > ") {
			t.Fatalf("a continuation line escaped its column: %q", line)
		}
	}
	if got := firstErrorLine("just the one line"); got != "just the one line" {
		t.Fatalf("a single-line error was changed to %q", got)
	}
}

func TestInvocationRenderingNamesReconcileWorkWithoutInventingAnEvent(t *testing.T) {
	reason := protocol.AppReconcileReasonInfo{
		Causes: []string{"gap", "version_changed"}, Version: 7, ThroughSeq: 42,
		PreviousVersions: []int{3, 5},
	}
	inv := protocol.AppInvocationInfo{
		ID: 9, VersionID: 7, Kind: "reconcile", Handler: "reconcile",
		Status: "running", StartedAt: "2026-08-17T10:00:00Z", Reconcile: &reason,
		ThroughRequestID: protocol.Ptr(12),
	}
	if got := appInvocationWork(inv); got != "through seq 42 (gap, version_changed)" {
		t.Fatalf("reconcile work = %q", got)
	}
	line := devInvocationLine(inv)
	for _, want := range []string{"running", "reconcile", "through seq 42", "gap, version_changed"} {
		if !strings.Contains(line, want) {
			t.Fatalf("dev line %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, " in ") {
		t.Fatalf("running invocation invented a duration: %q", line)
	}
}

func TestInvocationRenderingKeepsSubscriptionIdentity(t *testing.T) {
	inv := protocol.AppInvocationInfo{
		ID: 2, VersionID: 1, Kind: "subscription", Handler: "subscribe:ticket.*",
		Status: "error", StartedAt: "2026-08-17T10:00:00Z",
		EventSeq: protocol.Ptr(81), EventName: protocol.Ptr("ticket.updated"),
		EventSubject: protocol.Ptr("tk-7"), DurationMs: protocol.Ptr(4),
	}
	if got := appInvocationWork(inv); got != "seq 81 ticket.updated tk-7" {
		t.Fatalf("subscription work = %q", got)
	}
	if got := devInvocationLine(inv); !strings.Contains(got, "in 4ms") || !strings.Contains(got, "ticket.updated tk-7") {
		t.Fatalf("dev line = %q", got)
	}
}

func TestReconcileStatusRenderingNamesUnsupportedAndOwedStates(t *testing.T) {
	unsupported := appReconcileStatusCell(protocol.AppReconcileStatus{State: "unsupported"})
	if !strings.Contains(unsupported, "unsupported") || !strings.Contains(unsupported, "declares and implements reconcile") {
		t.Fatalf("unsupported status = %q", unsupported)
	}
	reason := protocol.AppReconcileReasonInfo{
		Causes: []string{"version_changed"}, Version: 4, ThroughSeq: 18,
		PreviousVersions: []int{3},
	}
	owed := appReconcileStatusCell(protocol.AppReconcileStatus{State: "owed", Reason: &reason})
	if owed != "owed through seq 18 (version_changed)" {
		t.Fatalf("owed status = %q", owed)
	}
}
