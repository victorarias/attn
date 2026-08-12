package main

import (
	"strings"
	"testing"
)

const denseText = "The implementation consideration underlying the classification " +
	"subsystem demonstrates that propositional characterisation of the corresponding " +
	"infrastructure necessitates substantial reconsideration of the architectural " +
	"assumptions established throughout the preceding investigation of comparable " +
	"functionality across the participating components. "

func dense(times int) string { return strings.Repeat(denseText, times) }

// The gate must never wedge an agent. A rewrite that still reads densely is
// still progress, so the second call goes through whatever it says.
func TestGateRefusesOncePerSession(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())

	first := evaluateProseGate("ticket new", "s1", dense(6))
	if !first.Refused {
		t.Fatalf("dense prose was not refused: %+v", first)
	}
	if !strings.Contains(first.Stderr, "Restate your last message") {
		t.Errorf("refusal did not carry the nudge: %q", first.Stderr)
	}

	second := evaluateProseGate("ticket new", "s1", dense(7))
	if second.Refused {
		t.Fatalf("a rewrite was refused a second time: %+v", second)
	}

	third := evaluateProseGate("ticket new", "s1", dense(8))
	if !third.Refused {
		t.Error("the gate must arm again for the next message, not stay open forever")
	}
}

// One agent's refusal must not open the gate for another.
func TestRefusalIsPerSession(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())

	if !evaluateProseGate("delegate", "s1", dense(6)).Refused {
		t.Fatal("expected the first session to be refused")
	}
	if !evaluateProseGate("delegate", "s2", dense(6)).Refused {
		t.Error("a second session went through on the first session's refusal")
	}
}

// The nudge asks for plainness, and plainness is when diagrams die. The author
// is told, and the write still lands.
func TestRewriteReportsDroppedDiagram(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())

	withDiagram := dense(6) + "\n\n```mermaid\nflowchart LR\n A --> B\n```\n\nSee [the plan](docs/plan.md).\n"
	if !evaluateProseGate("ticket comment", "s1", withDiagram).Refused {
		t.Fatal("expected the dense message to be refused")
	}

	rewrite := evaluateProseGate("ticket comment", "s1", "We check the text, then we write it. See [the plan](docs/plan.md).")
	if rewrite.Refused {
		t.Fatal("the rewrite must land even when it dropped structure")
	}
	if !strings.Contains(rewrite.Stderr, "fenced blocks") {
		t.Errorf("dropping the diagram was not reported: %q", rewrite.Stderr)
	}
	if strings.Contains(rewrite.Stderr, "links") {
		t.Errorf("the surviving link was reported as lost: %q", rewrite.Stderr)
	}
}

// A rewrite that keeps everything says nothing at all.
func TestQuietWhenRewriteKeepsStructure(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())

	withDiagram := dense(6) + "\n\n```mermaid\nflowchart LR\n A --> B\n```\n"
	if !evaluateProseGate("delegate", "s1", withDiagram).Refused {
		t.Fatal("expected the dense message to be refused")
	}

	rewrite := evaluateProseGate("delegate", "s1", "We check the text, then we write it.\n\n```mermaid\nflowchart LR\n A --> B\n```\n")
	if rewrite.Refused || rewrite.Stderr != "" {
		t.Errorf("a clean rewrite should be silent, got %+v", rewrite)
	}
}

// Plain prose never reaches the refusal record, so the gate stays armed.
func TestPlainProsePassesUntouched(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())

	plain := strings.Repeat("We run the gate before the write lands. It reads the text and says yes or no. ", 12)
	if out := evaluateProseGate("ticket new", "s1", plain); out.Refused || out.Stderr != "" {
		t.Fatalf("plain prose was not left alone: %+v", out)
	}
	if _, ok := readRefusal("s1"); ok {
		t.Error("plain prose left a refusal on record")
	}
}
