package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeAssistant(uuid, text string) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, uuid, text)
}

func codexAssistant(text string) string {
	return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"agent_message","message":%q}}`, text)
}

func contents(messages []AssistantMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Content)
	}
	return out
}

var generous = RecentAssistantMessagesLimits{MaxMessages: 32, MaxMessageChars: 64 * 1024, MaxTotalChars: 256 * 1024}

func TestReadRecentAssistantMessages_KeepsEveryTurnOldestFirst(t *testing.T) {
	path := writeTranscript(t,
		claudeAssistant("u1", "first answer"),
		`{"type":"user","message":{"role":"user","content":"another"}}`,
		claudeAssistant("u2", "second answer"),
	)

	messages, report, err := ReadRecentAssistantMessages(path, generous)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := contents(messages), []string{"first answer", "second answer"}; !sameStrings(got, want) {
		t.Errorf("messages = %q, want %q", got, want)
	}
	if report.Truncated() {
		t.Error("truncated for a two-message transcript")
	}
}

func TestReadRecentAssistantMessages_KeyPrefersTheEntryID(t *testing.T) {
	// The entry id survives anything that could rewrite the text around it,
	// which a content hash would not.
	path := writeTranscript(t, claudeAssistant("entry-uuid", "an answer"))

	messages, _, err := ReadRecentAssistantMessages(path, generous)
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 1 || messages[0].Key != "entry-uuid" {
		t.Fatalf("key = %+v, want the transcript entry's own id", messages)
	}
}

func TestReadRecentAssistantMessages_KeyFallsBackToContentHash(t *testing.T) {
	// Codex and copilot transcripts carry no entry id. Identical text then
	// shares a key, which is the honest answer: nothing distinguishes them.
	path := writeTranscript(t, codexAssistant("an answer"), codexAssistant("a different answer"))

	messages, _, err := ReadRecentAssistantMessages(path, generous)
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	for _, message := range messages {
		if message.Key == "" {
			t.Fatalf("empty key for %q", message.Content)
		}
	}
	if messages[0].Key == messages[1].Key {
		t.Error("different content produced the same key")
	}
}

func TestReadRecentAssistantMessages_DropsAnOversizeMessageWholeAndReportsIt(t *testing.T) {
	// Truncating would keep the message but move every offset past the cut,
	// which turns a stored annotation into a quote of the wrong words.
	huge := strings.Repeat("x", 101)
	path := writeTranscript(t, claudeAssistant("u1", "small"), claudeAssistant("u2", huge))

	messages, report, err := ReadRecentAssistantMessages(path, RecentAssistantMessagesLimits{MaxMessageChars: 100})
	if err != nil {
		t.Fatal(err)
	}

	if got := contents(messages); !sameStrings(got, []string{"small"}) {
		t.Errorf("messages = %q, want only the in-budget one", got)
	}
	if report.DroppedOversize != 1 || report.LargestDropped != 101 {
		t.Errorf("report = %+v, want one drop of 101 chars named", report)
	}
	if !report.Truncated() {
		t.Error("truncated = false after dropping a message")
	}
}

func TestReadRecentAssistantMessages_CountCapKeepsTheNewest(t *testing.T) {
	lines := make([]string, 5)
	for i := range lines {
		lines[i] = claudeAssistant(fmt.Sprintf("u%d", i), fmt.Sprintf("answer %d", i))
	}
	path := writeTranscript(t, lines...)

	messages, report, err := ReadRecentAssistantMessages(path, RecentAssistantMessagesLimits{MaxMessages: 2})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := contents(messages), []string{"answer 3", "answer 4"}; !sameStrings(got, want) {
		t.Errorf("messages = %q, want %q", got, want)
	}
	if report.DroppedOld != 3 {
		t.Errorf("dropped old = %d, want 3", report.DroppedOld)
	}
}

func TestReadRecentAssistantMessages_ByteBudgetKeepsTheNewest(t *testing.T) {
	path := writeTranscript(t,
		claudeAssistant("u1", strings.Repeat("a", 60)),
		claudeAssistant("u2", strings.Repeat("b", 60)),
	)

	messages, report, err := ReadRecentAssistantMessages(path, RecentAssistantMessagesLimits{MaxTotalChars: 100})
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 1 || !strings.HasPrefix(messages[0].Content, "b") {
		t.Errorf("messages = %q, want only the newest within budget", contents(messages))
	}
	if report.DroppedOld != 1 {
		t.Errorf("dropped old = %d, want 1", report.DroppedOld)
	}
}

func TestReadRecentAssistantMessages_MissingTranscriptIsAnError(t *testing.T) {
	if _, _, err := ReadRecentAssistantMessages(filepath.Join(t.TempDir(), "nope.jsonl"), generous); err == nil {
		t.Error("reading a missing transcript succeeded")
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
