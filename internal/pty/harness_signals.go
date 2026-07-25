package pty

import (
	"strings"
	"time"
	"unicode/utf8"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

// Harness-owned state signals, read off the PTY stream.
//
// Both Claude Code and Codex already broadcast what they are doing, and attn has
// been ignoring it in favour of scraping the rendered screen:
//
//	ESC ] 0 ; ⠐ Run background sleep command BEL     claude, turn RUNNING
//	ESC ] 0 ; ✳ Run background sleep command BEL     claude, turn NOT running
//	ESC ] 0 ; ⠸ my-project BEL                       codex, busy
//	ESC ] 0 ; my-project BEL                         codex, idle
//
// The title glyph is a *level*, repainted about once a second by claude and ten
// times a second by codex, and it comes from the harness rather than from
// guessing at its TUI. Measured against hook ground truth it tracks the turn to
// within ~100ms in both directions.
//
// What it is for is bounding stuck states, not accuracy: a level that stops
// arriving cannot get stuck the way an edge-triggered claim can. It is not a
// detector on its own — claude's title goes silent for ~3.5s in the middle of a
// blocking foreground tool call, so no TTL gives both a fast settle and no false
// settle.
//
// Nothing here drives session state yet. The observations are recorded as
// evidence so the traces can be compared against the current behavior before
// anything starts arbitrating on them.

const (
	// heartbeatKeepalive bounds how often an unchanged heartbeat re-emits. The
	// level says "still true", so a re-emit carries real information (the agent is
	// alive right now), but at codex's ~10Hz repaint every frame would drown out
	// every other kind of evidence in the observation ring. 1s matches claude's
	// natural repaint rate.
	heartbeatKeepalive = time.Second

	// claimBusy and claimNotBusy are the heartbeat's vocabulary. It deliberately
	// does not speak in protocol state names: the title says whether the agent's
	// turn is running, and nothing about *why* it stopped — an approval, a
	// question, and a finished turn all look identical to it.
	claimBusy    = "busy"
	claimNotBusy = "not_busy"
	// claimApproval is the title saying the agent is blocked on the user. Only
	// codex has it; claude announces approvals on its Notification hook instead.
	claimApproval = "approval"
)

const (
	oscCodeTitleAndIcon = 0
	oscCodeTitle        = 2
)

// titleClassifier reads an OSC 0 title. It reports whether the agent's turn is
// running, plus the title with its level glyph removed.
//
// The glyph is stripped because it is the noisiest part of the title and the
// least informative: it cycles through spinner frames several times a second
// while saying nothing that the busy/not-busy claim does not already say. What
// is left is the turn summary, which changes only when the agent moves on to
// something else — so consecutive observations of an unchanged level are
// genuinely identical and collapse into one trace row instead of consuming one
// ring slot per second and evicting every other kind of evidence.
//
// ok is false when the title says nothing about the agent — most often because a
// subprocess overwrote it — in which case no observation is made and whatever
// the previous level was still stands.
type titleClassifier func(title string) (claim string, summary string, ok bool)

// harnessSignalPolicy is the per-agent part. The mechanics below (scanning,
// rate limiting, timestamping) are shared; only the reading of a title differs.
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

// newHarnessSignalObserver builds the observer the driver asked for, or nil (as
// a nil interface value, so callers' nil checks work) for an agent with no
// harness signals.
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
	// A level only needs re-stating periodically: it changed, or it has been long
	// enough that "still true" is news.
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

// codexApprovalMarker is what codex puts in its title while an approval prompt
// is on screen: "[ . ] Action Required | <cwd>", switching the glyph to "!" the
// moment it is answered. Measured on codex 0.145.0 driven through a real PTY
// with --ask-for-approval untrusted.
//
// Matching harness UI text is a real cost and worth naming: a codex release
// that rewords this silently drops the signal. It degrades safely — the title
// stops looking busy, which is what it already did before this existed, so the
// worst case is the behavior attn shipped without it. The alternative is worse:
// codex has no notification escape at all (its OSC vocabulary is 0, 10, 11 —
// there is no 777) and no approval hook, so the title is the only leading edge
// available.
const codexApprovalMarker = "Action Required"

// classifyCodexTitle reads Codex's title. A braille spinner frame means the turn
// is running; the approval marker means it is blocked on the user; the bare
// working directory means neither.
//
// Unlike claude there is no distinct idle glyph, so a title a subprocess
// overwrote reads as not-busy. That is safe under a freshness rule — the level
// that matters is *when busy frames last arrived* — and a live capture confirmed
// a competing title repaint moves accuracy by 0.2pp, because the agent keeps
// painting over it.
func classifyCodexTitle(title string) (string, string, bool) {
	first, ok := firstRune(title)
	if !ok {
		return "", "", false
	}
	if isBrailleSpinner(first) {
		return claimBusy, stripLevelGlyph(title), true
	}
	// The marker is a prefix on an otherwise ordinary title, so the summary is
	// whatever follows the separator — the cwd, same as any other codex title.
	if strings.Contains(title, codexApprovalMarker) {
		return claimApproval, codexTitleSummary(title), true
	}
	return claimNotBusy, stripLevelGlyph(title), true
}

// codexTitleSummary strips the "[ x ] Action Required | " prefix.
func codexTitleSummary(title string) string {
	if _, rest, found := strings.Cut(title, "|"); found {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(title)
}

// stripLevelGlyph removes a leading level glyph and the space after it, leaving
// the turn summary. A title that is only a glyph leaves an empty summary, which
// is the honest answer: there was no summary.
func stripLevelGlyph(title string) string {
	trimmed := strings.TrimSpace(title)
	first, size := utf8.DecodeRuneInString(trimmed)
	if isBrailleSpinner(first) || first == '✳' {
		return strings.TrimSpace(trimmed[size:])
	}
	return trimmed
}

// isBrailleSpinner reports whether r is in the Braille Patterns block, which is
// what both agents animate their spinners with.
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
