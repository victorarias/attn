package hooks

import (
	"encoding/json"
	"fmt"
)

// HookConfig represents a Claude Code hooks configuration
type HookConfig struct {
	Hooks []HookEntry `json:"hooks"`
}

// HookEntry is a single hook configuration
type HookEntry struct {
	Matcher EventMatcher `json:"matcher"`
	Hooks   []Hook       `json:"hooks"`
}

// EventMatcher matches events
type EventMatcher struct {
	Event string `json:"event"`
	Tool  string `json:"tool,omitempty"`
}

// Hook is an individual hook action
type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// Generate generates hooks configuration for a session
func Generate(sessionID, socketPath string) string {
	config := HookConfig{
		Hooks: []HookEntry{
			{
				Matcher: EventMatcher{Event: "Stop"},
				Hooks: []Hook{
					{
						Type:    "command",
						Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"waiting"}' | nc -U %s`, sessionID, socketPath),
					},
				},
			},
			{
				Matcher: EventMatcher{Event: "UserPromptSubmit"},
				Hooks: []Hook{
					{
						Type:    "command",
						Command: fmt.Sprintf(`echo '{"cmd":"state","id":"%s","state":"working"}' | nc -U %s`, sessionID, socketPath),
					},
				},
			},
			{
				Matcher: EventMatcher{Event: "PostToolUse", Tool: "TodoWrite"},
				Hooks: []Hook{
					{
						Type:    "command",
						Command: fmt.Sprintf(`cm _hook-todo "%s"`, sessionID),
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
