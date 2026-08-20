package daemon

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"
)

// A doorbell types into whatever is on screen, and a session's state is only as
// fresh as its last classification. Claude's question tool parks on a selector
// without firing the permission hook that would make attn call the session
// pending_approval, so the state still reads `working` while the screen waits
// for a keypress — and a bracketed paste followed by Enter answers it.
//
// Receipt, over 44,724 viewports captured from live sessions between
// 2026-08-03 and 2026-08-13: 47 of 40,130 screens in `working` were selectors,
// and 6 of 2,217 in `idle`. Rare, and exactly the screens a nudge must not type
// into.
const (
	// doorbellScreenTailLines is how far up the viewport the footer is read. A
	// selector prints its keys on the last line; a composer's own footer is
	// four lines tall. On the corpus above 6, 8 and 12 find the same 47
	// screens and 20 starts matching assistant prose, so 8 is the tripwire:
	// past anything real, short of the noise.
	doorbellScreenTailLines = 8

	doorbellScreenTimeout = 2 * time.Second
)

// doorbellSelectorFooter matches the footer a selector prints. It reads words
// rather than glyphs on purpose: claude changed which glyphs it animates with
// inside one minor version, while every one of the 25 distinct footers in the
// corpus says "to select" or "Esc to cancel".
var doorbellSelectorFooter = regexp.MustCompile(`(?i)\bto select\b|\besc to cancel\b`)

// screenShowsSelector reports whether a rendered viewport is waiting for a
// keypress rather than for words, and the line that says so.
func screenShowsSelector(text string) (string, bool) {
	lines := make([]string, 0, doorbellScreenTailLines)
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > doorbellScreenTailLines {
		lines = lines[len(lines)-doorbellScreenTailLines:]
	}
	for _, line := range lines {
		if doorbellSelectorFooter.MatchString(line) {
			return line, true
		}
	}
	return "", false
}

// doorbellSelectorOnScreen reads the session's authoritative viewport and
// reports the selector line if one is up.
//
// A backend that cannot answer — an older worker, a session with no rendered
// frame yet — reports nothing and delivery proceeds as it did before this
// guard existed. Failing closed on a missing capability would turn a snapshot
// outage into a silent nudge outage, a bigger hole than the one this closes.
func (d *Daemon) doorbellSelectorOnScreen(sessionID string) (string, bool) {
	if d.ptyBackend == nil {
		return "", false
	}
	provider, ok := d.ptyBackend.(ptybackend.SnapshotProvider)
	if !ok {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), doorbellScreenTimeout)
	defer cancel()
	snapshot, err := provider.Snapshot(ctx, sessionID)
	if err != nil || snapshot.Screen == nil || !snapshot.Screen.HasText {
		return "", false
	}
	return screenShowsSelector(snapshot.Screen.Text)
}
