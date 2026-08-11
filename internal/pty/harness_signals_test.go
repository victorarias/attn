package pty

import (
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// title builds an OSC 0 window-title sequence, the form both agents repaint
// their spinner into.
func title(text string) []byte {
	return []byte("\x1b]0;" + text + "\x07")
}

func observeAt(o *harnessSignalObserver, at time.Time, chunks ...[]byte) []Observation {
	var out []Observation
	for _, chunk := range chunks {
		out = append(out, o.Observe(chunk, at)...)
	}
	return out
}

func onlySignal(t *testing.T, got []Observation) Observation {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 observation, got %d: %+v", len(got), got)
	}
	return got[0]
}

func TestNoObserverForAnAgentWithoutHarnessSignals(t *testing.T) {
	if got := newHarnessSignalObserver(agentdriver.HarnessSignalsNone); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
	// A nil observer must be safe to call so the read loop needs no extra guard.
	var nilObserver *harnessSignalObserver
	if got := nilObserver.Observe(title("⠐ x"), time.Now()); got != nil {
		t.Fatalf("nil observer returned %+v", got)
	}
}

func TestClaudeTitleHeartbeat(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		title   string
		claim   string
		summary string
	}{
		// Claude cycles the running glyph and has changed which glyphs it cycles
		// through: braille up to 2.1.227, half circles from 2.1.228. Both read as
		// busy because busy is "a status symbol that is not the resting one",
		// which is what survives the next change.
		{name: "braille spinner is busy", title: "⠐ Run background sleep command", claim: claimBusy, summary: "Run background sleep command"},
		{name: "another braille frame", title: "⠸ Editing files", claim: claimBusy, summary: "Editing files"},
		{name: "2.1.228 half circle is busy", title: "◐ Run background sleep command", claim: claimBusy, summary: "Run background sleep command"},
		{name: "the other half circle frame", title: "◑ Editing files", claim: claimBusy, summary: "Editing files"},
		{name: "asterisk is not busy", title: "✳ Run background sleep command", claim: claimNotBusy, summary: "Run background sleep command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
			got := onlySignal(t, observeAt(o, now, title(tc.title)))
			if got.Source != SourceHeartbeat || got.Claim != tc.claim {
				t.Fatalf("got %+v, want %s/%s", got, SourceHeartbeat, tc.claim)
			}
			// The title doubles as a live turn summary — a free sidebar label —
			// minus the level glyph, which says nothing the claim does not.
			if got.Detail != tc.summary {
				t.Fatalf("detail %q, want %q", got.Detail, tc.summary)
			}
			if !got.At.Equal(now) {
				t.Fatalf("At %s, want %s", got.At, now)
			}
		})
	}
}

// A title claude did not write says nothing about claude, and claiming a level
// for it would let any subprocess that sets a title settle the session. It is
// still reported: someone painted it, which is all the stuck tripwire asks.
func TestClaudeReportsAForeignTitleAsLivenessOnly(t *testing.T) {
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
	now := time.Now()
	// A second past the keepalive: unchanged levels collapse, and unclassified
	// is a level like any other.
	for i, foreign := range []string{"victor@mac: ~", "htop"} {
		got := onlySignal(t, observeAt(o, now.Add(time.Duration(i)*2*time.Second), title(foreign)))
		if got.Claim != claimUnclassified {
			t.Fatalf("title %q claimed %q, want %q", foreign, got.Claim, claimUnclassified)
		}
	}
}

// An empty title is not a paint: there is nothing to have witnessed.
func TestClaudeIgnoresAnEmptyTitle(t *testing.T) {
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
	if got := observeAt(o, time.Now(), title("")); got != nil {
		t.Fatalf("empty title produced %+v", got)
	}
}

// Codex has no distinct idle glyph: busy is a spinner frame, anything else is
// the bare working directory. A hijacked title therefore reads as not-busy,
// which is safe under a freshness rule keyed on when busy frames last arrived.
func TestCodexTitleHeartbeat(t *testing.T) {
	now := time.Now()
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsCodex)
	got := onlySignal(t, observeAt(o, now, title("⠸ attn--fix-state-detec...")))
	if got.Claim != claimBusy {
		t.Fatalf("got %+v, want busy", got)
	}
	if got.Detail != "attn--fix-state-detec..." {
		t.Fatalf("detail %q, want the spinner stripped", got.Detail)
	}
	if got := onlySignal(t, observeAt(o, now, title("attn--fix-state-detec..."))); got.Claim != claimNotBusy {
		t.Fatalf("got %+v, want not_busy", got)
	}
}

// The level restates itself continuously — codex repaints about ten times a
// second. Emitting every frame would drown out every other kind of evidence.
func TestHeartbeatRateLimitsAnUnchangedLevel(t *testing.T) {
	start := time.Now()
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)

	if got := observeAt(o, start, title("⠐ working")); len(got) != 1 {
		t.Fatalf("first frame produced %d observations", len(got))
	}
	for i := range 20 {
		at := start.Add(time.Duration(i+1) * 10 * time.Millisecond)
		if got := observeAt(o, at, title("⠸ working")); got != nil {
			t.Fatalf("repeat frame at +%dms produced %+v", (i+1)*10, got)
		}
	}
	// Past the keepalive, "still busy" is news again: it is what distinguishes a
	// running agent from one that stopped emitting.
	at := start.Add(heartbeatKeepalive + time.Millisecond)
	if got := observeAt(o, at, title("⠿ working")); len(got) != 1 {
		t.Fatalf("keepalive frame produced %d observations", len(got))
	}
}

