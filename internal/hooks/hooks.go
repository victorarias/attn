package hooks

import (
	"encoding/json"
	"fmt"
)

// HookEntry is a single hook configuration
type HookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

// Hook is an individual hook action
type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// SettingsConfig represents Claude Code settings with hooks
type SettingsConfig struct {
	Hooks map[string][]HookEntry `json:"hooks"`
}

// Generate generates settings configuration with hooks for a session
func Generate(sessionID, socketPath string) string {
	config := SettingsConfig{
		Hooks: map[string][]HookEntry{
			"Stop": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`~/.local/bin/attn _hook-stop "%s"`, sessionID),
						},
					},
				},
			},
			"UserPromptSubmit": {
				{
					Matcher: "*",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"working"}' | nc -U %s`, sessionID, socketPath),
						},
					},
				},
			},
			"PostToolUse": {
				{
					Matcher: "TodoWrite",
					Hooks: []Hook{
						{
							Type:    "command",
							Command: fmt.Sprintf(`~/.local/bin/attn _hook-todo "%s"`, sessionID),
						},
					},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return string(data)
}

// GenerateUnregisterCommand generates the command to unregister a session
func GenerateUnregisterCommand(sessionID, socketPath string) string {
	return fmt.Sprintf(`echo '{"cmd":"unregister","id":"%s"}' | nc -U %s`, sessionID, socketPath)
}
