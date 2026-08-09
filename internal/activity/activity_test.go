package activity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/transcript"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The window is a delta: a cursor that has already consumed the transcript
// yields nothing, which is what lets an idle session cost zero.
func TestReadIsADelta(t *testing.T) {
	path := writeTranscript(t,
		`{"timestamp":"2026-08-07T10:00:00Z","type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}`,
	)
	first, err := Read(path, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 {
		t.Fatalf("first read events = %d, want 1", len(first.Events))
	}
	second, err := Read(path, "claude", first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Empty() {
		t.Fatalf("second read returned %d events, want an empty delta", len(second.Events))
	}
}

// A delta larger than one page is read to its end, and what comes back is its
// NEWEST events. Returning the first page instead would answer "what is this
// agent doing right now" with the oldest thing it did while nobody was looking —
// and would leave the cursor mid-backlog, so the next window would be stale too.
func TestReadKeepsTheNewestEventsOfALongDelta(t *testing.T) {
	lines := make([]string, 0, MaxEvents*3)
	for i := 0; i < MaxEvents*3; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"timestamp":"2026-08-07T10:00:00Z","type":"assistant","message":{"content":[{"type":"text","text":"event-%d"}]}}`, i))
	}
	path := writeTranscript(t, lines...)

	window, err := Read(path, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != MaxEvents {
		t.Fatalf("events = %d, want %d", len(window.Events), MaxEvents)
	}
	newest := fmt.Sprintf("event-%d", MaxEvents*3-1)
	if got := window.Events[len(window.Events)-1].Text; got != newest {
		t.Errorf("last event = %q, want %q — the read kept the oldest page", got, newest)
	}
	oldest := fmt.Sprintf("event-%d", MaxEvents*3-MaxEvents)
	if got := window.Events[0].Text; got != oldest {
		t.Errorf("first event = %q, want %q", got, oldest)
	}

	// The report counts against the whole delta, not against one page: a caller
	// told "dropped 1 of 201" about a backlog of 600 would believe it had the story.
	if window.Report.TotalEvents != MaxEvents*3 {
		t.Errorf("TotalEvents = %d, want %d", window.Report.TotalEvents, MaxEvents*3)
	}
	if want := MaxEvents * 2; window.Report.DroppedOld != want {
		t.Errorf("DroppedOld = %d, want %d", window.Report.DroppedOld, want)
	}

	// And the cursor is past the end, so the next read is a genuine delta.
	next, err := Read(path, "claude", window.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Empty() {
		t.Fatalf("follow-up read returned %d events; the cursor stopped short of the end", len(next.Events))
	}
}

// Thinking is why this package exists: it is the densest statement of intent and
// it must survive into the rendered window with its own label, distinct from
// what the user was actually shown.
func TestRenderLabelsThinkingSeparatelyFromProse(t *testing.T) {
	window := Window{Events: []transcript.Event{
		{Kind: transcript.EventKindThinking, Text: "I should fix the migration first"},
		{Kind: transcript.EventKindAssistant, Text: "Fixing the migration"},
		{Kind: transcript.EventKindToolCall, ToolName: "Edit", Text: `{"file":"m.sql"}`},
		{Kind: transcript.EventKindToolResult, Text: "ok"},
		{Kind: transcript.EventKindToolResult, Text: "boom", IsError: true},
	}}
	got := window.Render()
	for _, want := range []string{
		"thinking: I should fix the migration first",
		"assistant: Fixing the migration",
		`tool_call Edit: {"file":"m.sql"}`,
		"tool_result: ok",
		"tool_result ERROR: boom",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q\n---\n%s", want, got)
		}
	}
}

// A limit someone can hit is a limit they must see: the event cap keeps the
// newest events and says how many it dropped, rather than presenting a silently
// short window as the whole story.
func TestCapKeepsNewestAndReportsTheDrop(t *testing.T) {
	events := make([]transcript.Event, MaxEvents+5)
	for i := range events {
		events[i] = transcript.Event{Kind: transcript.EventKindToolCall, ToolName: "Bash", Text: string(rune('a' + i%26))}
	}
	events[len(events)-1].Text = "LAST"
	window := Window{Events: events}
	window.Report.TotalEvents = len(events)
	window.cap()

	if len(window.Events) != MaxEvents {
		t.Fatalf("events = %d, want %d", len(window.Events), MaxEvents)
	}
	if window.Events[len(window.Events)-1].Text != "LAST" {
		t.Error("cap dropped the newest event; it must drop the oldest")
	}
	if !window.Report.Truncated() {
		t.Fatal("report does not say the window was truncated")
	}
	note := window.Report.String()
	if !strings.Contains(note, "5") || !strings.Contains(note, "max_events=200") {
		t.Errorf("report must name the limit, its value, and the ask; got %q", note)
	}
	if !strings.Contains(window.Render(), "window truncated") {
		t.Error("rendered window must carry the truncation note")
	}
}

// Clip budgets are per kind. Thinking gets the largest because it is where
// intent lives, but it is still bounded — it is the longest content type.
func TestClipIsPerKind(t *testing.T) {
	long := strings.Repeat("x", 5000)
	for _, tc := range []struct {
		kind  string
		limit int
	}{
		{transcript.EventKindThinking, ClipThinking},
		{transcript.EventKindAssistant, ClipAssistant},
		{transcript.EventKindToolResult, ClipToolResult},
	} {
		got := clip(transcript.Event{Kind: tc.kind, Text: long})
		if len([]rune(got)) > tc.limit+1 { // +1 for the ellipsis
			t.Errorf("%s clipped to %d, want <= %d", tc.kind, len(got), tc.limit)
		}
	}
}

// The prompt must keep state, window, and the previous line apart, and must
// degrade readably when the optional ones are absent — the first line for a
// session has no previous, and a breakthrough can have an empty window.
func TestTemplateRenderSubstitutesAndDegrades(t *testing.T) {
	template := Template{Name: "t", Body: "S={{STATE}} R={{STATE_REASON}} P={{PREVIOUS}} W={{WINDOW}}"}
	got := template.Render(Input{State: "working"})
	for _, want := range []string{"S=working", "R=unspecified", "none —", "nothing new"} {
		if !strings.Contains(got.User, want) {
			t.Errorf("render missing %q; got %q", want, got.User)
		}
	}
	got = template.Render(Input{State: "idle", StateReason: "stop_hook", Previous: "was testing", Window: "assistant: done"})
	for _, want := range []string{"S=idle", "R=stop_hook", "P=was testing", "W=assistant: done"} {
		if !strings.Contains(got.User, want) {
			t.Errorf("render missing %q; got %q", want, got.User)
		}
	}
}

// The marker is what lets the invariant half ride --system-prompt and replace the
// CLI's own, which is where nearly all the per-run cost lived. A template without
// it must still render — as an all-user prompt, paying the full prefix.
func TestTemplateRenderSplitsOnTheSystemMarker(t *testing.T) {
	template := Template{Name: "t", Body: "the rules\n" + SystemMarker + "\nstate: {{STATE}}"}
	got := template.Render(Input{State: "working"})
	if got.System != "the rules" {
		t.Errorf("system = %q, want %q", got.System, "the rules")
	}
	if got.User != "state: working" {
		t.Errorf("user = %q, want %q", got.User, "state: working")
	}
	if got.Chars() != len("the rules")+len("state: working") {
		t.Errorf("Chars() = %d, want the sum of both parts", got.Chars())
	}

	unmarked := Template{Name: "t", Body: "state: {{STATE}}"}.Render(Input{State: "idle"})
	if unmarked.System != "" || unmarked.User != "state: idle" {
		t.Errorf("an unmarked template must be all user prompt; got %+v", unmarked)
	}

	// The shipped baseline must actually carry the marker — losing it is a silent
	// 10x cost regression, not a test failure anywhere else.
	baseline, err := LoadTemplate("baseline", filepath.Join("prompts", "baseline.md"))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	rendered := baseline.Render(Input{State: "working", Window: "assistant: hi"})
	if rendered.System == "" {
		t.Fatal("internal/activity/prompts/baseline.md lost its {{USER}} marker: the whole prompt would ship as the user turn and pay the CLI's full system prefix")
	}
	if !strings.Contains(rendered.User, "assistant: hi") {
		t.Errorf("baseline must put the window in the user turn; got %q", rendered.User)
	}
}

// The load-bearing check. A spike produced a fluent line describing active work
// for a pending_approval session because the state was withheld from the prompt.
// Anything that regresses that must fail here.
func TestCheckCatchesALineThatContradictsABlockedState(t *testing.T) {
	violations := Check("Running the frontend test suite in attn--brisk-toucan", "pending_approval")
	if !hasCheck(violations, "state_consistency") {
		t.Fatalf("a line narrating active work for a blocked session must fail state_consistency; got %v", violations)
	}
	if v := Check("Awaiting approval to delete migrations/0042.sql", "pending_approval"); len(v) != 0 {
		t.Errorf("a line that acknowledges the block must pass; got %v", v)
	}
	// working is not a blocked state, so the same line is fine there.
	if v := Check("Running the frontend test suite in attn--brisk-toucan", "working"); len(v) != 0 {
		t.Errorf("active narration is correct for working; got %v", v)
	}
	// A false positive here is worse than a miss: it makes a good line look like a
	// model failure and sends prompt tuning after a phantom. These are real lines
	// the harness generated and the check wrongly rejected.
	for _, line := range []string{
		"Completed activity-bench harness; cost error requires design revision",
		"Halted on a failing migration in internal/store",
		"Stuck on an unresolved import in app/src/App.tsx",
	} {
		if v := Check(line, "idle"); len(v) != 0 {
			t.Errorf("line %q acknowledges the session stopped; got %v", line, v)
		}
	}
}

func TestCheckCatchesFormatFailures(t *testing.T) {
	for _, tc := range []struct {
		name, line, state, want string
	}{
		{"empty", "  ", "working", "nonempty"},
		{"too long", strings.Repeat("x", MaxLineRunes+1), "working", "length"},
		{"trailing period", "Fixing the migration.", "working", "no_trailing_period"},
		{"quoted", `"Fixing the migration"`, "working", "no_quotes"},
		{"preamble", "The agent is fixing the migration", "working", "no_preamble"},
		{"newline", "Fixing\nthe migration", "working", "single_line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !hasCheck(Check(tc.line, tc.state), tc.want) {
				t.Errorf("Check(%q) did not report %s", tc.line, tc.want)
			}
		})
	}
	// A colon mid-line is normal phrasing, not a preamble.
	if hasCheck(Check("Fixing auth: token refresh loops", "working"), "no_preamble") {
		t.Error("a mid-line colon must not read as a preamble")
	}
}

func hasCheck(violations []Violation, name string) bool {
	for _, violation := range violations {
		if violation.Check == name {
			return true
		}
	}
	return false
}
