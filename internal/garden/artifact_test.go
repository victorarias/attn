package garden

import (
	"strings"
	"testing"
)

func TestValidateArtifactAcceptsEachKindAndRefusesTheRest(t *testing.T) {
	cases := []struct {
		name string
		in   ArtifactReference
		// want is empty when the reference should be accepted; otherwise the
		// substring the refusal must carry, so the caller can fix the call.
		want string
	}{
		{name: "notebook", in: ArtifactReference{Kind: ArtifactNotebook, NotebookDocumentID: "nb-7"}},
		{name: "markdown file", in: ArtifactReference{Kind: ArtifactMarkdownFile, Path: "docs/plans/x.md"}},
		{
			name: "markdown file in a named repository",
			in:   ArtifactReference{Kind: ArtifactMarkdownFile, Path: "docs/plans/x.md", Repository: "attn"},
		},
		{name: "repository path", in: ArtifactReference{Kind: ArtifactRepository, Repository: "attn", Path: "internal/garden"}},
		{name: "url", in: ArtifactReference{Kind: ArtifactURL, URL: "https://example.test/pr/1"}},
		{name: "trims to the same reference", in: ArtifactReference{Kind: " URL ", URL: "  https://example.test  "}},

		{name: "no kind", in: ArtifactReference{URL: "https://example.test"}, want: "needs a kind"},
		{name: "unknown kind", in: ArtifactReference{Kind: "gist", URL: "https://example.test"}, want: `"gist" is not a kind`},
		{name: "notebook with no id", in: ArtifactReference{Kind: ArtifactNotebook}, want: "needs notebook_document_id"},
		{name: "markdown file with no path", in: ArtifactReference{Kind: ArtifactMarkdownFile, Repository: "attn"}, want: "needs path"},
		{name: "repository with no path", in: ArtifactReference{Kind: ArtifactRepository, Repository: "attn"}, want: "needs repository and path"},
		{name: "url with no url", in: ArtifactReference{Kind: ArtifactURL}, want: "needs url"},
		{
			name: "notebook carrying a path too",
			in:   ArtifactReference{Kind: ArtifactNotebook, NotebookDocumentID: "nb-7", Path: "x.md"},
			want: "carries notebook_document_id, not path",
		},
		{
			name: "url carrying a repository too",
			in:   ArtifactReference{Kind: ArtifactURL, URL: "https://example.test", Repository: "attn"},
			want: "carries url, not repository",
		},
		{
			name: "a field carrying a document instead of pointing at one",
			in:   ArtifactReference{Kind: ArtifactURL, URL: strings.Repeat("x", MaxArtifactFieldChars+1)},
			want: "max_artifact_field_chars=2048, asked for 2049",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateArtifact(tc.in)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ValidateArtifact(%+v) refused: %v", tc.in, err)
				}
				if got != tc.in.trimmed() {
					t.Fatalf("stored %+v, want the trimmed input %+v", got, tc.in.trimmed())
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateArtifact(%+v) was accepted; wanted a refusal naming %q", tc.in, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

// The projection is the only definition of "current artifacts", so it is
// exercised over the shapes a real log produces — including the two that a
// naive replay gets wrong.
func TestCurrentArtifactsProjectsAttachMinusDetach(t *testing.T) {
	plan := ArtifactReference{Kind: ArtifactMarkdownFile, Path: "docs/plans/plan.md"}
	notes := ArtifactReference{Kind: ArtifactNotebook, NotebookDocumentID: "nb-7"}
	pr := ArtifactReference{Kind: ArtifactURL, URL: "https://example.test/pr/1"}

	cases := []struct {
		name string
		// log is newest first, the order every garden read hands notes over in.
		log  []Note
		want []ArtifactReference
	}{
		{name: "an empty log has no artifacts"},
		{
			name: "a plain note carrying nothing",
			log:  []Note{{Kind: NoteKindNote, Body: "worked on it"}},
		},
		{
			name: "one attach",
			log:  []Note{{Kind: NoteKindAttach, Artifact: &plan}},
			want: []ArtifactReference{plan},
		},
		{
			name: "a detach removes the attach beneath it",
			log: []Note{
				{Kind: NoteKindDetach, Artifact: &plan},
				{Kind: NoteKindAttach, Artifact: &plan},
			},
		},
		{
			name: "a detach above an unrelated attach leaves it alone",
			log: []Note{
				{Kind: NoteKindDetach, Artifact: &plan},
				{Kind: NoteKindAttach, Artifact: &notes},
				{Kind: NoteKindAttach, Artifact: &plan},
			},
			want: []ArtifactReference{notes},
		},
		{
			// The newest verb decides. Replaying newest-first without reversing
			// would let the older detach win and lose the re-attach; keeping the
			// detached key in the order would list it twice.
			name: "re-attaching after a detach brings it back once",
			log: []Note{
				{Kind: NoteKindAttach, Artifact: &plan},
				{Kind: NoteKindDetach, Artifact: &plan},
				{Kind: NoteKindAttach, Artifact: &plan},
			},
			want: []ArtifactReference{plan},
		},
		{
			name: "a detach naming nothing attached is inert",
			log:  []Note{{Kind: NoteKindDetach, Artifact: &pr}},
		},
		{
			// Oldest first, so the set reads in the order the work produced it
			// rather than in the reverse order the log is read in.
			name: "the set keeps the order things were attached in",
			log: []Note{
				{Kind: NoteKindAttach, Artifact: &pr},
				{Kind: NoteKindNote, Body: "meanwhile"},
				{Kind: NoteKindAttach, Artifact: &notes},
				{Kind: NoteKindAttach, Artifact: &plan},
			},
			want: []ArtifactReference{plan, notes, pr},
		},
		{
			// Same path, different repository: two documents, so a detach of one
			// must not take the other.
			name: "a repository tells two identical paths apart",
			log: []Note{
				{Kind: NoteKindDetach, Artifact: &ArtifactReference{Kind: ArtifactMarkdownFile, Path: "plan.md", Repository: "other"}},
				{Kind: NoteKindAttach, Artifact: &ArtifactReference{Kind: ArtifactMarkdownFile, Path: "plan.md", Repository: "other"}},
				{Kind: NoteKindAttach, Artifact: &ArtifactReference{Kind: ArtifactMarkdownFile, Path: "plan.md", Repository: "attn"}},
			},
			want: []ArtifactReference{{Kind: ArtifactMarkdownFile, Path: "plan.md", Repository: "attn"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CurrentArtifacts(tc.log)
			if len(got) != len(tc.want) {
				t.Fatalf("projected %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("artifact %d is %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestDefaultNoteBodyRendersTheReference(t *testing.T) {
	cases := []struct {
		kind string
		in   ArtifactReference
		want string
	}{
		{NoteKindAttach, ArtifactReference{Kind: ArtifactMarkdownFile, Path: "plan.md"}, "attached plan.md"},
		{NoteKindDetach, ArtifactReference{Kind: ArtifactMarkdownFile, Path: "plan.md"}, "detached plan.md"},
		{NoteKindAttach, ArtifactReference{Kind: ArtifactNotebook, NotebookDocumentID: "nb-7"}, "attached nb-7"},
		{NoteKindAttach, ArtifactReference{Kind: ArtifactURL, URL: "https://x.test"}, "attached https://x.test"},
		{NoteKindAttach, ArtifactReference{Kind: ArtifactRepository, Repository: "attn", Path: "internal/garden"}, "attached internal/garden (attn)"},
	}
	for _, tc := range cases {
		if got := DefaultNoteBody(tc.kind, tc.in); got != tc.want {
			t.Fatalf("DefaultNoteBody(%s, %+v) = %q, want %q", tc.kind, tc.in, got, tc.want)
		}
	}
}

func TestParseNoteKindAcceptsTheArtifactKinds(t *testing.T) {
	for _, kind := range []string{NoteKindAttach, NoteKindDetach} {
		got, err := ParseNoteKind(kind)
		if err != nil || got != kind {
			t.Fatalf("ParseNoteKind(%q) = %q, %v", kind, got, err)
		}
		if !CarriesArtifact(kind) {
			t.Fatalf("%q must carry an artifact", kind)
		}
	}
	for _, kind := range []string{NoteKindNote, NoteKindHandoff} {
		if CarriesArtifact(kind) {
			t.Fatalf("%q must not carry an artifact", kind)
		}
	}
}