// A change is never rate limited: busy -> not busy is the edge that matters.
func TestHeartbeatAlwaysReportsAChange(t *testing.T) {
	start := time.Now()
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
	observeAt(o, start, title("⠐ working"))

	got := onlySignal(t, observeAt(o, start.Add(time.Millisecond), title("✳ done")))
	if got.Claim != claimNotBusy {
		t.Fatalf("got %+v, want not_busy immediately", got)
	}
}

// The heartbeat speaks its own vocabulary rather than protocol state names, so
// nothing may cache, dedup, or apply its claim as a state. Everything else names
// a state — and, since the screen scrapers were deleted, everything else is also
// entitled to apply one, because all that is left is the worker poll ending
// `launching`.
func TestOnlyTheHeartbeatSpeaksItsOwnVocabulary(t *testing.T) {
	if SourceHeartbeat.ClaimsProtocolState() {
		t.Fatalf("%s claims a level, not a state", SourceHeartbeat)
	}
	for _, source := range []Source{SourceWorkerInfo, SourceUnknown} {
		if !source.ClaimsProtocolState() {
			t.Fatalf("%s claims a protocol state", source)
		}
	}
}

// The read loop hands over PTY chunks as they arrive, so a title routinely
// straddles one. Splitting the whole exchange at every byte proves the level
// survives without duplicating or losing a frame.
func TestHeartbeatSurvivesEverySplitPoint(t *testing.T) {
	const stream = "\x1b]0;⠐ working\x07output\x1b]0;✳ done\x07"
	start := time.Now()
	for split := range len(stream) + 1 {
		o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
		got := append(
			o.Observe([]byte(stream[:split]), start),
			o.Observe([]byte(stream[split:]), start.Add(time.Millisecond))...,
		)
		if len(got) != 2 {
			t.Fatalf("split at %d: got %d observations %+v", split, len(got), got)
		}
		if got[0].Claim != claimBusy || got[1].Claim != claimNotBusy {
			t.Fatalf("split at %d: got %+v", split, got)
		}
	}
}

// The spinner frame changes several times a second while the level does not.
// Carrying it in the detail made every keepalive emission a distinct row, so a
// long turn consumed one trace slot per second and evicted every other kind of
// evidence — the opposite of what the trace is for. The detail is the turn
// summary, and it must be stable across frames.
func TestHeartbeatDetailIsStableAcrossSpinnerFrames(t *testing.T) {
	start := time.Now()
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)

	var details []string
	for i, frame := range []string{"⠐", "⠸", "⠿", "⠇", "⠏"} {
		// One keepalive apart, so every frame is emitted rather than throttled.
		at := start.Add(time.Duration(i) * (heartbeatKeepalive + time.Millisecond))
		got := onlySignal(t, observeAt(o, at, title(frame+" Run background sleep command")))
		if got.Claim != claimBusy {
			t.Fatalf("frame %s: claim %q, want busy", frame, got.Claim)
		}
		details = append(details, got.Detail)
	}

	for _, detail := range details {
		if detail != "Run background sleep command" {
			t.Fatalf("details differ across frames: %q", details)
		}
	}
}

// A title that is only a spinner has no summary to report, and inventing one
// (the glyph itself) would reintroduce the churn.
func TestHeartbeatDetailIsEmptyForAGlyphOnlyTitle(t *testing.T) {
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
	if got := onlySignal(t, observeAt(o, time.Now(), title("⠐"))); got.Detail != "" {
		t.Fatalf("detail %q, want empty", got.Detail)
	}
}

// Codex puts an approval in its title, which is the only leading approval edge
// it emits: it has no notification escape and no approval hook. The titles here
// are verbatim from a codex 0.145.0 session driven through a real PTY with
// --ask-for-approval untrusted, across prompt → approve → resume.
func TestCodexTitleReportsAnApproval(t *testing.T) {
	for _, tc := range []struct {
		name        string
		title       string
		wantClaim   string
		wantSummary string
	}{
		{"running", "⠼ scratchpad", claimBusy, "scratchpad"},
		{"approval prompt on screen", "[ . ] Action Required | scratchpad", claimApproval, "scratchpad"},
		// Codex leaves the words in place and flips only the glyph, so an
		// answered prompt still carries the marker. Reading it as an approval
		// re-arms the edge on every repaint.
		{"approval answered", "[ ! ] Action Required | scratchpad", claimNotBusy, "scratchpad"},
		{"settled", "scratchpad", claimNotBusy, "scratchpad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim, summary, ok := classifyCodexTitle(tc.title)
			if !ok {
				t.Fatalf("classifyCodexTitle(%q) reported nothing", tc.title)
			}
			if claim != tc.wantClaim {
				t.Errorf("claim = %q, want %q", claim, tc.wantClaim)
			}
			if summary != tc.wantSummary {
				t.Errorf("summary = %q, want %q", summary, tc.wantSummary)
			}
		})
	}
}

// Claude's title has no approval marker — it announces approvals on the
// Notification hook instead — so the marker must not leak across agents.
func TestClaudeTitleHasNoApprovalMarker(t *testing.T) {
	claim, _, _ := classifyClaudeTitle("[ . ] Action Required | whatever")
	if claim != claimUnclassified {
		t.Fatalf("claude read a codex title as %q, want %q", claim, claimUnclassified)
	}
}
