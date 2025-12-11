package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/config"
)

// hookStopInput is the JSON input from Claude Code Stop hook
type hookStopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func runHookStop(sessionID string) error {
	// Read hook input from stdin
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var hookInput hookStopInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}

	// Send stop command to daemon
	c := client.New(config.SocketPath())
	return c.SendStop(sessionID, hookInput.TranscriptPath)
}
