package transcript

import (
	"bufio"
	"encoding/json"
	"os"
)

// transcriptEntry represents a single entry in the JSONL transcript
type transcriptEntry struct {
	Type    string `json:"type"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// ExtractLastAssistantMessage reads a JSONL transcript and returns
// the last N characters of the last assistant message.
func ExtractLastAssistantMessage(path string, maxChars int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lastAssistantContent string
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry transcriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // Skip malformed lines
		}

		if entry.Type == "assistant" && entry.Message.Content != "" {
			lastAssistantContent = entry.Message.Content
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// Truncate to last maxChars
	if len(lastAssistantContent) > maxChars {
		lastAssistantContent = lastAssistantContent[len(lastAssistantContent)-maxChars:]
	}

	return lastAssistantContent, nil
}
