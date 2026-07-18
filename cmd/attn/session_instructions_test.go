package main

import (
	"bytes"
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
