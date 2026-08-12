package prosegate

import (
	"strings"
	"testing"
)

// A message whose bulk is a diagram must be judged on its prose alone. Before
// proseOnly stripped fenced blocks, mermaid node labels read as long words in
// long lines and tripped the gate on their own.
func TestDiagramDoesNotTripTheGate(t *testing.T) {
	prose := strings.Repeat("The gate runs before the write lands. It reads the text and says yes or no. ", 12)
	diagram := "```mermaid\nflowchart LR\n" +
		strings.Repeat("  A[deterministic classification subsystem] --> B[propositional density evaluator]\n", 20) +
		"```\n"

	bare := Check(prose, Default())
	withDiagram := Check(prose+"\n\n"+diagram, Default())

	if bare.Tripped {
		t.Fatalf("plain prose tripped the gate: %+v", bare.Gates)
	}
	if withDiagram.Tripped {
		t.Fatalf("diagram tripped the gate on prose that passes alone: %+v", withDiagram.Gates)
	}
	if withDiagram.Words != bare.Words {
		t.Errorf("diagram counted as prose: %d words with, %d without", withDiagram.Words, bare.Words)
	}
}

// Tables are prose-shaped enough to fool a word counter but are not prose.
func TestTableDoesNotCountAsProse(t *testing.T) {
	prose := strings.Repeat("We keep the words short and the sentences plain. ", 25)
	table := "\n| feature | dense | accepted |\n| --- | --- | --- |\n" +
		strings.Repeat("| nominalization measurement | 13.572931 | 9.801244 |\n", 10)

	withTable := Check(prose+table, Default())
	if withTable.Tripped {
		t.Fatalf("table tripped the gate: %+v", withTable.Gates)
	}
}

// Below the floor the per-100-word rates are one sentence wide, so the gate
// says nothing rather than guessing.
func TestAbstainsBelowFloor(t *testing.T) {
	v := Check("Extraordinarily sophisticated implementation considerations notwithstanding.", Default())
	if !v.Abstained {
		t.Fatalf("expected abstention under %d words, got %+v", MinWords, v)
	}
	if v.Tripped {
		t.Error("abstained verdict must not also trip")
	}
	if v.Nudge != "" {
		t.Error("abstained verdict must not carry a nudge")
	}
}

// The gate must actually fire on the shape it was calibrated against: long
// words, long sentences, nominal style.
func TestFiresOnDenseProse(t *testing.T) {
	dense := strings.Repeat(
		"The implementation consideration underlying the classification subsystem "+
			"demonstrates that propositional characterisation of the corresponding "+
			"infrastructure necessitates substantial reconsideration of the "+
			"architectural assumptions established throughout the preceding "+
			"investigation of comparable functionality. ", 6)

	v := Check(dense, Default())
	if !v.Tripped {
		t.Fatalf("dense prose did not trip the gate: %+v", v)
	}
	if v.Nudge == "" {
		t.Error("a tripped verdict must carry the nudge")
	}
	if !strings.Contains(v.Nudge, "don't drop them") {
		t.Error("nudge must ask for structure to survive the rewrite")
	}
}

// Gate order comes from a map; without sorting the JSON output and any test
// asserting on it would flap.
func TestGateOrderIsStable(t *testing.T) {
	dense := strings.Repeat(
		"The implementation consideration underlying the classification subsystem "+
			"demonstrates that propositional characterisation of the corresponding "+
			"infrastructure necessitates substantial reconsideration. ", 12)

	first := Check(dense, Default())
	for range 20 {
		next := Check(dense, Default())
		if len(next.Gates) != len(first.Gates) {
			t.Fatalf("gate count changed between runs: %d then %d", len(first.Gates), len(next.Gates))
		}
		for j := range next.Gates {
			if next.Gates[j].Name != first.Gates[j].Name {
				t.Fatalf("gate order changed: %v then %v", first.Gates, next.Gates)
			}
		}
	}
}

func TestStructureLoss(t *testing.T) {
	before := StructureOf("# Title\n\nSee [the plan](docs/plan.md).\n\n" +
		"```mermaid\nflowchart LR\n A --> B\n```\n\n- one\n- two\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n")

	if before.FencedBlocks != 1 || before.Headings != 1 || before.ListItems != 2 || before.Tables != 1 || before.Links != 1 {
		t.Fatalf("miscounted structure: %+v", before)
	}

	stripped := StructureOf("# Title\n\nSee the plan.\n\n- one\n- two\n")
	lost := before.Lost(stripped)
	if len(lost) == 0 {
		t.Fatal("dropping a diagram, a table and a link must be reported as loss")
	}
	if before.Preserved(stripped) {
		t.Error("Preserved must be false when structure was dropped")
	}

	// Adding structure is fine; only losses are reported.
	richer := StructureOf("# Title\n## Sub\n\nSee [the plan](docs/plan.md).\n\n" +
		"```mermaid\nflowchart LR\n A --> B\n```\n\n- one\n- two\n- three\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n")
	if !before.Preserved(richer) {
		t.Errorf("a rewrite that adds structure must pass; lost=%v", before.Lost(richer))
	}
}

// Reorganising the prose is what the nudge asks for. A rewrite that folds two
// bullets into a sentence and drops a heading must not be reported as loss, or
// the warning fires on every honest rewrite.
func TestReorganisingProseIsNotLoss(t *testing.T) {
	before := StructureOf("# Title\n\n## Detail\n\n- one\n- two\n\n" +
		"```mermaid\nflowchart LR\n A --> B\n```\n")
	after := StructureOf("# Title\n\nOne and two.\n\n```mermaid\nflowchart LR\n A --> B\n```\n")

	if lost := before.Lost(after); len(lost) != 0 {
		t.Errorf("folding bullets and a heading into prose reported as loss: %v", lost)
	}
}
