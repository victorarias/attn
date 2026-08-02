package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

func TestSanitizeSessionTitle(t *testing.T) {
	longMultibyte := strings.Repeat("é", 60) // multibyte rune: a byte-based cut would corrupt/mis-truncate this
	wantLongMultibyte := strings.Repeat("é", maxSessionTitleRunes)

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "Fix login flow", "Fix login flow"},
		{"double_quotes", `"Fix login flow"`, "Fix login flow"},
		{"backticks", "`Fix login flow`", "Fix login flow"},
		{"curly_quotes", "“Fix login flow”", "Fix login flow"},
		{"title_prefix", "Title: Fix login flow", "Fix login flow"},
		{"title_prefix_lower", "title: fix login flow", "fix login flow"},
		{"multiline_first_nonempty", "\n  \nFix login flow\nsecond line", "Fix login flow"},
		{"internal_whitespace_collapsed", "Fix   login\tflow", "Fix login flow"},
		{"trailing_punctuation", "Fix login flow.", "Fix login flow"},
		{"trailing_punctuation_mixed", "Fix login flow!;", "Fix login flow"},
		{"over_limit_multibyte", longMultibyte, wantLongMultibyte},
		{"empty", "", ""},
		{"whitespace_only", "   \n\t  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeSessionTitle(tc.raw)
			if got != tc.want {
				t.Errorf("sanitizeSessionTitle(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDefaultSessionLabel(t *testing.T) {
	cases := []struct {
		name      string
		cwd       string
		sessionID string
		want      string
	}{
		{"normal_path", "/Users/victor/projects/attn", "sess-1", "attn"},
		{"empty_cwd", "", "sess-1", "sess-1"},
		{"dot_cwd", ".", "sess-1", "sess-1"},
		{"root_cwd", string(filepath.Separator), "sess-1", "sess-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultSessionLabel(tc.cwd, tc.sessionID)
			if got != tc.want {
				t.Errorf("defaultSessionLabel(%q, %q) = %q, want %q", tc.cwd, tc.sessionID, got, tc.want)
			}
		})
	}
}

// writeSessionTitleTranscript writes a minimal Claude-format transcript JSONL
// with a genuine human turn, so slice.Brief is non-empty.
func writeSessionTitleTranscript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"session_meta","cwd":"/tmp"}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"content":"fix the login flow"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"looking into it"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// writeSessionTitleTranscriptNoUserContent writes a transcript with no
// genuine human turn (only a tool-result-shaped user line), so slice.Brief
// stays empty.
func writeSessionTitleTranscriptNoUserContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"type":"session_meta","cwd":"/tmp"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","text":"some tool output"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// seedSessionTitleSession adds a session whose label is its directory's
// default label (filepath.Base) unless label is explicitly given.
func seedSessionTitleSession(t *testing.T, d *Daemon, id, directory, label string) *protocol.Session {
	t.Helper()
	if label == "" {
		label = defaultSessionLabel(directory, id)
	}
	session := &protocol.Session{
		ID:        id,
		Label:     label,
		Agent:     protocol.SessionAgentClaude,
		Directory: directory,
		State:     protocol.SessionStateIdle,
	}
	d.store.Add(session)
	return session
}

