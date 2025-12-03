package wrapper

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/victorarias/claude-manager/internal/hooks"
)

// GenerateSessionID generates a unique session ID
func GenerateSessionID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// DefaultLabel returns the current directory name as default label
func DefaultLabel() string {
	dir, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(dir)
}

// GetTmuxTarget returns the current tmux pane location
func GetTmuxTarget() string {
	// This will be implemented to shell out to tmux
	// For now return empty string if not in tmux
	if os.Getenv("TMUX") == "" {
		return ""
	}
	return "" // Will implement with actual tmux command
}

// WriteHooksConfig writes a temporary hooks configuration file
func WriteHooksConfig(tmpDir, sessionID, socketPath string) (string, error) {
	configPath := filepath.Join(tmpDir, "claude-hooks-"+sessionID+".json")

	content := hooks.Generate(sessionID, socketPath)

	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		return "", err
	}

	return configPath, nil
}

// CleanupHooksConfig removes the temporary hooks configuration
func CleanupHooksConfig(configPath string) {
	os.Remove(configPath)
}
