package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedNoteRingFlagIsExplicit(t *testing.T) {
	f := newSeedFlags("note")
	positionals := f.parse("note", []string{"s-7k3f9m", "-m", "look here", "--ring"})
	if len(positionals) != 1 || positionals[0] != "s-7k3f9m" || !*f.ring {
		t.Fatalf("parsed note = positionals %q ring %v", positionals, *f.ring)
	}

	plain := newSeedFlags("note")
	plain.parse("note", []string{"s-7k3f9m", "-m", "quiet"})
	if *plain.ring {
		t.Fatal("a plain note rings without --ring")
	}
}

func TestSeedShowRendersAndSerializesWatchState(t *testing.T) {
	result := &protocol.SeedShowResult{
		Seed:     protocol.Seed{ID: "s-7k3f9m", Title: "doorbell", Status: "growing"},
		Watching: true,
	}
	var out bytes.Buffer
	fprintSeedShow(&out, result)
	if !strings.Contains(out.String(), "watching  yes") {
		t.Fatalf("show output has no watch state:\n%s", out.String())
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"watching":true`) {
		t.Fatalf("JSON has no watch state: %s", raw)
	}
}
