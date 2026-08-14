package crew

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// A woken member's launch priming: who it is, the letter its predecessor left,
// and the verbs of its own home. Composed here so the text lives beside the id
// rule it teaches; the daemon reads the files and hands them over.
//
// Without injected guidance a member does not know its own verbs — reading a
// charter confers nothing, and a session woken as trellis that was never told
// how a handoff is filed cannot file one.

// HandoffsDirName is where a member's letters live under its home.
const HandoffsDirName = "handoffs"

// What one launch may inline of the two member-authored files. Measured
// 2026-08-14 over the simulation's real homes: the largest of three charters is
// 5,569 bytes and the largest of 23 filed handoffs is 6,601. Both limits sit
// well past that, so only a file nothing like a real one is ever cut — and the
// cut says where the whole text is.
const (
	CharterLimit = 16000
	HandoffLimit = 16000
)

// Priming is one wake's material: the member's record, the prose read off its
// home, and the names of the older letters it may drill into.
type Priming struct {
	Member        string
	HomeDir       string
	CharterPath   string
	CWD           string
	AwarenessDirs []string

	// Charter is CHARTER.md's text; empty when the member has none yet, which
	// makes this the member's first day.
	Charter string
	// Handoff is the freshest letter's text and HandoffName its file name.
	Handoff     string
	HandoffName string
	// OlderHandoffs names every earlier letter, freshest first. Names only:
	// depth on demand replaces forgetting, and a wake that loaded them all
	// would be the ceremony the primitive exists to remove.
	OlderHandoffs []string
}

// SortHandoffNames orders letters freshest first. The file names are UTC
// timestamps, so lexicographic order is chronological — the same rule the
// freshest-note read uses.
func SortHandoffNames(names []string) {
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
}

// Block is the system-prompt text injected at a member's wake. Empty Member
// yields nothing: an unbound session is primed with no crew block at all.
func (p Priming) Block() string {
	if strings.TrimSpace(p.Member) == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, `You are **%[1]s**, a crew member of this attn home. Identity is this invocation: you are %[1]s because attn woke you as %[1]s, not because you read any file. Your sessions are your days — this one is today.

Your home is `+"`%[2]s`"+`, plain markdown, hand-editable by you and by Victor; attn reads it and never rewrites your prose.
`, p.Member, p.HomeDir)

	if cwd := strings.TrimSpace(p.CWD); cwd != "" {
		fmt.Fprintf(&b, "You launched in `%s`.\n", cwd)
	}
	if dirs := trimmed(p.AwarenessDirs); len(dirs) > 0 {
		fmt.Fprintf(&b, "Your charter is also about %s — reachable from this session.\n", quotedList(dirs))
	}

	if charter := strings.TrimSpace(p.Charter); charter != "" {
		fmt.Fprintf(&b, "\n## Your charter (%s)\n\n%s\n", p.CharterPath, cut(charter, CharterLimit, p.CharterPath))
	} else {
		fmt.Fprintf(&b, "\nThere is no `%s` yet, so this is your first day. Agree the name and the charter with Victor, then write that file yourself, in your own words — a self, not a job description.\n",
			filepath.Join(p.HomeDir, CharterFileName))
	}

	handoffsDir := filepath.Join(p.HomeDir, HandoffsDirName)
	if handoff := strings.TrimSpace(p.Handoff); handoff != "" {
		fmt.Fprintf(&b, "\n## Your predecessor's letter (%s)\n\nIt was written to you at the close of the last day. Trust it as their honest closure, and verify anything load-bearing — branches, PRs, running delegations — before acting on it; the world moved while you slept.\n\n%s\n",
			p.HandoffName, cut(handoff, HandoffLimit, filepath.Join(handoffsDir, p.HandoffName)))
	} else {
		fmt.Fprintf(&b, "\nNo letter was left for you in `%s`. Ask Victor where things stand rather than guessing.\n", handoffsDir)
	}
	if older := trimmed(p.OlderHandoffs); len(older) > 0 {
		fmt.Fprintf(&b, "\nEarlier letters, freshest first — read one when the freshest note points at it or the work needs the history: %s\n", strings.Join(backticked(older), ", "))
	}

	fmt.Fprintf(&b, `
## How your day ends

Closure is consented: it runs when you or Victor calls for it, never silently. When this day ends, write your successor a letter in your own words — where things stand precisely enough to resume, what you learned, what you would do next, and anything Victor should decide — and file it at `+"`%s/<UTC timestamp>-%s.md`"+` (for example `+"`2026-08-14T19-30Z-%[2]s.md`"+`). The line is append-only: never edit a filed letter; a correction is a new one.

That letter is your **day-line**, and it is not a seed handoff. A seed handoff (`+"`attn seed note <id> --handoff`"+`) is the *work item's* thread for whoever tends it next, member or worker; yours is the *member's* thread for your own successor. Both stand on their own — point at a seed from your letter if it helps, but never file one where the other belongs.

Only one session is %[2]s at a time. Parallelism means waking another member, never a second copy of you.
`, handoffsDir, p.Member)

	return strings.TrimRight(b.String(), "\n")
}

// cut bounds an inlined file, saying where the rest is rather than ending
// mid-sentence with no way back. Markdown carries any Unicode, so the cut lands
// on a rune boundary.
func cut(text string, limit int, path string) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + fmt.Sprintf("\n\n[cut at %d bytes of %d — the whole file is at %s]", end, len(text), path)
}

func trimmed(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func backticked(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, "`"+value+"`")
	}
	return out
}

func quotedList(values []string) string {
	quoted := backticked(values)
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
