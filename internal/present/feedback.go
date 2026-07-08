package present

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/git"
)

// FeedbackComment is a single inline review comment anchored to a line range
// within one side (old/new) of a file, ready to be rendered back to the
// authoring agent as markdown.
type FeedbackComment struct {
	Filepath  string
	LineStart int
	LineEnd   int
	Side      string
	Content   string
}

// RenderFeedback renders a round's review comments as markdown for the
// authoring agent to read via `attn present feedback`. Comments are grouped by
// file, keeping the input order both across files and within a file. Each
// comment is preceded by a fenced quote of the referenced lines, fetched from
// git at the round's pinned base/head SHA (side "old" reads baseSHA, "new"
// reads headSHA) — but a quote is best-effort: if git fails or the lines are
// out of range, the quote is silently omitted rather than failing the whole
// render. verdict is "" (unsubmitted), "approved", or "feedback" — "approved"
// adds a bold verdict line right after the submitted line; "feedback" and ""
// leave the rendered shape exactly as it was before verdicts existed.
func RenderFeedback(repoPath, title string, seq int, baseSHA, headSHA string, submittedAt string, verdict string, comments []FeedbackComment) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s — round %d\n\n", title, seq)
	fmt.Fprintf(&b, "%s..%s\n\n", shortSHA(baseSHA), shortSHA(headSHA))

	isSubmitted := strings.TrimSpace(submittedAt) != ""
	if !isSubmitted {
		b.WriteString("Round not submitted yet.\n")
		if len(comments) == 0 {
			return b.String()
		}
	} else {
		fmt.Fprintf(&b, "Submitted: %s\n", submittedAt)
	}

	if verdict == "approved" {
		b.WriteString("\n**Approved.**\n")
	}

	if len(comments) == 0 {
		b.WriteString("\nNo comments — round handed back clean.\n")
		return b.String()
	}

	cache := map[[2]string][]string{}
	fetchLines := func(sha, filepath string) []string {
		key := [2]string{sha, filepath}
		lines, cached := cache[key]
		if cached {
			return lines
		}
		out, err := git.Output(git.OpMetadata, repoPath, "show", sha+":"+filepath)
		if err == nil {
			lines = strings.Split(string(out), "\n")
		}
		cache[key] = lines
		return lines
	}

	var order []string
	byFile := map[string][]FeedbackComment{}
	for _, c := range comments {
		if _, seen := byFile[c.Filepath]; !seen {
			order = append(order, c.Filepath)
		}
		byFile[c.Filepath] = append(byFile[c.Filepath], c)
	}

	for _, fp := range order {
		fmt.Fprintf(&b, "\n## %s\n", fp)
		for _, c := range byFile[fp] {
			lineRange := fmt.Sprintf("%d", c.LineStart)
			if c.LineEnd != c.LineStart {
				lineRange = fmt.Sprintf("%d-%d", c.LineStart, c.LineEnd)
			}
			fmt.Fprintf(&b, "\n### %s:%s (%s)\n", fp, lineRange, c.Side)

			sha := headSHA
			if c.Side == "old" {
				sha = baseSHA
			}
			if quote := sliceLines(fetchLines(sha, fp), c.LineStart, c.LineEnd); quote != "" {
				fmt.Fprintf(&b, "\n```\n%s\n```\n", quote)
			}
			fmt.Fprintf(&b, "\n%s\n", c.Content)
		}
	}

	return b.String()
}

// sliceLines returns the 1-indexed, inclusive line range [start, end] joined
// with newlines, or "" if the range is invalid or entirely out of bounds.
func sliceLines(lines []string, start, end int) string {
	if start < 1 || end < start || start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
