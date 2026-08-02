package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// AssistantMessage is one assistant message from a transcript, carrying the
// identity annotations are persisted against.
type AssistantMessage struct {
	// Key is stable across re-reads of the same transcript: the entry's own id
	// when the agent's format has one, otherwise a hash of the content. An
	// annotation stored against this key must still find its message tomorrow.
	Key     string
	Content string
}

// RecentAssistantMessagesLimits bounds what a window read will return.
type RecentAssistantMessagesLimits struct {
	// MaxMessages caps how many messages come back, newest kept.
	MaxMessages int
	// MaxMessageChars drops any single message longer than this. A message is
	// dropped rather than truncated because annotation offsets address the whole
	// text: half a message would silently re-point every offset past the cut.
	MaxMessageChars int
	// MaxTotalChars caps the window's combined size, dropping oldest first.
	MaxTotalChars int
}

// RecentAssistantMessagesReport says what a read left out, so a caller can say
// so instead of presenting a silently short window.
type RecentAssistantMessagesReport struct {
	// DroppedOversize is how many messages exceeded MaxMessageChars, and
	// LargestDropped is the biggest one's length — the value to compare the cap
	// against when deciding whether the cap is wrong.
	DroppedOversize int
	LargestDropped  int
	// DroppedOld is how many older messages fell outside the count or byte
	// budget.
	DroppedOld int
}

// Truncated reports whether anything was left out.
func (r RecentAssistantMessagesReport) Truncated() bool {
	return r.DroppedOversize > 0 || r.DroppedOld > 0
}

// ReadRecentAssistantMessages returns the transcript's most recent assistant
// messages, oldest first, within limits.
//
// One JSONL entry with prose is one message, which is also how the agents print
// them — so a message here is the same unit the user sees as one block on the
// terminal, and can annotate as one.
func ReadRecentAssistantMessages(path string, limits RecentAssistantMessagesLimits) ([]AssistantMessage, RecentAssistantMessagesReport, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, RecentAssistantMessagesReport{}, err
	}
	defer file.Close()

	var (
		messages []AssistantMessage
		report   RecentAssistantMessagesReport
	)
	if err := readJSONLLines(file, func(line []byte) {
		content := ExtractAssistantContent(line)
		if content == "" {
			return
		}
		if limits.MaxMessageChars > 0 && len(content) > limits.MaxMessageChars {
			report.DroppedOversize++
			if len(content) > report.LargestDropped {
				report.LargestDropped = len(content)
			}
			return
		}
		messages = append(messages, AssistantMessage{
			Key:     messageKey(extractLineUUID(line), content),
			Content: content,
		})
	}); err != nil {
		return nil, RecentAssistantMessagesReport{}, err
	}

	if limits.MaxMessages > 0 && len(messages) > limits.MaxMessages {
		report.DroppedOld += len(messages) - limits.MaxMessages
		messages = messages[len(messages)-limits.MaxMessages:]
	}
	if limits.MaxTotalChars > 0 {
		total := 0
		keepFrom := len(messages)
		for i := len(messages) - 1; i >= 0; i-- {
			if total+len(messages[i].Content) > limits.MaxTotalChars {
				break
			}
			total += len(messages[i].Content)
			keepFrom = i
		}
		report.DroppedOld += keepFrom
		messages = messages[keepFrom:]
	}
	return messages, report, nil
}

// messageKey prefers the transcript entry's own id, which survives anything
// that could rewrite the text. Formats without one (codex, copilot) fall back
// to a content hash: stable for as long as the message is, which is what an
// append-only transcript guarantees.
func messageKey(uuid, content string) string {
	if uuid != "" {
		return uuid
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:16])
}
