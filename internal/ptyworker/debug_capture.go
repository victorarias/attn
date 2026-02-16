package ptyworker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	debugCaptureSubscriberID = "__attn_debug_capture__"
	debugCaptureWindow       = 90 * time.Second
	debugCaptureMaxEvents    = 4096
	debugCaptureChunkLimit   = 4096
	debugCaptureDumpCooldown = 2 * time.Second
)

type debugCapture struct {
	sessionID string
	agent     string
	dir       string
	logf      func(format string, args ...interface{})

	window    time.Duration
	maxEvents int

	mu         sync.Mutex
	events     []debugCaptureEvent
	lastDumpAt time.Time
}

type debugCaptureEvent struct {
	Time           time.Time `json:"time"`
	Kind           string    `json:"kind"`
	Seq            *uint32   `json:"seq,omitempty"`
	State          string    `json:"state,omitempty"`
	DataB64        string    `json:"data_b64,omitempty"`
	TruncatedBytes int       `json:"truncated_bytes,omitempty"`
	Bytes          int       `json:"bytes,omitempty"`
	Note           string    `json:"note,omitempty"`
}

func newDebugCapture(cfg Config, logf func(format string, args ...interface{})) *debugCapture {
	if !shouldEnableDebugCapture(cfg.Agent) {
		return nil
	}
	if cfg.RegistryPath == "" {
		return nil
	}

	baseDir := filepath.Dir(filepath.Dir(cfg.RegistryPath))
	dir := filepath.Join(baseDir, "captures")
	if err := os.MkdirAll(dir, 0700); err != nil {
		if logf != nil {
			logf("worker debug capture disabled: create dir failed path=%s err=%v", dir, err)
		}
		return nil
	}

	return &debugCapture{
		sessionID: cfg.SessionID,
		agent:     strings.TrimSpace(strings.ToLower(cfg.Agent)),
		dir:       dir,
		logf:      logf,
		window:    debugCaptureWindow,
		maxEvents: debugCaptureMaxEvents,
		events:    make([]debugCaptureEvent, 0, 256),
	}
}

func shouldEnableDebugCapture(agent string) bool {
	normalized := strings.TrimSpace(strings.ToLower(agent))
	if normalized == "" {
		return false
	}

	raw := strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_DEBUG_PTY_CAPTURE")))
	switch raw {
	case "0", "false", "off", "no", "disabled":
		return false
	case "1", "true", "on", "yes", "all":
		return true
	case "codex":
		return normalized == "codex"
	case "copilot":
		return normalized == "copilot"
	case "":
		// Temporary debugging default: capture codex sessions without extra setup.
		return normalized == "codex"
	default:
		return normalized == "codex"
	}
}

func (c *debugCapture) recordState(state string) {
	state = strings.TrimSpace(state)
	if c == nil || state == "" {
		return
	}
	c.append(debugCaptureEvent{
		Time:  time.Now().UTC(),
		Kind:  "state",
		State: state,
	})
}

func (c *debugCapture) recordOutput(seq uint32, data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	chunk := data
	truncated := 0
	if len(chunk) > debugCaptureChunkLimit {
		truncated = len(chunk) - debugCaptureChunkLimit
		chunk = chunk[:debugCaptureChunkLimit]
	}
	seqCopy := seq
	c.append(debugCaptureEvent{
		Time:           time.Now().UTC(),
		Kind:           "output",
		Seq:            &seqCopy,
		DataB64:        base64.StdEncoding.EncodeToString(chunk),
		TruncatedBytes: truncated,
		Bytes:          len(data),
	})
}

func (c *debugCapture) recordInput(data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	chunk := data
	truncated := 0
	if len(chunk) > debugCaptureChunkLimit {
		truncated = len(chunk) - debugCaptureChunkLimit
		chunk = chunk[:debugCaptureChunkLimit]
	}
	c.append(debugCaptureEvent{
		Time:           time.Now().UTC(),
		Kind:           "input",
		DataB64:        base64.StdEncoding.EncodeToString(chunk),
		TruncatedBytes: truncated,
		Bytes:          len(data),
	})
}

func (c *debugCapture) recordNote(note string) {
	note = strings.TrimSpace(note)
	if c == nil || note == "" {
		return
	}
	c.append(debugCaptureEvent{
		Time: time.Now().UTC(),
		Kind: "note",
		Note: note,
	})
}

func (c *debugCapture) append(evt debugCaptureEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.events = append(c.events, evt)
	c.pruneLocked(evt.Time)
}

func (c *debugCapture) pruneLocked(now time.Time) {
	if c.window > 0 {
		cutoff := now.Add(-c.window)
		first := 0
		for first < len(c.events) && c.events[first].Time.Before(cutoff) {
			first++
		}
		if first > 0 {
			c.events = append([]debugCaptureEvent(nil), c.events[first:]...)
		}
	}
	if c.maxEvents > 0 && len(c.events) > c.maxEvents {
		c.events = append([]debugCaptureEvent(nil), c.events[len(c.events)-c.maxEvents:]...)
	}
}

func (c *debugCapture) dump(reason string) (string, error) {
	if c == nil {
		return "", nil
	}

	now := time.Now().UTC()
	reason = sanitizeCaptureReason(reason)
	if reason == "" {
		reason = "manual"
	}

	c.mu.Lock()
	if now.Sub(c.lastDumpAt) < debugCaptureDumpCooldown {
		c.mu.Unlock()
		return "", nil
	}
	c.lastDumpAt = now
	events := append([]debugCaptureEvent(nil), c.events...)
	c.mu.Unlock()

	if len(events) == 0 {
		return "", nil
	}

	name := fmt.Sprintf(
		"%s-%s-%s.jsonl",
		sanitizeCaptureReason(c.sessionID),
		now.Format("20060102T150405.000000000Z"),
		reason,
	)
	path := filepath.Join(c.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	meta := map[string]any{
		"time":           now,
		"kind":           "meta",
		"session_id":     c.sessionID,
		"agent":          c.agent,
		"reason":         reason,
		"window_seconds": int(c.window.Seconds()),
		"event_count":    len(events),
	}
	if err := enc.Encode(meta); err != nil {
		return "", err
	}
	for i := range events {
		if err := enc.Encode(events[i]); err != nil {
			return "", err
		}
	}
	return path, nil
}

func sanitizeCaptureReason(input string) string {
	value := strings.TrimSpace(strings.ToLower(input))
	if value == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range value {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out.WriteRune(r)
		case r == '_' || r == '-':
			out.WriteRune(r)
		default:
			out.WriteRune('_')
		}
	}
	return strings.Trim(out.String(), "_")
}

func isWorkingToStopTransition(previousState, nextState string) bool {
	return previousState == "working" && (nextState == "waiting_input" || nextState == "idle")
}
