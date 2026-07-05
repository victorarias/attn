package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Synthetic JSONL line shapes used to build a realistic-mix transcript
// fixture without committing a multi-MB blob. Built via encoding/json
// (never hand-concatenated strings), so every generated line is guaranteed
// valid JSON.

type benchContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type benchAssistantEntry struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string              `json:"role"`
		Content []benchContentBlock `json:"content"`
	} `json:"message"`
}

type benchUserEntry struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

type benchCodexEventMsg struct {
	Type    string `json:"type"`
	Payload struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"payload"`
}

// benchAssistantText is a few hundred chars, roughly what a real assistant
// turn looks like.
const benchAssistantText = "I've reviewed the change and it looks correct. The fix addresses the " +
	"root cause rather than papering over the symptom, and the added test exercises the failing " +
	"path directly. One small note: consider extracting the retry helper if a third caller shows " +
	"up, but that can wait for a follow-up PR. Ready to merge once CI is green."

func marshalLine(tb testing.TB, v interface{}) []byte {
	tb.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		tb.Fatal(err)
	}
	return append(data, '\n')
}

func assistantTextLine(tb testing.TB, index int, ts time.Time) []byte {
	var e benchAssistantEntry
	e.Type = "assistant"
	e.UUID = fmt.Sprintf("uuid-assistant-text-%d", index)
	e.Timestamp = ts.Format(time.RFC3339Nano)
	e.Message.Role = "assistant"
	e.Message.Content = []benchContentBlock{{Type: "text", Text: benchAssistantText}}
	return marshalLine(tb, e)
}

func userLine(tb testing.TB, index int, ts time.Time) []byte {
	var e benchUserEntry
	e.Type = "user"
	e.UUID = fmt.Sprintf("uuid-user-%d", index)
	e.Message.Role = "user"
	e.Message.Content = fmt.Sprintf("Please take a look at issue #%d and let me know what you think.", index)
	return marshalLine(tb, e)
}

// assistantToolUseLine is a tool_use-style content block with no text block,
// forcing extractTextContent to walk the block array and come up empty for
// this line (realistic: tool-call entries don't carry assistant prose).
func assistantToolUseLine(tb testing.TB, index int, ts time.Time) []byte {
	var e benchAssistantEntry
	e.Type = "assistant"
	e.UUID = fmt.Sprintf("uuid-tool-%d", index)
	e.Timestamp = ts.Format(time.RFC3339Nano)
	e.Message.Role = "assistant"
	e.Message.Content = []benchContentBlock{
		{Type: "tool_use", ID: fmt.Sprintf("toolu_%d", index), Name: "Read", Input: json.RawMessage(`{"file_path":"/tmp/example.go"}`)},
	}
	return marshalLine(tb, e)
}

func codexAgentMessageLine(tb testing.TB, index int) []byte {
	var e benchCodexEventMsg
	e.Type = "event_msg"
	e.Payload.Type = "agent_message"
	e.Payload.Message = fmt.Sprintf("Working on task %d now.", index)
	return marshalLine(tb, e)
}

// writeSyntheticTranscript writes a deterministic, realistic-shaped JSONL
// transcript to dir, cycling through a Claude assistant text entry, a Claude
// user entry, a tool_use-style assistant entry (content-block array with a
// non-text block), and a codex event_msg agent_message line. It always
// finishes with an assistant text line after the last user line, so
// ExtractLastAssistantTurnAfterLastUserSince exercises the full extract path
// instead of taking the early empty-turn return ("last user has no
// subsequent assistant yet").
func writeSyntheticTranscript(tb testing.TB, dir string, numLines int) (path string, fileSize int64) {
	tb.Helper()
	path = filepath.Join(dir, "transcript.jsonl")
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}

	base := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	for i := 0; i < numLines; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		var line []byte
		switch i % 4 {
		case 0:
			line = assistantTextLine(tb, i, ts)
		case 1:
			line = userLine(tb, i, ts)
		case 2:
			line = assistantToolUseLine(tb, i, ts)
		case 3:
			line = codexAgentMessageLine(tb, i)
		}
		if _, err := f.Write(line); err != nil {
			tb.Fatal(err)
		}
	}
	// Always finish on a real assistant text line after the last user line.
	finalTS := base.Add(time.Duration(numLines) * time.Second)
	if _, err := f.Write(assistantTextLine(tb, numLines, finalTS)); err != nil {
		tb.Fatal(err)
	}

	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		tb.Fatal(err)
	}
	return path, info.Size()
}

// TestSyntheticTranscriptFixtureIsRealistic guards the benchmark fixture's
// key assumption: it must end with real assistant content after the last
// user line, not an early empty AssistantTurn{}. If a future edit to the
// generator (or to ExtractLastAssistantTurnAfterLastUserSince) breaks that,
// the benchmarks below would silently start measuring the cheap early-return
// path instead of the full parse — this test catches that.
func TestSyntheticTranscriptFixtureIsRealistic(t *testing.T) {
	dir := t.TempDir()
	path, size := writeSyntheticTranscript(t, dir, 40)
	if size == 0 {
		t.Fatal("expected non-empty fixture file")
	}
	turn, err := ExtractLastAssistantTurnAfterLastUserSince(path, 2000, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Content != benchAssistantText {
		t.Fatalf("expected fixture to end on the final assistant text line, got %q", turn.Content)
	}
}

// benchExtractLastAssistantTurn benchmarks the once-per-assistant-turn
// transcript parse, which today re-reads the whole file from offset 0 and
// runs isUserEntry + ExtractAssistantContent (several json.Unmarshal calls
// each) per line. Parameterizing by line count makes the O(file-size)
// scaling visible.
func benchExtractLastAssistantTurn(b *testing.B, numLines int) {
	dir := b.TempDir()
	path, fileSize := writeSyntheticTranscript(b, dir, numLines)

	// Sanity check, outside the timed loop: confirm the fixture drives the
	// full extract path and returns real content rather than the early
	// empty-turn return, so the benchmark below measures real work.
	turn, err := ExtractLastAssistantTurnAfterLastUserSince(path, 2000, time.Time{})
	if err != nil {
		b.Fatal(err)
	}
	if turn.Content == "" {
		b.Fatal("sanity check failed: fixture produced an empty assistant turn")
	}

	b.ReportAllocs()
	b.SetBytes(fileSize)
	b.ResetTimer()
	// ReportMetric must come after ResetTimer: ResetTimer clears any custom
	// metrics recorded before it.
	b.ReportMetric(float64(fileSize), "fixture_bytes")
	for i := 0; i < b.N; i++ {
		if _, err := ExtractLastAssistantTurnAfterLastUserSince(path, 2000, time.Time{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractLastAssistantTurn_1kLines(b *testing.B) {
	benchExtractLastAssistantTurn(b, 1000)
}
func BenchmarkExtractLastAssistantTurn_10kLines(b *testing.B) {
	benchExtractLastAssistantTurn(b, 10000)
}
func BenchmarkExtractLastAssistantTurn_50kLines(b *testing.B) {
	benchExtractLastAssistantTurn(b, 50000)
}
