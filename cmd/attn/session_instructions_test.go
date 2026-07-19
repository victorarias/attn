package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseSessionInstructionsArgs(t *testing.T) {
	got, err := parseSessionInstructionsArgs([]string{"target", "--question", "Was it authorized?", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if got.target != "target" || got.question != "Was it authorized?" || !got.json {
		t.Fatalf("got %+v", got)
	}
	for _, args := range [][]string{nil, {"--question", "x"}, {"target"}, {"target", "--question", " "}, {"target", "extra", "--question", "x"}} {
		if _, err := parseSessionInstructionsArgs(args); err == nil {
			t.Fatalf("parseSessionInstructionsArgs(%v) succeeded", args)
		}
	}
}

func TestParseSessionTranscriptArgs(t *testing.T) {
	got, err := parseSessionTranscriptArgs([]string{"target", "--after", "v1:abc:10:0", "--follow", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if got.target != "target" || got.after != "v1:abc:10:0" || !got.follow || !got.json {
		t.Fatalf("got %+v", got)
	}
	for _, args := range [][]string{nil, {"--follow"}, {"target", "extra"}} {
		if _, err := parseSessionTranscriptArgs(args); err == nil {
			t.Fatalf("parseSessionTranscriptArgs(%v) succeeded", args)
		}
	}
}

func TestStreamSessionTranscriptPaginatesWithoutDuplicates(t *testing.T) {
	pages := []*protocol.SessionTranscriptResult{
		{SessionID: "target", Events: []protocol.SessionTranscriptEvent{{Cursor: "cursor-1", Kind: "user", Text: protocol.Ptr("one")}}, NextCursor: "cursor-1", AtEnd: false},
		{SessionID: "target", Events: []protocol.SessionTranscriptEvent{{Cursor: "cursor-2", Kind: "assistant", Text: protocol.Ptr("two")}}, NextCursor: "cursor-2", AtEnd: true},
	}
	var cursors []string
	fetch := func(_ string, cursor string) (*protocol.SessionTranscriptResult, error) {
		cursors = append(cursors, cursor)
		page := pages[len(cursors)-1]
		return page, nil
	}
	var output bytes.Buffer
	cursor, err := streamSessionTranscript(context.Background(), &output, sessionTranscriptArgs{target: "target", json: true}, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "cursor-2" || len(cursors) != 2 || cursors[0] != "" || cursors[1] != "cursor-1" {
		t.Fatalf("cursor=%q calls=%v", cursor, cursors)
	}
	if strings.Count(output.String(), `"kind"`) != 2 || strings.Count(output.String(), `"one"`) != 1 || strings.Count(output.String(), `"two"`) != 1 {
		t.Fatalf("output=%q", output.String())
	}
}

func TestSessionTranscriptErrorCodeAndRendering(t *testing.T) {
	if got := sessionTranscriptErrorCode(errors.New("daemon error: cursor_mismatch")); got != "cursor_mismatch" {
		t.Fatalf("code=%q", got)
	}
	if got := sessionTranscriptErrorCode(errors.New("socket unavailable")); got != "transcript_unavailable" {
		t.Fatalf("code=%q", got)
	}
	var output bytes.Buffer
	printSessionTranscriptEvent(&output, protocol.SessionTranscriptEvent{
		Timestamp:  protocol.Ptr("2026-07-19T10:00:00Z"),
		Kind:       "tool_call",
		ToolName:   protocol.Ptr("shell"),
		ToolCallID: protocol.Ptr("call-1"),
		Text:       protocol.Ptr(`{"cmd":"pwd"}`),
	})
	for _, want := range []string{"2026-07-19T10:00:00Z", "TOOL_CALL", "shell", "call-1", `{"cmd":"pwd"}`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output=%q missing %q", output.String(), want)
		}
	}
}

func TestSessionInstructionsErrorCodeAndRendering(t *testing.T) {
	if got := sessionInstructionsErrorCode(errors.New("daemon error: invalid_evidence")); got != "invalid_evidence" {
		t.Fatalf("code=%q", got)
	}
	if got := sessionInstructionsErrorCode(errors.New("network down")); got != "model_unavailable" {
		t.Fatalf("code=%q", got)
	}
	var output bytes.Buffer
	printSessionInstructionsTo(&output, &protocol.SessionInstructionsResult{Answer: "Unclear.", Evidence: []protocol.EvidenceExcerpt{{TurnID: "turn-1", Author: "user", Quote: "hello"}}, TranscriptPath: "/tmp/synthetic.jsonl"})
	if text := output.String(); !strings.Contains(text, "Unclear.") || !strings.Contains(text, "Transcript: /tmp/synthetic.jsonl") {
		t.Fatalf("output=%q", text)
	}
}
