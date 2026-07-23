package pty

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// fakeBlockRef is a pure blockRef for block-table tests: it resolves to a fixed
// SCREEN point and counts Free calls so a test can assert every native ref
// would have been freed (the pure analogue of ghosttyvt.LiveTrackedRefs).
type fakeBlockRef struct {
	x, y  int
	freed *int
}

func (r *fakeBlockRef) ScreenPoint() (int, int, bool) { return r.x, r.y, true }
func (r *fakeBlockRef) Free()                          { *r.freed++ }

type blkStep struct {
	Marker   string  `json:"marker"`
	Row      int     `json:"row"`
	Col      int     `json:"col"`
	Cmdline  *string `json:"cmdline"`
	ExitCode *int    `json:"exitCode"`
}

type blkBlock struct {
	ID             uint64  `json:"id"`
	PromptRow      int32   `json:"promptRow"`
	InputRow       *int32  `json:"inputRow"`
	InputCol       *int32  `json:"inputCol"`
	OutputStartRow *int32  `json:"outputStartRow"`
	EndRow         *int32  `json:"endRow"`
	Command        *string `json:"command"`
	ExitCode       *int32  `json:"exitCode"`
}

type blkGenerate struct {
	FullCycles    int    `json:"fullCycles"`
	RowsPerCycle  int    `json:"rowsPerCycle"`
	CommandPrefix string `json:"commandPrefix"`
}

type blkExpect struct {
	Completed      []blkBlock `json:"completed"`
	CompletedCount *int       `json:"completedCount"`
	FirstID        *uint64    `json:"firstId"`
	LastID         *uint64    `json:"lastId"`
	FirstCommand   *string    `json:"firstCommand"`
	LastCommand    *string    `json:"lastCommand"`
	FirstPromptRow *int32     `json:"firstPromptRow"`
	Pending        *blkBlock  `json:"pending"`
	NextID         uint64     `json:"nextId"`
}

type blkCase struct {
	Name     string       `json:"name"`
	Steps    []blkStep    `json:"steps"`
	Generate *blkGenerate `json:"generate"`
	Expect   blkExpect    `json:"expect"`
}

func loadBlockCorpus(t *testing.T) []blkCase {
	t.Helper()
	data, err := os.ReadFile("testdata/osc133_block_corpus.json")
	if err != nil {
		t.Fatalf("read block corpus: %v", err)
	}
	var doc struct {
		Cases []blkCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse block corpus: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("block corpus is empty")
	}
	return doc.Cases
}

func expandBlockSteps(t *testing.T, c blkCase) []blkStep {
	t.Helper()
	if c.Steps != nil {
		return c.Steps
	}
	g := c.Generate
	if g == nil {
		t.Fatalf("case %s has neither steps nor generate", c.Name)
	}
	var steps []blkStep
	for i := 0; i < g.FullCycles; i++ {
		base := i * g.RowsPerCycle
		cmd := fmt.Sprintf("%s%d", g.CommandPrefix, i)
		zero := 0
		steps = append(steps,
			blkStep{Marker: "prompt-start", Row: base, Col: 0},
			blkStep{Marker: "pre-exec", Row: base + 1, Col: 0, Cmdline: &cmd},
			blkStep{Marker: "command-end", Row: base + 2, Col: 0, ExitCode: &zero},
		)
	}
	return steps
}

func stepToMarker(s blkStep) osc133Marker {
	switch s.Marker {
	case "prompt-start":
		return osc133Marker{Kind: osc133PromptStart}
	case "input-start":
		return osc133Marker{Kind: osc133InputStart}
	case "pre-exec":
		return osc133Marker{Kind: osc133PreExec, Cmdline: s.Cmdline}
	case "command-end":
		var ec *int32
		if s.ExitCode != nil {
			v := int32(*s.ExitCode)
			ec = &v
		}
		return osc133Marker{Kind: osc133CommandEnd, ExitCode: ec}
	default:
		panic("unknown corpus marker " + s.Marker)
	}
}

func attachToBlk(d AttachBlockData) blkBlock {
	return blkBlock{
		ID:             d.ID,
		PromptRow:      d.PromptRow,
		InputRow:       d.InputRow,
		InputCol:       d.InputCol,
		OutputStartRow: d.OutputStartRow,
		EndRow:         d.EndRow,
		Command:        d.Command,
		ExitCode:       d.ExitCode,
	}
}

