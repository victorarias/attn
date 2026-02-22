package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FindCodexTranscript searches Codex session logs for the most recent session
// matching the given cwd and start time. Returns empty string if not found.
func FindCodexTranscript(cwd string, startedAt time.Time) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sessionsDir := filepath.Join(homeDir, ".codex", "sessions")

	type codexLine struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}

	var bestPath string
	var bestTime time.Time
	var fallbackPath string
	var fallbackModTime time.Time
	cwdClean := filepath.Clean(cwd)

	filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		// Skip files that are too old to be relevant.
		if info.ModTime().Before(startedAt.Add(-5 * time.Minute)) {
			return nil
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}

		reader := bufio.NewReader(f)
		line, readErr := reader.ReadBytes('\n')
		_ = f.Close()
		if readErr != nil && len(line) == 0 {
			return nil
		}

		var entry codexLine
		if json.Unmarshal(bytes.TrimSpace(line), &entry) != nil {
			return nil
		}
		if entry.Type != "session_meta" {
			return nil
		}

		entryCwd := filepath.Clean(entry.Payload.Cwd)
		if entryCwd != cwdClean {
			return nil
		}

		ts := entry.Payload.Timestamp
		if ts == "" {
			ts = entry.Timestamp
		}
		if ts == "" {
			return nil
		}

		sessionTime, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr == nil && !sessionTime.Before(startedAt.Add(-5*time.Minute)) {
			if bestPath == "" || sessionTime.After(bestTime) {
				bestPath = path
				bestTime = sessionTime
			}
			return nil
		}

		// Fallback for resumed/continued sessions where session_meta timestamp is old
		// but the transcript file is still actively written.
		if fallbackPath == "" || info.ModTime().After(fallbackModTime) {
			fallbackPath = path
			fallbackModTime = info.ModTime()
		}

		return nil
	})

	if bestPath != "" {
		return bestPath
	}
	return fallbackPath
}

func readCopilotWorkspaceCWD(workspacePath string) string {
	data, err := os.ReadFile(workspacePath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cwd: ") {
			continue
		}
		return filepath.Clean(strings.TrimSpace(strings.TrimPrefix(line, "cwd: ")))
	}
	return ""
}

type copilotEventEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type copilotSessionStartData struct {
	StartTime string `json:"startTime"`
}

type copilotEventMeta struct {
	StartTime           time.Time
	HasStartTime        bool
	HasAssistantMessage bool
}

func readCopilotEventMeta(eventsPath string) copilotEventMeta {
	f, err := os.Open(eventsPath)
	if err != nil {
		return copilotEventMeta{}
	}
	defer f.Close()

	meta := copilotEventMeta{}
	_ = readJSONLLines(f, func(line []byte) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}

		var evt copilotEventEnvelope
		if err := json.Unmarshal(line, &evt); err != nil {
			return
		}

		switch evt.Type {
		case "session.start":
			var data copilotSessionStartData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				return
			}
			if data.StartTime == "" {
				return
			}
			ts, parseErr := time.Parse(time.RFC3339Nano, data.StartTime)
			if parseErr != nil {
				return
			}
			meta.StartTime = ts
			meta.HasStartTime = true
		case "assistant.message":
			meta.HasAssistantMessage = true
		}
	})

	return meta
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// FindCopilotTranscript searches Copilot session-state for the most recently
// active events stream matching cwd and launch timing.
func FindCopilotTranscript(cwd string, startedAt time.Time) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sessionsDir := filepath.Join(homeDir, ".copilot", "session-state")
	cwdClean := filepath.Clean(cwd)
	cutoff := startedAt.Add(-5 * time.Minute)

	var bestPath string
	var bestModTime time.Time
	bestRank := 10
	bestDelta := time.Duration(1<<63 - 1)

	filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == sessionsDir {
			return nil
		}

		workspacePath := filepath.Join(path, "workspace.yaml")
		eventsPath := filepath.Join(path, "events.jsonl")
		if _, statErr := os.Stat(eventsPath); statErr != nil {
			return filepath.SkipDir
		}

		matchedCWD := readCopilotWorkspaceCWD(workspacePath)
		if matchedCWD == "" || matchedCWD != cwdClean {
			return filepath.SkipDir
		}

		info, statErr := os.Stat(eventsPath)
		if statErr != nil {
			return filepath.SkipDir
		}
		modTime := info.ModTime()
		if modTime.Before(cutoff) {
			return filepath.SkipDir
		}

		meta := readCopilotEventMeta(eventsPath)
		rank := 1
		delta := time.Duration(1<<63 - 1)

		if meta.HasStartTime {
			startWindowMin := startedAt.Add(-10 * time.Minute)
			startWindowMax := startedAt.Add(2 * time.Minute)
			if !meta.StartTime.Before(startWindowMin) && !meta.StartTime.After(startWindowMax) {
				rank = 0
				delta = absDuration(meta.StartTime.Sub(startedAt))
			}
		}
		if !meta.HasAssistantMessage {
			rank++
		}

		if bestPath == "" {
			bestPath = eventsPath
			bestModTime = modTime
			bestRank = rank
			bestDelta = delta
			return filepath.SkipDir
		}
		if rank < bestRank {
			bestPath = eventsPath
			bestModTime = modTime
			bestRank = rank
			bestDelta = delta
			return filepath.SkipDir
		}
		if rank == bestRank {
			if rank == 0 {
				if delta < bestDelta || (delta == bestDelta && modTime.After(bestModTime)) {
					bestPath = eventsPath
					bestModTime = modTime
					bestDelta = delta
				}
			} else if modTime.After(bestModTime) {
				bestPath = eventsPath
				bestModTime = modTime
				bestDelta = delta
			}
		}

		return filepath.SkipDir
	})

	return bestPath
}

// FindCopilotTranscriptForResume resolves a Copilot resume ID to a transcript path.
// Resume IDs are directory names under ~/.copilot/session-state.
func FindCopilotTranscriptForResume(resumeID string) string {
	if strings.TrimSpace(resumeID) == "" {
		return ""
	}

	// Resume IDs are directory names; reject path traversal / separators.
	if strings.Contains(resumeID, "/") || strings.Contains(resumeID, "\\") || strings.Contains(resumeID, "..") {
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(homeDir, ".copilot", "session-state", resumeID, "events.jsonl")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// FindClaudeTranscript searches Claude project directories for a transcript
// file matching the session ID. Returns empty string if not found.
func FindClaudeTranscript(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	transcriptName := sessionID + ".jsonl"
	var found string

	filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == projectsDir {
			return nil
		}

		transcriptPath := filepath.Join(path, transcriptName)
		if _, statErr := os.Stat(transcriptPath); statErr == nil {
			found = transcriptPath
			return filepath.SkipAll
		}

		// Claude stores transcript files directly under each project dir.
		return filepath.SkipDir
	})

	return found
}
