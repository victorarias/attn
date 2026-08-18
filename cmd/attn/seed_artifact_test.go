package main

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// The flag the caller used chooses the kind, so nobody types it twice — and a
// call naming two documents is refused rather than silently picking one.
func TestSeedArtifactFlagsChooseTheKind(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want *protocol.SeedArtifactReference
		// refusal is the substring the error must carry when want is nil.
		refusal string
	}{
		{
			name: "a path is a markdown file",
			args: []string{"--path", "docs/plans/x.md"},
			want: &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr("docs/plans/x.md")},
		},
		{
			name: "a repository beside a path tells two worktrees apart",
			args: []string{"--path", "docs/plans/x.md", "--repo", "attn"},
			want: &protocol.SeedArtifactReference{
				Kind: garden.ArtifactMarkdownFile,
				Path: protocol.Ptr("docs/plans/x.md"), Repository: protocol.Ptr("attn"),
			},
		},
		{
			name: "a notebook id is a notebook document",
			args: []string{"--notebook", "nb-7"},
			want: &protocol.SeedArtifactReference{Kind: garden.ArtifactNotebook, NotebookDocumentID: protocol.Ptr("nb-7")},
		},
		{
			name: "a url is a url",
			args: []string{"--url", "https://example.test/pr/1"},
			want: &protocol.SeedArtifactReference{Kind: garden.ArtifactURL, URL: protocol.Ptr("https://example.test/pr/1")},
		},
		{
			name:    "no reference at all",
			args:    nil,
			refusal: "name the document",
		},
		{
			name:    "two documents in one call",
			args:    []string{"--path", "x.md", "--url", "https://example.test"},
			refusal: "--path and --url were all given",
		},
		{
			name:    "a repository with nothing to qualify",
			args:    []string{"--repo", "attn"},
			refusal: "name the document",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSeedFlags("attach")
			f.parse("attach", append([]string{"s-7k3f9m"}, tc.args...))
			got, err := f.artifact()
			if tc.refusal != "" {
				if err == nil {
					t.Fatalf("accepted %v; wanted a refusal naming %q", tc.args, tc.refusal)
				}
				if !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("refusal %q does not name %q", err, tc.refusal)
				}
				return
			}
			if err != nil {
				t.Fatalf("artifact(): %v", err)
			}
			if got.Kind != tc.want.Kind ||
				protocol.Deref(got.Path) != protocol.Deref(tc.want.Path) ||
				protocol.Deref(got.Repository) != protocol.Deref(tc.want.Repository) ||
				protocol.Deref(got.NotebookDocumentID) != protocol.Deref(tc.want.NotebookDocumentID) ||
				protocol.Deref(got.URL) != protocol.Deref(tc.want.URL) {
				t.Fatalf("reference = %+v, want %+v", got, tc.want)
			}
			// Whatever the CLI composes must survive the daemon's own rule; a
			// flag combination that only the CLI accepts is a broken verb.
			if _, err := garden.ValidateArtifact(garden.ArtifactReference{
				Kind:               got.Kind,
				Path:               protocol.Deref(got.Path),
				Repository:         protocol.Deref(got.Repository),
				NotebookDocumentID: protocol.Deref(got.NotebookDocumentID),
				URL:                protocol.Deref(got.URL),
			}); err != nil {
				t.Fatalf("the daemon refuses what the CLI composed: %v", err)
			}
		})
	}
}