func TestMaybeGenerateSessionTitle_HappyPath(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	if calls != 1 {
		t.Fatalf("exec calls = %d, want 1", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}

func TestMaybeGenerateSessionTitle_CustomLabelSkipped(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "user renamed me")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	if calls != 0 {
		t.Fatalf("exec calls = %d, want 0 (custom label must never be clobbered)", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "user renamed me" {
		t.Fatalf("session label = %+v, want unchanged", got)
	}
}

func TestMaybeGenerateSessionTitle_ExecError(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)
	wantLabel := defaultSessionLabel(directory, "sess-1")

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		return "", errors.New("boom")
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	got := d.store.Get("sess-1")
	if got == nil || got.Label != wantLabel {
		t.Fatalf("session label = %+v, want unchanged %q", got, wantLabel)
	}
}

func TestMaybeGenerateSessionTitle_UnusableOutput(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)
	wantLabel := defaultSessionLabel(directory, "sess-1")

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		return "   \n\t  ", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	got := d.store.Get("sess-1")
	if got == nil || got.Label != wantLabel {
		t.Fatalf("session label = %+v, want unchanged %q", got, wantLabel)
	}
}

func TestMaybeGenerateSessionTitle_OneAttemptPerSession(t *testing.T) {
	directory := t.TempDir()
	transcriptPath := writeSessionTitleTranscript(t)

	t.Run("after_success", func(t *testing.T) {
		d := newDaemonForTest(t)
		seedSessionTitleSession(t, d, "sess-1", directory, "")
		calls := 0
		d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
			calls++
			return "Fix login flow", nil
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		if calls != 1 {
			t.Fatalf("first call: exec calls = %d, want 1", calls)
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		if calls != 1 {
			t.Fatalf("second call: exec calls = %d, want still 1 (label no longer default)", calls)
		}
	})

	t.Run("after_failure", func(t *testing.T) {
		d := newDaemonForTest(t)
		seedSessionTitleSession(t, d, "sess-1", directory, "")
		calls := 0
		d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
			calls++
			return "", errors.New("boom")
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		if calls != 1 {
			t.Fatalf("first call: exec calls = %d, want 1", calls)
		}
		d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		if calls != 1 {
			t.Fatalf("second call after failure: exec calls = %d, want still 1 (attempted-guard)", calls)
		}
	})
}

func TestMaybeGenerateSessionTitle_RenameRace(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		// Simulate a user rename landing while the LLM call is in flight.
		d.store.UpdateSessionLabel("sess-1", "user choice")
		return "llm title", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	got := d.store.Get("sess-1")
	if got == nil || got.Label != "user choice" {
		t.Fatalf("session label = %+v, want %q (must not clobber the in-flight rename)", got, "user choice")
	}
}

func TestMaybeGenerateSessionTitle_EmptyTranscriptNotMarkedAttempted(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	emptyTranscriptPath := writeSessionTitleTranscriptNoUserContent(t)
	goodTranscriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", emptyTranscriptPath)
	if calls != 0 {
		t.Fatalf("exec calls after empty transcript = %d, want 0", calls)
	}

	d.maybeGenerateSessionTitle("sess-1", goodTranscriptPath)
	if calls != 1 {
		t.Fatalf("exec calls after good transcript = %d, want 1 (empty transcript must not have marked attempted)", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}

// TestMaybeGenerateSessionTitle_ConcurrentAttemptRunsExecOnce simulates two
// Stop events for the same session interleaving between the early
// attempted-check and the mark: exec, on its first invocation, synchronously
// re-enters maybeGenerateSessionTitle for the same session — standing in for a
// second concurrent Stop that raced past the early attempted-check while the
// first call was still extracting the transcript slice, before either had
// marked the session attempted. At that point the session's label is still
// default (the outer call hasn't written the title yet), so only the
// attempted-guard — not the label check — can stop the inner call. With the
// fix, the inner call's re-check under the lock finds the session already
// marked and returns without a second exec; if the guard mutex were instead
// held across exec, this would deadlock.
func TestMaybeGenerateSessionTitle_ConcurrentAttemptRunsExecOnce(t *testing.T) {
	d := newDaemonForTest(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
		calls++
		if calls == 1 {
			d.maybeGenerateSessionTitle("sess-1", transcriptPath)
		}
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	if calls != 1 {
		t.Fatalf("exec calls = %d, want 1 (concurrent caller must not double-run the paid LLM call)", calls)
	}
	got := d.store.Get("sess-1")
	if got == nil || got.Label != "Fix login flow" {
		t.Fatalf("session label = %+v, want %q", got, "Fix login flow")
	}
}
