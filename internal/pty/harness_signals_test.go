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
		name  string
		title string
		claim string
	}{
		// The braille block is the spinner; claude cycles through its frames.
		{name: "braille spinner is busy", title: "⠐ Run background sleep command", claim: claimBusy},
		{name: "another spinner frame", title: "⠸ Editing files", claim: claimBusy},
		{name: "asterisk is not busy", title: "✳ Run background sleep command", claim: claimNotBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
			got := onlySignal(t, observeAt(o, now, title(tc.title)))
			if got.Source != SourceHeartbeat || got.Claim != tc.claim {
				t.Fatalf("got %+v, want %s/%s", got, SourceHeartbeat, tc.claim)
			}
			// The title doubles as a live turn summary — a free sidebar label.
			if got.Detail != tc.title {
				t.Fatalf("detail %q, want the title verbatim", got.Detail)
			}
			if !got.At.Equal(now) {
				t.Fatalf("At %s, want %s", got.At, now)
			}
		})
	}
}

// A title claude did not write says nothing about claude. Reporting not-busy for
// it would let any subprocess that sets a title settle the session.
func TestClaudeIgnoresAForeignTitle(t *testing.T) {
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsClaude)
	for _, foreign := range []string{"victor@mac: ~", "htop", ""} {
		if got := observeAt(o, time.Now(), title(foreign)); got != nil {
			t.Fatalf("title %q produced %+v", foreign, got)
		}
	}
}

// Codex has no distinct idle glyph: busy is a spinner frame, anything else is
// the bare working directory. A hijacked title therefore reads as not-busy,
// which is safe under a freshness rule keyed on when busy frames last arrived.
func TestCodexTitleHeartbeat(t *testing.T) {
	now := time.Now()
	o := newHarnessSignalObserver(agentdriver.HarnessSignalsCodex)
	if got := onlySignal(t, observeAt(o, now, title("⠸ attn--fix-state-detec..."))); got.Claim != claimBusy {
		t.Fatalf("got %+v, want busy", got)
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

// These sources speak their own vocabulary rather than protocol state names, so
// nothing may try to apply their claims as states.
func TestHarnessSignalSourcesAreEvidenceOnly(t *testing.T) {
	if !SourceHeartbeat.EvidenceOnly() {
		t.Fatalf("%s must be evidence-only", SourceHeartbeat)
	}
	for _, source := range []Source{SourceScreen, SourceApproval, SourceWorkerInfo, SourceUnknown} {
		if source.EvidenceOnly() {
			t.Fatalf("%s must not be evidence-only", source)
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
