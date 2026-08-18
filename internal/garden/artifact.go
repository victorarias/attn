package garden

import (
	"fmt"
	"slices"
	"strings"
)

// An artifact is a document a seed's work produced or leans on. The seed
// records the association and nothing else: where a document actually lives
// stays with the canonical-artifact lifecycle
// (docs/plans/2026-07-18-canonical-plan-artifact-lifecycle.md).
//
// The association is a typed reference on an `attach` log entry, and `detach`
// is the way out. The current set is a projection over the log — attach minus
// detach — so the log stays the whole truth about a seed and nothing has to be
// kept in sync with it.

// Artifact kinds. Each names which of the reference's fields carry it, and
// nothing else in the reference may be set: the daemon validates a shape, it
// never reads meaning out of a string a caller composed.
const (
	// ArtifactMarkdownFile is a markdown document at a path — the one kind a
	// seed's artifact set opens as its own file tile.
	ArtifactMarkdownFile = "markdown_file"
	// ArtifactNotebook is a Notebook document, addressed by its id.
	ArtifactNotebook = "notebook"
	// ArtifactRepository is a path inside a named repository.
	ArtifactRepository = "repository"
	// ArtifactURL is anything reachable by URL and nothing attn stores.
	ArtifactURL = "url"
)

// ArtifactKinds is every kind, in the order an unknown one is refused against.
var ArtifactKinds = []string{ArtifactMarkdownFile, ArtifactNotebook, ArtifactRepository, ArtifactURL}

// ArtifactReference is one typed association. Optional fields are omitted
// rather than written empty: unlike a seed's declared fields, nothing queries
// these, and an empty string beside a set one reads as a second answer.
type ArtifactReference struct {
	Kind               string `json:"kind"`
	NotebookDocumentID string `json:"notebook_document_id,omitempty"`
	Repository         string `json:"repository,omitempty"`
	Path               string `json:"path,omitempty"`
	URL                string `json:"url,omitempty"`
}

// MaxArtifactFieldChars bounds each field of a reference. A path, a repository
// name, a Notebook id and a URL are all short by nature; this is a tripwire
// past anything real — the longest path in this repo is 84 characters — so a
// caller that puts a document *in* the field is told, instead of storing it.
const MaxArtifactFieldChars = 2048

// trimArtifact is the one place a reference's fields are cleaned, so validation
// and storage never disagree about what was actually written.
func (a ArtifactReference) trimmed() ArtifactReference {
	return ArtifactReference{
		Kind:               strings.TrimSpace(strings.ToLower(a.Kind)),
		NotebookDocumentID: strings.TrimSpace(a.NotebookDocumentID),
		Repository:         strings.TrimSpace(a.Repository),
		Path:               strings.TrimSpace(a.Path),
		URL:                strings.TrimSpace(a.URL),
	}
}

// ValidateArtifact accepts a reference and hands back the trimmed form to
// store, or refuses naming the kind, the field it wanted, and what it got.
func ValidateArtifact(raw ArtifactReference) (ArtifactReference, error) {
	a := raw.trimmed()
	if a.Kind == "" {
		return ArtifactReference{}, fmt.Errorf(
			"an artifact needs a kind; the kinds are %s", strings.Join(ArtifactKinds, ", "))
	}
	if !slices.Contains(ArtifactKinds, a.Kind) {
		return ArtifactReference{}, fmt.Errorf(
			"%q is not a kind of artifact; the kinds are %s", raw.Kind, strings.Join(ArtifactKinds, ", "))
	}
	for name, value := range map[string]string{
		"notebook_document_id": a.NotebookDocumentID,
		"repository":           a.Repository,
		"path":                 a.Path,
		"url":                  a.URL,
	} {
		if n := len(value); n > MaxArtifactFieldChars {
			return ArtifactReference{}, fmt.Errorf(
				"max_artifact_field_chars=%d, asked for %d on %s; a reference points at a document, it does not carry one",
				MaxArtifactFieldChars, n, name)
		}
	}
	required, allowed := artifactFields(a.Kind)
	for _, field := range required {
		if a.field(field) == "" {
			return ArtifactReference{}, fmt.Errorf(
				"a %s artifact needs %s", a.Kind, strings.Join(required, " and "))
		}
	}
	for _, field := range []string{"notebook_document_id", "repository", "path", "url"} {
		if a.field(field) != "" && !slices.Contains(allowed, field) {
			return ArtifactReference{}, fmt.Errorf(
				"a %s artifact carries %s, not %s", a.Kind, strings.Join(allowed, " and "), field)
		}
	}
	return a, nil
}