func i32PtrEq(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func blocksEqual(got, want blkBlock, pending bool) bool {
	if got.ID != want.ID || got.PromptRow != want.PromptRow {
		return false
	}
	if !i32PtrEq(got.InputRow, want.InputRow) || !i32PtrEq(got.InputCol, want.InputCol) {
		return false
	}
	if !i32PtrEq(got.OutputStartRow, want.OutputStartRow) {
		return false
	}
	if !i32PtrEq(got.ExitCode, want.ExitCode) {
		return false
	}
	// endRow is absent on a pending block by construction; the corpus omits it.
	if !pending && !i32PtrEq(got.EndRow, want.EndRow) {
		return false
	}
	// command: corpus completed blocks always carry a (possibly empty) command;
	// pending blocks carry one only once pre-exec has been seen.
	wantCmd := ""
	if want.Command != nil {
		wantCmd = *want.Command
	}
	gotCmd := ""
	if got.Command != nil {
		gotCmd = *got.Command
	}
	if want.Command == nil && got.Command != nil {
		// pending-before-pre-exec: corpus has no command, table must not invent one
		return got.Command == nil
	}
	return wantCmd == gotCmd
}

// TestBlockTableCorpus proves the Go worker block table produces the same
// lifecycle state as the shared corpus (proven against the client
// TerminalBlockStore by app/src/utils/terminalBlocks.corpus.test.ts). It also
// asserts the leak contract: after Close, every ref the table ever held is
// freed exactly once.
func TestBlockTableCorpus(t *testing.T) {
	for _, c := range loadBlockCorpus(t) {
		t.Run(c.Name, func(t *testing.T) {
			steps := expandBlockSteps(t, c)
			bt := newBlockTable()
			created, freed := 0, 0
			for _, s := range steps {
				created++
				ref := &fakeBlockRef{x: s.Col, y: s.Row, freed: &freed}
				bt.ApplyMarker(stepToMarker(s), ref, false)
			}

			snap := bt.SnapshotBlocks()
			var completed []blkBlock
			var pending *blkBlock
			for i := range snap {
				b := attachToBlk(snap[i])
				if snap[i].Pending {
					if pending != nil {
						t.Fatalf("more than one pending block in snapshot")
					}
					p := b
					pending = &p
				} else {
					completed = append(completed, b)
				}
			}

			if c.Expect.Completed != nil {
				if len(completed) != len(c.Expect.Completed) {
					t.Fatalf("completed count = %d, want %d\ngot: %s", len(completed), len(c.Expect.Completed), mustJSON(completed))
				}
				for i := range completed {
					if !blocksEqual(completed[i], c.Expect.Completed[i], false) {
						t.Fatalf("completed[%d] mismatch\n got: %s\nwant: %s", i, mustJSON(completed[i]), mustJSON(c.Expect.Completed[i]))
					}
				}
			}
			if c.Expect.CompletedCount != nil && len(completed) != *c.Expect.CompletedCount {
				t.Fatalf("completed count = %d, want %d", len(completed), *c.Expect.CompletedCount)
			}
			if c.Expect.FirstID != nil && completed[0].ID != *c.Expect.FirstID {
				t.Fatalf("firstId = %d, want %d", completed[0].ID, *c.Expect.FirstID)
			}
			if c.Expect.LastID != nil && completed[len(completed)-1].ID != *c.Expect.LastID {
				t.Fatalf("lastId = %d, want %d", completed[len(completed)-1].ID, *c.Expect.LastID)
			}
			if c.Expect.FirstCommand != nil {
				got := ""
				if completed[0].Command != nil {
					got = *completed[0].Command
				}
				if got != *c.Expect.FirstCommand {
					t.Fatalf("firstCommand = %q, want %q", got, *c.Expect.FirstCommand)
				}
			}
			if c.Expect.LastCommand != nil {
				last := completed[len(completed)-1]
				got := ""
				if last.Command != nil {
					got = *last.Command
				}
				if got != *c.Expect.LastCommand {
					t.Fatalf("lastCommand = %q, want %q", got, *c.Expect.LastCommand)
				}
			}
			if c.Expect.FirstPromptRow != nil && completed[0].PromptRow != *c.Expect.FirstPromptRow {
				t.Fatalf("firstPromptRow = %d, want %d", completed[0].PromptRow, *c.Expect.FirstPromptRow)
			}

			if c.Expect.Pending == nil {
				if pending != nil {
					t.Fatalf("expected no pending block, got %s", mustJSON(*pending))
				}
			} else {
				if pending == nil {
					t.Fatalf("expected pending block %s, got none", mustJSON(*c.Expect.Pending))
				}
				if !blocksEqual(*pending, *c.Expect.Pending, true) {
					t.Fatalf("pending mismatch\n got: %s\nwant: %s", mustJSON(*pending), mustJSON(*c.Expect.Pending))
				}
			}

			if bt.nextID != c.Expect.NextID {
				t.Fatalf("nextID = %d, want %d", bt.nextID, c.Expect.NextID)
			}

			bt.Close()
			if freed != created {
				t.Fatalf("ref leak: created %d refs, freed %d after Close", created, freed)
			}
		})
	}
}
