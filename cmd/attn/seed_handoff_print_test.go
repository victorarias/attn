package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func seedNote(id, kind, body string) protocol.SeedNote {
	return protocol.SeedNote{
		ID: id, SeedID: "s-7k3f9m", Kind: kind, Body: body,
		AuthorMember: "keel", CreatedAt: "2026-08-13T10:22:00Z",
	}
}

// The slice's acceptance, at the surface a successor actually reads: the
// handoff is above the seed, not under its body and not buried in the log.
func TestFprintSeedShowPutsTheHandoffFirst(t *testing.T) {
	left := seedNote("n-aaaaaa", garden.NoteKindHandoff, "the join test is the gate")
	var buf bytes.Buffer
	fprintSeedShow(&buf, &protocol.SeedShowResult{
		Seed:       protocol.Seed{ID: "s-7k3f9m", Title: "carry this", Status: "growing", Body: "the plan"},
		Notes:      []protocol.SeedNote{left, seedNote("n-bbbbbb", garden.NoteKindNote, "ordinary progress")},
		NotesTotal: 2,
		Handoff:    &left,
	})
	out := buf.String()

	if !strings.HasPrefix(out, "handoff — keel,") {
		t.Fatalf("the handoff is not the first thing on the screen:\n%s", out)
	}
	if strings.Index(out, "the join test is the gate") > strings.Index(out, "s-7k3f9m") {
		t.Fatalf("the handoff renders after the seed:\n%s", out)
	}
	// Rendered once. The same paragraph twice on one screen reads as a bug.
	if n := strings.Count(out, "the join test is the gate"); n != 1 {
		t.Fatalf("the handoff body appears %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "ordinary progress") {
		t.Fatalf("dropping the handoff from the log dropped the rest of it:\n%s", out)
	}
}

// A seed nobody handed over says nothing about handoffs, and its log is
// whole.
func TestFprintSeedShowWithoutAHandoff(t *testing.T) {
	var buf bytes.Buffer
	fprintSeedShow(&buf, &protocol.SeedShowResult{
		Seed:       protocol.Seed{ID: "s-7k3f9m", Title: "quiet", Status: "planted"},
		Notes:      []protocol.SeedNote{seedNote("n-bbbbbb", garden.NoteKindNote, "what happened")},
		NotesTotal: 1,
	})
	out := buf.String()

	if strings.Contains(out, "handoff") {
		t.Fatalf("a seed with no handoff mentions one:\n%s", out)
	}
	if !strings.Contains(out, "what happened") {
		t.Fatalf("the log is missing:\n%s", out)
	}
}

// Dropping the handoff from the log must not turn a shown note into a hidden
// one: what is withheld is counted against the window the daemon read.
func TestFprintSeedShowCountsWhatItWithheld(t *testing.T) {
	left := seedNote("n-aaaaaa", garden.NoteKindHandoff, "over to you")
	notes := []protocol.SeedNote{left}
	for _, id := range []string{"n-bbbbbb", "n-cccccc", "n-dddddd", "n-eeeeee"} {
		notes = append(notes, seedNote(id, garden.NoteKindNote, "progress "+id))
	}
	var buf bytes.Buffer
	fprintSeedShow(&buf, &protocol.SeedShowResult{
		Seed:       protocol.Seed{ID: "s-7k3f9m", Title: "busy", Status: "growing"},
		Notes:      notes,
		NotesTotal: 9,
		Handoff:    &left,
	})

	if out := buf.String(); !strings.Contains(out, "4 more — `attn seed notes s-7k3f9m`") {
		t.Fatalf("the withheld count is wrong or missing:\n%s", out)
	}
}

// A handoff read in the log — an older one, or one on `attn seed notes` — is
// labelled, so it is recognisable as written to a successor rather than to
// nobody.
func TestFprintNotesLabelsAHandoff(t *testing.T) {
	var buf bytes.Buffer
	fprintNotes(&buf, []protocol.SeedNote{
		seedNote("n-aaaaaa", garden.NoteKindHandoff, "over to you"),
		seedNote("n-bbbbbb", garden.NoteKindNote, "progress"),
	}, "s-7k3f9m", 0)
	out := buf.String()

	handoffLine, plainLine := "", ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "2026-08-13") {
			if handoffLine == "" {
				handoffLine = line
			} else if plainLine == "" {
				plainLine = line
			}
		}
	}
	if !strings.Contains(handoffLine, garden.NoteKindHandoff) {
		t.Fatalf("a handoff on the log is not labelled: %q", handoffLine)
	}
	if strings.Contains(plainLine, garden.NoteKindNote) {
		t.Fatalf("a plain note carries a kind nobody needs: %q", plainLine)
	}
}

// Tending confirms the claim first — the tender needs to know it landed — and
// primes with the handoff on the same screen.
func TestFprintTransitionPrimesOnTend(t *testing.T) {
	left := seedNote("n-aaaaaa", garden.NoteKindHandoff, "start at the docstore compiler")
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed:    protocol.Seed{ID: "s-7k3f9m", Status: "growing", TenderMember: "alder"},
		Handoff: &left,
	})
	out := buf.String()

	if !strings.HasPrefix(out, "s-7k3f9m is growing, tended by alder\n") {
		t.Fatalf("the claim is not confirmed first:\n%s", out)
	}
	if !strings.Contains(out, "start at the docstore compiler") {
		t.Fatalf("tend did not print the handoff:\n%s", out)
	}

	// A move that carries none says nothing about handoffs.
	buf.Reset()
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{ID: "s-7k3f9m", Status: "dormant"},
	})
	if out := buf.String(); strings.Contains(out, "handoff") {
		t.Fatalf("a move with no handoff mentions one:\n%s", out)
	}
}

// A multi-line handoff stays readable: every line is indented under its header,
// so the note is one block instead of prose that runs into the seed below it.
func TestFprintHandoffIndentsEveryLine(t *testing.T) {
	left := seedNote("n-aaaaaa", garden.NoteKindHandoff, "first line\nsecond line\n")
	var buf bytes.Buffer
	fprintHandoff(&buf, &left)

	for _, want := range []string{"  first line\n", "  second line\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("line %q is not indented under the header:\n%s", strings.TrimSpace(want), buf.String())
		}
	}
}
