package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMediaTypeForPath(t *testing.T) {
	cases := map[string]string{
		"screenshot.png":  "image/png",
		"photo.jpg":       "image/jpeg",
		"photo.jpeg":      "image/jpeg",
		"PHOTO.JPG":       "image/jpeg",
		"anim.gif":        "image/gif",
		"pic.webp":        "image/webp",
		"/abs/path/x.PNG": "image/png",
	}
	for path, want := range cases {
		got, err := mediaTypeForPath(path)
		if err != nil {
			t.Errorf("mediaTypeForPath(%q) unexpected error: %v", path, err)
			continue
		}
		if got != want {
			t.Errorf("mediaTypeForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestMediaTypeForPathUnsupported(t *testing.T) {
	for _, path := range []string{"file.bmp", "file", "file.txt", "file.tiff"} {
		if _, err := mediaTypeForPath(path); err == nil {
			t.Errorf("mediaTypeForPath(%q) expected error, got nil", path)
		}
	}
}

func TestBuildVisionCheckMessage(t *testing.T) {
	line, err := buildVisionCheckMessage("what color is the button?", "image/png", "QUJD")
	if err != nil {
		t.Fatalf("buildVisionCheckMessage error: %v", err)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("buildVisionCheckMessage must return a single line, got: %q", line)
	}

	var decoded struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text,omitempty"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source,omitempty"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("failed to unmarshal built message: %v", err)
	}
	if decoded.Type != "user" {
		t.Errorf("type = %q, want %q", decoded.Type, "user")
	}
	if decoded.Message.Role != "user" {
		t.Errorf("message.role = %q, want %q", decoded.Message.Role, "user")
	}
	if len(decoded.Message.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(decoded.Message.Content))
	}
	if decoded.Message.Content[0].Type != "text" || decoded.Message.Content[0].Text != "what color is the button?" {
		t.Errorf("content[0] = %+v", decoded.Message.Content[0])
	}
	img := decoded.Message.Content[1]
	if img.Type != "image" || img.Source.Type != "base64" || img.Source.MediaType != "image/png" || img.Source.Data != "QUJD" {
		t.Errorf("content[1] = %+v", img)
	}
}

func TestParseVisionCheckResultLastEventWins(t *testing.T) {
	stdout := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"looking..."}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"first (should be overridden)","total_cost_usd":0.01,"num_turns":1}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"the button is blue","total_cost_usd":0.0234,"num_turns":1}`,
	}, "\n")

	r, err := parseVisionCheckResult(stdout)
	if err != nil {
		t.Fatalf("parseVisionCheckResult error: %v", err)
	}
	if r.Result != "the button is blue" {
		t.Errorf("Result = %q, want the last result event's text", r.Result)
	}
	if r.IsError {
		t.Errorf("IsError = true, want false")
	}
	if r.NumTurns != 1 {
		t.Errorf("NumTurns = %d, want 1", r.NumTurns)
	}
	if r.TotalCostUS != 0.0234 {
		t.Errorf("TotalCostUS = %v, want 0.0234", r.TotalCostUS)
	}
}

func TestParseVisionCheckResultIsErrorPassthrough(t *testing.T) {
	stdout := `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"hit max turns"}`
	r, err := parseVisionCheckResult(stdout)
	if err != nil {
		t.Fatalf("parseVisionCheckResult error: %v", err)
	}
	if !r.IsError {
		t.Errorf("IsError = false, want true")
	}
	if r.Subtype != "error_max_turns" {
		t.Errorf("Subtype = %q, want error_max_turns", r.Subtype)
	}
}

func TestParseVisionCheckResultMissingResultEvent(t *testing.T) {
	stdout := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[]}}`,
	}, "\n")
	if _, err := parseVisionCheckResult(stdout); err == nil {
		t.Fatal("expected error when no result event is present, got nil")
	}
}

func TestParseVisionCheckResultHugeLine(t *testing.T) {
	// Simulate an assistant line far larger than bufio.Scanner's default
	// 64KB token limit, to prove the reader doesn't choke on it.
	huge := strings.Repeat("x", 300*1024)
	stdout := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + huge + `"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","total_cost_usd":0.1,"num_turns":1}`,
	}, "\n")
	r, err := parseVisionCheckResult(stdout)
	if err != nil {
		t.Fatalf("parseVisionCheckResult error with huge line: %v", err)
	}
	if r.Result != "ok" {
		t.Errorf("Result = %q, want ok", r.Result)
	}
}

func TestParseVisionCheckArgs(t *testing.T) {
	image, question, model, timeout, jsonOut, err := parseVisionCheckArgs([]string{"shot.png", "what does this say?"})
	if err != nil {
		t.Fatalf("parseVisionCheckArgs error: %v", err)
	}
	if image != "shot.png" || question != "what does this say?" {
		t.Errorf("got image=%q question=%q", image, question)
	}
	if model != "sonnet" {
		t.Errorf("default model = %q, want sonnet", model)
	}
	if timeout.Seconds() != 120 {
		t.Errorf("default timeout = %v, want 120s", timeout)
	}
	if jsonOut {
		t.Errorf("default json = true, want false")
	}
}

func TestParseVisionCheckArgsMissingPositional(t *testing.T) {
	if _, _, _, _, _, err := parseVisionCheckArgs([]string{"shot.png"}); err == nil {
		t.Fatal("expected error for missing question argument")
	}
}