// artifactFields is the whole table: what each kind must carry, and what it may.
func artifactFields(kind string) (required, allowed []string) {
	switch kind {
	case ArtifactMarkdownFile:
		// A markdown file may name the repository it lives in — the same document
		// at the same path in two worktrees is one artifact, and the repository is
		// what says which.
		return []string{"path"}, []string{"path", "repository"}
	case ArtifactNotebook:
		return []string{"notebook_document_id"}, []string{"notebook_document_id"}
	case ArtifactRepository:
		return []string{"repository", "path"}, []string{"repository", "path"}
	case ArtifactURL:
		return []string{"url"}, []string{"url"}
	}
	return nil, nil
}

func (a ArtifactReference) field(name string) string {
	switch name {
	case "notebook_document_id":
		return a.NotebookDocumentID
	case "repository":
		return a.Repository
	case "path":
		return a.Path
	case "url":
		return a.URL
	}
	return ""
}

// Label is how a reference reads on a log line and in the artifact set: the
// field that identifies it, which is the part a person recognizes.
func (a ArtifactReference) Label() string {
	switch {
	case a.Path != "":
		return a.Path
	case a.NotebookDocumentID != "":
		return a.NotebookDocumentID
	case a.URL != "":
		return a.URL
	case a.Repository != "":
		return a.Repository
	}
	return a.Kind
}

// Identity is what the attach-minus-detach projection keys on, so a detach
// naming the same document as an earlier attach removes it. Every field
// participates: two references differing anywhere are two artifacts, and
// silently collapsing them would drop one from the set.
func (a ArtifactReference) Identity() string {
	return strings.Join([]string{a.Kind, a.Path, a.NotebookDocumentID, a.Repository, a.URL}, "\x00")
}

// DefaultNoteBody is the log line written when an attach or detach carries no
// words of its own. It renders the typed reference; it never reads meaning out
// of one.
func DefaultNoteBody(kind string, a ArtifactReference) string {
	verb := "attached"
	if kind == NoteKindDetach {
		verb = "detached"
	}
	if a.Repository != "" && a.Path != "" {
		return fmt.Sprintf("%s %s (%s)", verb, a.Path, a.Repository)
	}
	return fmt.Sprintf("%s %s", verb, a.Label())
}

// CurrentArtifacts projects a seed's current set from its log: attach adds,
// detach removes, newest wins. Notes arrive newest first, as every garden read
// hands them over, and are replayed oldest first so the newest verb decides.
//
// The set is the projection and nothing else — there is no stored list to drift
// from it, which is why a note nobody could decode is skipped rather than
// failing the read.
func CurrentArtifacts(notes []Note) []ArtifactReference {
	current := map[string]ArtifactReference{}
	order := []string{}
	for i := len(notes) - 1; i >= 0; i-- {
		note := notes[i]
		if note.Artifact == nil {
			continue
		}
		key := note.Artifact.Identity()
		switch note.Kind {
		case NoteKindAttach:
			// The attach that made it current decides where it sits, so a
			// re-attach after a detach moves it to the end rather than
			// reappearing in a slot it was removed from — and, since a detached
			// key stays in order, listing it twice.
			order = append(slices.DeleteFunc(order, func(held string) bool { return held == key }), key)
			current[key] = *note.Artifact
		case NoteKindDetach:
			delete(current, key)
		}
	}
	out := make([]ArtifactReference, 0, len(current))
	for _, key := range order {
		if artifact, held := current[key]; held {
			out = append(out, artifact)
		}
	}
	return out
}
