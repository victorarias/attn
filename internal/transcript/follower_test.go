package transcript

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFollowerTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexFollowerEventMessage(text string) string {
	return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"agent_message","message":%q}}`, text)
}

func codexFollowerResponseMessage(text string) string {
	return fmt.Sprintf(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`, text)
}

func TestFollowerReadsCompleteRecordsOnceAndWaitsForPartialFinalRecord(t *testing.T) {
	path := writeFollowerTranscript(t, codexFollowerEventMessage("first"))
	follower, err := NewFollower(path, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}

	first, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].Text != "first" || first.Events[0].Cursor == "" {
		t.Fatalf("first batch = %+v", first)
	}
	if again, err := follower.Read(); err != nil || len(again.Records) != 0 {
		t.Fatalf("unchanged read = %+v, %v", again, err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(codexFollowerEventMessage("second")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if partial, err := follower.Read(); err != nil || len(partial.Records) != 0 {
		t.Fatalf("partial read = %+v, %v", partial, err)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("\n")
	_ = f.Close()
	completed, err := follower.Read()
	if err != nil || len(completed.Events) != 1 || completed.Events[0].Text != "second" {
		t.Fatalf("completed read = %+v, %v", completed, err)
	}
}

func TestFollowerDeduplicatesCodexPairedAssistantRecords(t *testing.T) {
	path := writeFollowerTranscript(t, codexFollowerEventMessage("same answer"), codexFollowerResponseMessage("same answer"))
	follower, err := NewFollower(path, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Records) != 2 || len(batch.Events) != 1 || batch.Events[0].Text != "same answer" {
		t.Fatalf("batch = %+v", batch)
	}
}

func TestFollowerRejectsTranscriptReplacement(t *testing.T) {
	path := writeFollowerTranscript(t, codexFollowerEventMessage("original"))
	follower, err := NewFollower(path, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := follower.Read(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(codexFollowerEventMessage("replacement with a longer first record")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := follower.Read(); !errors.Is(err, ErrCursorMismatch) {
		t.Fatalf("Read error = %v, want ErrCursorMismatch", err)
	}
}

func TestAssistantWindowKeepsDistinctIdenticalMessagesByCursor(t *testing.T) {
	window := NewAssistantWindow(AssistantWindowLimits{MaxMessages: 32, MaxMessageChars: 1024, MaxTotalChars: 4096})
	if !window.Apply([]Event{
		{Cursor: "cursor-1", Kind: EventKindAssistant, Text: "same answer"},
		{Cursor: "cursor-2", Kind: EventKindAssistant, Text: "same answer"},
	}) {
		t.Fatal("Apply reported no change")
	}
	messages, report := window.Snapshot()
	if len(messages) != 2 || messages[0].Key == messages[1].Key || report.Truncated() {
		t.Fatalf("snapshot = %+v report=%+v", messages, report)
	}
}

func TestAssistantWindowCapsNewestMessagesAndDropsOversizeWhole(t *testing.T) {
	window := NewAssistantWindow(AssistantWindowLimits{MaxMessages: 2, MaxMessageChars: 5, MaxTotalChars: 8})
	window.Apply([]Event{
		{Cursor: "1", Kind: EventKindAssistant, Text: "one"},
		{Cursor: "2", Kind: EventKindAssistant, Text: "oversize"},
		{Cursor: "3", Kind: EventKindAssistant, Text: "two"},
		{Cursor: "4", Kind: EventKindAssistant, Text: "three"},
	})
	messages, report := window.Snapshot()
	if len(messages) != 2 || messages[0].Content != "two" || messages[1].Content != "three" {
		t.Fatalf("messages = %+v", messages)
	}
	if report.DroppedOversize != 1 || report.LargestDropped != 8 || report.DroppedOld != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestAssistantWindowReportsAnOmittedBootstrapPrefix(t *testing.T) {
	window := NewAssistantWindow(AssistantWindowLimits{})
	window.MarkPrefixOmitted()
	_, report := window.Snapshot()
	if !report.OmittedPrefix || !report.Truncated() {
		t.Fatalf("report = %+v", report)
	}
}
