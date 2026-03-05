package wrapper

import (
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/hooks"
)

// GenerateSessionID generates a UUID for use as session ID
func GenerateSessionID() string {
	return uuid.New().String()
}

// DefaultLabel returns the current directory name as default label
func DefaultLabel() string {
	dir, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(dir)
}

// WriteSettingsConfig writes arbitrary settings content to a temporary file.
// Creates a subdirectory to isolate from other temp files (avoids fs.watch issues).
func WriteSettingsConfig(tmpDir, sessionID, content string) (string, error) {
	settingsDir := filepath.Join(tmpDir, "attn-hooks-"+sessionID)
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		return "", err
	}
	configPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		return "", err
	}
	return configPath, nil
}

// WriteHooksConfig writes a temporary hooks configuration file.
func WriteHooksConfig(tmpDir, sessionID, socketPath, wrapperPath string) (string, error) {
	content := hooks.Generate(sessionID, socketPath, wrapperPath)
	return WriteSettingsConfig(tmpDir, sessionID, content)
}

// CleanupHooksConfig removes the temporary hooks configuration and its directory
func CleanupHooksConfig(configPath string) {
	os.Remove(configPath)
	// Also remove the parent directory (attn-hooks-<sessionID>)
	os.Remove(filepath.Dir(configPath))
}
