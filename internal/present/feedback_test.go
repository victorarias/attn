package present

import (
	"os"
	"strings"
	"testing"
)

// writeFile writes content and commits it, returning the resulting commit SHA.
func writeCommit(t *testing.T, dir, path, content, message string) string {
	t.Helper()
	if err := os.WriteFile(dir+"/"+path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	runGit(t, dir, "add", path)
	runGit(t, dir, "commit", "-m", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func TestRenderFeedback_UnsubmittedNoComments(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	sha := writeCommit(t, dir, "a.txt", "one\ntwo\n", "init")

	got := RenderFeedback(dir, "My Change", 1, sha, sha, "", "", nil)
	if !strings.Contains(got, "Round not submitted yet.") {
		t.Errorf("RenderFeedback() = %q, want the not-submitted line", got)
	}
	if strings.Contains(got, "handed back clean") {
		t.Errorf("RenderFeedback() = %q, unsubmitted round should not contain handed back clean message", got)
	}
}

func TestRenderFeedback_SubmittedNoComments(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	sha := writeCommit(t, dir, "a.txt", "one\ntwo\n", "init")

	got := RenderFeedback(dir, "My Change", 1, sha, sha, "2026-07-01T00:00:00Z", "", nil)
	if !strings.Contains(got, "No comments — round handed back clean.") {
		t.Errorf("RenderFeedback() = %q, want the clean-handback line", got)
	}
	if !strings.Contains(got, "Submitted: 2026-07-01T00:00:00Z") {
		t.Errorf("RenderFeedback() = %q, want the submitted timestamp", got)
	}
}

func TestRenderFeedback_ApprovedVerdict(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	sha := writeCommit(t, dir, "a.txt", "one\ntwo\n", "init")

	comments := []FeedbackComment{
		{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "nit: rename this"},
	}
	got := RenderFeedback(dir, "My Change", 1, sha, sha, "2026-07-01T00:00:00Z", "approved", comments)
	if !strings.Contains(got, "**Approved.**") {
		t.Errorf("RenderFeedback() = %q, want the Approved verdict line", got)
	}
	if !strings.Contains(got, "nit: rename this") {
		t.Errorf("RenderFeedback() approve-with-nits dropped the comment: %q", got)
	}
}

func TestRenderFeedback_FeedbackVerdictUnchangedShape(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	sha := writeCommit(t, dir, "a.txt", "one\ntwo\n", "init")

	got := RenderFeedback(dir, "My Change", 1, sha, sha, "2026-07-01T00:00:00Z", "feedback", nil)
	if strings.Contains(got, "**Approved.**") {
		t.Errorf("RenderFeedback() = %q, feedback verdict should not render an Approved line", got)
	}
}

func TestRenderFeedback_QuotesAndSides(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	baseSHA := writeCommit(t, dir, "a.txt", "line one\nline two\nline three\n", "base")
	headSHA := writeCommit(t, dir, "a.txt", "line ONE\nline two\nline three\nline four\n", "head")

	comments := []FeedbackComment{
		{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "old", Content: "was this intentional?"},
		{Filepath: "a.txt", LineStart: 4, LineEnd: 4, Side: "new", Content: "nice addition"},
	}

	got := RenderFeedback(dir, "My Change", 2, baseSHA, headSHA, "2026-07-01T00:00:00Z", "feedback", comments)

	if !strings.Contains(got, "Submitted: 2026-07-01T00:00:00Z") {
		t.Errorf("RenderFeedback() missing submitted timestamp: %q", got)
	}
	if !strings.Contains(got, "### a.txt:1 (old)") || !strings.Contains(got, "line one") {
		t.Errorf("RenderFeedback() missing old-side quote: %q", got)
	}
	if strings.Contains(got, "line ONE") {
		t.Errorf("RenderFeedback() quoted the new-side content for an old-side comment: %q", got)
	}
	if !strings.Contains(got, "### a.txt:4 (new)") || !strings.Contains(got, "line four") {
		t.Errorf("RenderFeedback() missing new-side quote: %q", got)
	}
	if !strings.Contains(got, "was this intentional?") || !strings.Contains(got, "nice addition") {
		t.Errorf("RenderFeedback() missing comment content: %q", got)
	}
}

func TestRenderFeedback_GroupsByFileKeepingOrder(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	sha := writeCommit(t, dir, "a.txt", "a1\na2\n", "a")
	runGit(t, dir, "add", "-A")
	if err := os.WriteFile(dir+"/b.txt", []byte("b1\nb2\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	runGit(t, dir, "add", "b.txt")
	runGit(t, dir, "commit", "-m", "add b")
	headSHA := runGit(t, dir, "rev-parse", "HEAD")

	comments := []FeedbackComment{
		{Filepath: "b.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "first, on b"},
		{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "second, on a"},
		{Filepath: "b.txt", LineStart: 2, LineEnd: 2, Side: "new", Content: "third, on b again"},
	}

	got := RenderFeedback(dir, "Multi File", 1, sha, headSHA, "", "", comments)

	bIdx := strings.Index(got, "## b.txt")
	aIdx := strings.Index(got, "## a.txt")
	if bIdx == -1 || aIdx == -1 || bIdx > aIdx {
		t.Fatalf("RenderFeedback() did not preserve first-seen file order (b.txt then a.txt): %q", got)
	}
	firstIdx := strings.Index(got, "first, on b")
	thirdIdx := strings.Index(got, "third, on b again")
	if firstIdx == -1 || thirdIdx == -1 || firstIdx > thirdIdx {
		t.Fatalf("RenderFeedback() did not preserve comment order within a file: %q", got)
	}
}

func TestRenderFeedback_OutOfRangeQuoteOmitted(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	sha := writeCommit(t, dir, "a.txt", "only one line\n", "init")

	comments := []FeedbackComment{
		{Filepath: "a.txt", LineStart: 50, LineEnd: 52, Side: "new", Content: "out of range comment"},
		{Filepath: "missing.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "file does not exist at this SHA"},
	}

	got := RenderFeedback(dir, "Edge Cases", 1, sha, sha, "", "", comments)

	if !strings.Contains(got, "out of range comment") || !strings.Contains(got, "file does not exist at this SHA") {
		t.Fatalf("RenderFeedback() dropped comment content instead of just omitting the quote: %q", got)
	}
	if strings.Contains(got, "```") {
		t.Errorf("RenderFeedback() rendered a quote fence for out-of-range/missing content: %q", got)
	}
}
