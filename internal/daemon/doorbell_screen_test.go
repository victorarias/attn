package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// The fixtures are real viewports captured from live claude sessions, with the
// prose redacted and the structure left exactly as it rendered — the structure
// is the whole of what the guard reads.
func readDoorbellScreen(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "doorbell", name+".txt"))
	if err != nil {
		t.Fatalf("read screen fixture %s: %v", name, err)
	}
	return string(text)
}

func TestScreenShowsSelector(t *testing.T) {
	// claude-question-selector is the case that motivates the guard: the
	// question tool fires no permission hook, so the session still reads
	// `working` while the screen waits for a keypress.
	// claude-resume-selector says "Enter to confirm", not "to select" — it is
	// caught on "Esc to cancel", which is why both halves of the pattern are
	// there.
	for _, name := range []string{"claude-question-selector", "claude-resume-selector"} {
		line, blocked := screenShowsSelector(readDoorbellScreen(t, name))
		if !blocked {
			t.Errorf("%s: the guard did not see a selector", name)
			continue
		}
		if !strings.Contains(strings.ToLower(line), "to select") &&
			!strings.Contains(strings.ToLower(line), "esc to cancel") {
			t.Errorf("%s: named %q as the selector line, which says neither", name, line)
		}
	}

	for _, name := range []string{"claude-composer-working", "claude-composer-idle"} {
		if line, blocked := screenShowsSelector(readDoorbellScreen(t, name)); blocked {
			t.Errorf("%s: a composer was held off as a selector, on %q", name, line)
		}
	}
}

// The footer is read a bounded distance up the viewport, so a selector that has
// already scrolled away does not hold a nudge off forever. Receipt for the
// depth is in doorbell_screen.go: on the captured corpus 6, 8 and 12 lines find
// the same screens, and 20 starts matching assistant prose.
func TestScreenShowsSelectorReadsOnlyTheFooter(t *testing.T) {
	scrolled := "Enter to select · Esc to cancel\n" +
		strings.Repeat("a line of ordinary output\n", doorbellScreenTailLines)
	if line, blocked := screenShowsSelector(scrolled); blocked {
		t.Fatalf("a selector that scrolled out of the footer still blocked, on %q", line)
	}

	// Blank lines are not what pushes it out of range: a selector footer is
	// routinely followed by them.
	padded := "Enter to select · Esc to cancel\n\n\n\n\n\n\n\n\n\n"
	if _, blocked := screenShowsSelector(padded); !blocked {
		t.Fatal("blank padding hid a selector footer from the guard")
	}
}

// Prose is where a word-based rule can go wrong, so the words were chosen to
// not appear in one. "to confirm" is deliberately absent from the pattern for
// exactly this reason.
func TestScreenShowsSelectorLeavesProseAlone(t *testing.T) {
	prose := []string{
		"∴ Let me check the AGENTS.md rules to confirm this is a docs-only PR, then start",
		"  I will pick the branch to rebase onto and then open the PR.",
		"  Waiting for you to choose which one to keep.",
	}
	for _, line := range prose {
		if _, blocked := screenShowsSelector(line); blocked {
			t.Errorf("assistant prose was read as a selector: %q", line)
		}
	}
}

func TestScreenShowsSelectorOnAnEmptyScreen(t *testing.T) {
	if _, blocked := screenShowsSelector(""); blocked {
		t.Fatal("an empty viewport was read as a selector")
	}
}

// The guard where it actually runs: a doorbell must not type at a screen that
// is waiting for a keypress, because the paste plus its Enter would answer the
// selector instead of reaching the agent.
func TestDoorbellHeldOffByAnOnScreenSelector(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	var typed [][]byte
	backend.onInput = func(_ string, data []byte) { typed = append(typed, data) }
	sessionID := "session-selector"
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: sessionID, Label: "member", Agent: protocol.SessionAgentClaude,
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	backend.screen = readDoorbellScreen(t, "claude-question-selector")
	if err := d.typeDoorbell(sessionID, "[attn] hand off now"); !errors.Is(err, errDoorbellBlockedBySelector) {
		t.Fatalf("typing at a selector returned %v, want errDoorbellBlockedBySelector", err)
	}
	if len(typed) != 0 {
		t.Fatalf("the doorbell wrote %q at a screen waiting for a keypress", typed)
	}
	if err := d.submitDoorbell(sessionID); !errors.Is(err, errDoorbellBlockedBySelector) {
		t.Fatalf("submitting at a selector returned %v, want errDoorbellBlockedBySelector", err)
	}

	// Same session, same state, composer back: the guard is about the screen and
	// nothing else, and it must not leave the nudge stuck once the selector goes.
	backend.screen = readDoorbellScreen(t, "claude-composer-working")
	if err := d.typeDoorbell(sessionID, "[attn] hand off now"); err != nil {
		t.Fatalf("typing at a composer failed: %v", err)
	}
	if len(typed) == 0 {
		t.Fatal("the doorbell wrote nothing at a composer")
	}
}

// A backend that cannot render — an older worker, a session with no frame yet —
// delivers as it did before the guard existed. Failing closed on a missing
// capability would turn a snapshot outage into a silent nudge outage.
func TestDoorbellDeliversWhenTheScreenIsUnavailable(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	var typed [][]byte
	backend.onInput = func(_ string, data []byte) { typed = append(typed, data) }
	sessionID := "session-no-screen"
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: sessionID, Label: "member", Agent: protocol.SessionAgentClaude,
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	if err := d.typeDoorbell(sessionID, "[attn] hand off now"); err != nil {
		t.Fatalf("typing without a screen failed: %v", err)
	}
	if len(typed) == 0 {
		t.Fatal("the doorbell wrote nothing when the backend had no screen to show it")
	}
}
