package pty

import (
	"strings"
	"time"
	"unicode/utf8"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// Harness-owned state signals, read off the PTY stream via the OSC 0/2 title:
//
//	ESC ] 0 ; ⠐ Run background sleep command BEL     claude, turn RUNNING
//	ESC ] 0 ; ✳ Run background sleep command BEL     claude, turn NOT running
//	ESC ] 0 ; ⠸ my-project BEL                       codex, busy
//	ESC ] 0 ; my-project BEL                         codex, idle
//
// The glyph is a *level*, repainted ~1/s by claude and ~10/s by codex;
// measured against hook ground truth it tracks the turn within ~100ms. It
// bounds stuck states rather than detecting on its own — claude's title goes
// silent ~3.5s mid blocking foreground tool call, so no TTL gives both a fast
// settle and no false settle. Nothing here drives session state yet.

const (
	// heartbeatKeepalive bounds how often an unchanged heartbeat re-emits: at
	// codex's ~10Hz every frame would evict every other kind of evidence from
	// the observation ring. 1s matches claude's natural repaint rate.
	heartbeatKeepalive = time.Second

	// The heartbeat's vocabulary — deliberately not protocol state names: the
	// title says whether the turn runs, not WHY it stopped.
	claimBusy    = "busy"
	claimNotBusy = "not_busy"
	// claimApproval: blocked on the user. Codex only; claude announces
	// approvals on its Notification hook instead.
	claimApproval = "approval"
)

const (
	oscCodeTitleAndIcon = 0
	oscCodeTitle        = 2
)

// titleClassifier reads an OSC 0 title, reporting whether the turn is running
// plus the title with its level glyph stripped (so unchanged levels collapse
// into one trace row). ok is false when the title says nothing about the agent
// (typically a subprocess overwrote it); the previous level then stands.
type titleClassifier func(title string) (claim string, summary string, ok bool)

// harnessSignalPolicy is the per-agent part; the mechanics are shared.
type harnessSignalPolicy struct {
	classifyTitle titleClassifier
}

// harnessSignalObserver watches one session's output for harness signals.
type harnessSignalObserver struct {
	policy    harnessSignalPolicy
	scanner   oscScanner
	lastClaim string
	lastEmit  time.Time
}

// newHarnessSignalObserver builds the observer the driver asked for, or nil
// for an agent with no harness signals.
func newHarnessSignalObserver(kind agentdriver.HarnessSignalKind) *harnessSignalObserver {
	switch kind {
	case agentdriver.HarnessSignalsClaude:
		return &harnessSignalObserver{policy: harnessSignalPolicy{
			classifyTitle: classifyClaudeTitle,
		}}
	case agentdriver.HarnessSignalsCodex:
		return &harnessSignalObserver{policy: harnessSignalPolicy{
			classifyTitle: classifyCodexTitle,
		}}
	default:
		return nil
	}
}

// Observe reports the harness signals in one chunk of PTY output, oldest first.
// It never modifies the chunk.
func (o *harnessSignalObserver) Observe(chunk []byte, now time.Time) []Observation {
	if o == nil || len(chunk) == 0 {
		return nil
	}
	var out []Observation
	o.scanner.Feed(chunk, func(code int, payload string) {
		switch code {
		case oscCodeTitleAndIcon, oscCodeTitle:
			if obs, ok := o.observeTitle(payload, now); ok {
				out = append(out, obs)
			}
		}
	})
	return out
}

func (o *harnessSignalObserver) observeTitle(title string, now time.Time) (Observation, bool) {
	if o.policy.classifyTitle == nil {
		return Observation{}, false
	}
	claim, summary, ok := o.policy.classifyTitle(title)
	if !ok {
		return Observation{}, false
	}
	// Re-state a level only when it changed or "still true" is old enough to
	// be news.
	if claim == o.lastClaim && now.Sub(o.lastEmit) < heartbeatKeepalive {
		return Observation{}, false
	}
	o.lastClaim = claim
	o.lastEmit = now
	return newObservation(SourceHeartbeat, claim, summary, now), true
}

// classifyClaudeTitle reads Claude Code's title glyph: a braille spinner frame
// while the turn runs, U+2733 EIGHT SPOKED ASTERISK when it does not. Anything
// else is not claude talking.
func classifyClaudeTitle(title string) (string, string, bool) {
	first, ok := firstRune(title)
	if !ok {
		return "", "", false
	}
	switch {
	case isBrailleSpinner(first):
		return claimBusy, stripLevelGlyph(title), true
	case first == '✳':
		return claimNotBusy, stripLevelGlyph(title), true
	default:
		return "", "", false
	}
}

// codexApprovalMarker is codex's title while an approval prompt is on screen:
// "[ . ] Action Required | <cwd>", glyph flipping to "!" once answered.
// Measured on codex 0.145.0 through a real PTY with --ask-for-approval
// untrusted. Matching harness UI text is a real cost — a reworded release
// silently drops the signal, degrading to pre-signal behavior — but codex has
// no notification escape (OSC vocabulary is 0, 10, 11) and no approval hook.
const codexApprovalMarker = "Action Required"

// classifyCodexTitle reads Codex's title: braille spinner = running, approval
// marker = blocked on the user, bare cwd = neither. No idle glyph, so an
// overwritten title reads as not-busy — safe under the freshness rule; a live
// capture measured a competing repaint moving accuracy by 0.2pp.
func classifyCodexTitle(title string) (string, string, bool) {
	first, ok := firstRune(title)
	if !ok {
		return "", "", false
	}
	if isBrailleSpinner(first) {
		return claimBusy, stripLevelGlyph(title), true
	}
	// Codex leaves the marker words after the prompt is answered and flips
	// only the glyph — the marker alone would re-arm the approval forever.
	if strings.Contains(title, codexApprovalMarker) {
		if codexApprovalPending(title) {
			return claimApproval, codexTitleSummary(title), true
		}
		return claimNotBusy, codexTitleSummary(title), true
	}
	return claimNotBusy, stripLevelGlyph(title), true
}

// codexApprovalPending reads the glyph before the marker: "." while waiting,
// "!" once answered (measured on codex 0.145.0). Neither glyph reads as
// answered — a missed approval costs a moment, a stuck one nags forever.
func codexApprovalPending(title string) bool {
	prefix, _, found := strings.Cut(title, codexApprovalMarker)
	if !found {
		return false
	}
	return strings.Contains(prefix, ".")
}

// codexTitleSummary strips the "[ x ] Action Required | " prefix.
func codexTitleSummary(title string) string {
	if _, rest, found := strings.Cut(title, "|"); found {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(title)
}

// stripLevelGlyph removes a leading level glyph and the space after it. A
// glyph-only title leaves an empty summary — the honest answer.
func stripLevelGlyph(title string) string {
	trimmed := strings.TrimSpace(title)
	first, size := utf8.DecodeRuneInString(trimmed)
	if isBrailleSpinner(first) || first == '✳' {
		return strings.TrimSpace(trimmed[size:])
	}
	return trimmed
}

// isBrailleSpinner reports whether r is in the Braille Patterns block, which
// both agents animate their spinners with.
func isBrailleSpinner(r rune) bool {
	return r >= 0x2800 && r <= 0x28FF
}

// firstRune returns the first non-space rune of s.
func firstRune(s string) (rune, bool) {
	trimmed := strings.TrimLeft(s, " \t")
	if trimmed == "" {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(trimmed)
	if r == utf8.RuneError && size <= 1 {
		return 0, false
	}
	return r, true
}
