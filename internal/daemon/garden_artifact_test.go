package daemon

import (
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// artifactNote writes one attach or detach the way `attn seed attach` does.
func artifactNote(t *testing.T, d *Daemon, seedID, kind, body string, artifact *protocol.SeedArtifactReference) protocol.Response {
	t.Helper()
	msg := protocol.SeedNoteMessage{
		Cmd:             protocol.CmdSeedNote,
		SourceSessionID: protocol.Ptr("sess-a"),
		SeedID:          seedID,
		Body:            body,
		Kind:            protocol.Ptr(kind),
		Artifact:        artifact,
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
}

func markdownArtifact(path string) *protocol.SeedArtifactReference {
	return &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr(path)}
}

// The whole attach lifecycle over the real handlers: the log keeps every verb,
// and `show` answers with the set rather than the timeline.
func TestSeedArtifactsAttachAndDetachThroughTheLog(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Ship the thing"})

	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "", markdownArtifact("docs/plans/thing.md")); !resp.Ok {
		t.Fatalf("attach: %v", protocol.Deref(resp.Error))
	}
	// A caller with nothing to add gets a body rendered from the reference, so
	// the log reads as prose rather than as an empty line with a payload.
	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "", markdownArtifact("docs/plans/thing.md")); resp.Ok {
		if body := resp.SeedNoteResult.Note.Body; body != "attached docs/plans/thing.md" {
			t.Fatalf("default body = %q", body)
		}
	}
	notebook := &protocol.SeedArtifactReference{
		Kind: garden.ArtifactNotebook, NotebookDocumentID: protocol.Ptr("nb-7"),
	}
	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "the review notes", notebook); !resp.Ok {
		t.Fatalf("attach notebook: %v", protocol.Deref(resp.Error))
	}

	current := show(t, d, seed.ID).Artifacts
	if len(current) != 2 {
		t.Fatalf("artifacts = %+v, want the markdown file and the notebook", current)
	}
	if protocol.Deref(current[0].Path) != "docs/plans/thing.md" || protocol.Deref(current[1].NotebookDocumentID) != "nb-7" {
		t.Fatalf("artifacts = %+v", current)
	}

	if resp := artifactNote(t, d, seed.ID, garden.NoteKindDetach, "", markdownArtifact("docs/plans/thing.md")); !resp.Ok {
		t.Fatalf("detach: %v", protocol.Deref(resp.Error))
	}
	current = show(t, d, seed.ID).Artifacts
	if len(current) != 1 || protocol.Deref(current[0].NotebookDocumentID) != "nb-7" {
		t.Fatalf("after detach artifacts = %+v, want the notebook alone", current)
	}

	// Detaching removes it from the set and from neither the log nor its count:
	// the seed's memory of what happened is not edited by what is current now.
	log := show(t, d, seed.ID)
	if log.NotesTotal != 4 {
		t.Fatalf("log holds %d entries, want the four verbs", log.NotesTotal)
	}
}

// The set is projected over the whole log, not over the bounded window `show`
// renders — an attach older than the newest few entries is exactly the one a
// window would lose.
func TestSeedArtifactsSurviveABusyLog(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Long haul"})
	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "", markdownArtifact("plan.md")); !resp.Ok {
		t.Fatalf("attach: %v", protocol.Deref(resp.Error))
	}
	for i := 0; i < garden.ShowNotes+3; i++ {
		note(t, d, "sess-a", seed.ID, "another day of work", "")
	}

	result := show(t, d, seed.ID)
	if len(result.Notes) > garden.ShowNotes {
		t.Fatalf("show rendered %d notes, past its own window", len(result.Notes))
	}
	if len(result.Artifacts) != 1 || protocol.Deref(result.Artifacts[0].Path) != "plan.md" {
		t.Fatalf("artifacts = %+v, want plan.md still current", result.Artifacts)
	}
}

func TestSeedNoteRefusesArtifactsThatSayNothing(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Ship the thing"})

	cases := []struct {
		name     string
		kind     string
		body     string
		artifact *protocol.SeedArtifactReference
		want     string
	}{
		{
			name: "an attach with no reference associates nothing",
			kind: garden.NoteKindAttach, body: "here you go",
			want: "needs the artifact it attaches",
		},
		{
			name: "a detach with no reference takes nothing back",
			kind: garden.NoteKindDetach, body: "never mind",
			want: "needs the artifact it detaches",
		},
		{
			name: "a plain note carrying one is invisible to the projection",
			kind: garden.NoteKindNote, body: "look at this", artifact: markdownArtifact("plan.md"),
			want: "a note note carries no artifact",
		},
		{
			name: "a handoff carrying one is the same mistake",
			kind: garden.NoteKindHandoff, body: "over to you", artifact: markdownArtifact("plan.md"),
			want: "carries no artifact",
		},
		{
			name: "a malformed reference is refused by the domain rule",
			kind: garden.NoteKindAttach,
			artifact: &protocol.SeedArtifactReference{
				Kind: garden.ArtifactNotebook, Path: protocol.Ptr("plan.md"),
			},
			want: "needs notebook_document_id",
		},
		{
			name: "an unknown kind names the whole set",
			kind: "bookmark", body: "x",
			want: "is not a kind of note",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := artifactNote(t, d, seed.ID, tc.kind, tc.body, tc.artifact)
			if resp.Ok {
				t.Fatalf("accepted; wanted a refusal naming %q", tc.want)
			}
			if got := protocol.Deref(resp.Error); !strings.Contains(got, tc.want) {
				t.Fatalf("refusal %q does not name %q", got, tc.want)
			}
		})
	}

	if total := show(t, d, seed.ID).NotesTotal; total != 0 {
		t.Fatalf("a refused note wrote %d log entries", total)
	}
}
