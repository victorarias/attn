package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	annotationSourceFile = "file"
	annotationSourceSeed = "seed"
)

type annotationDocumentSource struct {
	documentURI string
	kind        string
	workspaceID string
	path        string
	seedID      string
	draftKey    string
	seedTitle   string
}

func encodeURIComponent(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.ContainsRune("-_.!~*'()", rune(b)) {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&15])
	}
	return out.String()
}

func seedDocumentURI(seedID string) string {
	return "attn://seed/" + encodeURIComponent(seedID)
}

func fileDocumentURI(workspaceID, path string) string {
	return "attn://file/" + encodeURIComponent(workspaceID) + "/" + encodeURIComponent(filepath.Clean(path))
}

// resolveAnnotationDocumentSource validates typed authority fields and returns
// the store key. documentURI is correlation-only and is never parsed.
func (d *Daemon) resolveAnnotationDocumentSource(documentURI, kind string, workspaceID, path, seedID *string) (annotationDocumentSource, error) {
	source := annotationDocumentSource{
		documentURI: documentURI,
		kind:        strings.TrimSpace(kind),
		workspaceID: strings.TrimSpace(protocol.Deref(workspaceID)),
		path:        strings.TrimSpace(protocol.Deref(path)),
		seedID:      strings.TrimSpace(protocol.Deref(seedID)),
	}
	if strings.TrimSpace(source.documentURI) == "" {
		return source, fmt.Errorf("document_uri is required")
	}
	switch source.kind {
	case annotationSourceFile:
		if source.workspaceID == "" {
			return source, fmt.Errorf("workspace_id is required for file source")
		}
		if source.path == "" {
			return source, fmt.Errorf("path is required for file source")
		}
		if source.seedID != "" {
			return source, fmt.Errorf("seed_id is not valid for file source")
		}
		if !filepath.IsAbs(source.path) {
			return source, fmt.Errorf("path must be absolute for file source: %s", source.path)
		}
		source.path = filepath.Clean(source.path)
		source.draftKey = source.path
		if canonical := fileDocumentURI(source.workspaceID, source.path); source.documentURI != canonical {
			return source, fmt.Errorf("document_uri does not match typed file source: want %s", canonical)
		}
	case annotationSourceSeed:
		if source.seedID == "" {
			return source, fmt.Errorf("seed_id is required for seed source")
		}
		if source.workspaceID != "" || source.path != "" {
			return source, fmt.Errorf("workspace_id and path are not valid for seed source")
		}
		if err := d.requireHome(garden.Surface); err != nil {
			return source, err
		}
		seed, _, err := d.readSeed(source.seedID)
		if err != nil {
			return source, err
		}
		source.seedID = seed.ID
		source.seedTitle = seed.Title
		source.draftKey = seedDocumentURI(seed.ID)
		if source.documentURI != source.draftKey {
			return source, fmt.Errorf("document_uri does not match typed seed source: want %s", source.draftKey)
		}
	default:
		return source, fmt.Errorf("source_kind must be file or seed")
	}
	return source, nil
}

func annotationSourcePointers(source annotationDocumentSource) (workspaceID, path, seedID *string) {
	if source.workspaceID != "" {
		workspaceID = protocol.Ptr(source.workspaceID)
	}
	if source.path != "" {
		path = protocol.Ptr(source.path)
	}
	if source.seedID != "" {
		seedID = protocol.Ptr(source.seedID)
	}
	return
}

func (d *Daemon) handleMarkdownAnnotationsGet(client *wsClient, msg *protocol.MarkdownAnnotationsGetMessage) {
	source, sourceErr := d.resolveAnnotationDocumentSource(msg.DocumentUri, msg.SourceKind, msg.WorkspaceID, msg.Path, msg.SeedID)
	workspaceID, path, seedID := annotationSourcePointers(source)
	handler := newAnnotationDraftHandler(d, client, markdownAnnotationDraftAccessors(d.store), "document source",
		func(result annotationDraftResult[protocol.MarkdownAnnotation]) protocol.MarkdownAnnotationsGetResultMessage {
			if sourceErr != nil {
				result.err = protocol.Ptr("markdown_annotations_get: " + sourceErr.Error())
				result.success = false
			}
			return protocol.MarkdownAnnotationsGetResultMessage{
				Event: protocol.EventMarkdownAnnotationsGetResult, RequestID: msg.RequestID,
				DocumentUri: source.documentURI, SourceKind: source.kind, WorkspaceID: workspaceID, Path: path, SeedID: seedID,
				Annotations: result.annotations, Generation: result.generation, Success: result.success, Error: result.err,
			}
		})
	key := source.draftKey
	if sourceErr != nil {
		key = ""
	}
	handler.get("markdown_annotations_get", key, decodeMarkdownAnnotations)
}

func (d *Daemon) handleMarkdownAnnotationsSave(client *wsClient, msg *protocol.MarkdownAnnotationsSaveMessage) {
	source, sourceErr := d.resolveAnnotationDocumentSource(msg.DocumentUri, msg.SourceKind, msg.WorkspaceID, msg.Path, msg.SeedID)
	workspaceID, path, seedID := annotationSourcePointers(source)
	handler := newAnnotationDraftHandler(d, client, markdownAnnotationDraftAccessors(d.store), "document source",
		func(result annotationDraftResult[protocol.MarkdownAnnotation]) protocol.MarkdownAnnotationsSaveResultMessage {
			if sourceErr != nil {
				result.err = protocol.Ptr("markdown_annotations_save: " + sourceErr.Error())
				result.success = false
			}
			return protocol.MarkdownAnnotationsSaveResultMessage{
				Event: protocol.EventMarkdownAnnotationsSaveResult, RequestID: msg.RequestID,
				DocumentUri: source.documentURI, SourceKind: source.kind, WorkspaceID: workspaceID, Path: path, SeedID: seedID,
				Generation: result.generation, Success: result.success, Stale: result.stale, Error: result.err,
			}
		})
	key := source.draftKey
	if sourceErr != nil {
		key = ""
	}
	handler.save("markdown_annotations_save", key, msg.Annotations, "", msg.Generation)
}

func (d *Daemon) handleMarkdownAnnotationsClear(client *wsClient, msg *protocol.MarkdownAnnotationsClearMessage) {
	source, sourceErr := d.resolveAnnotationDocumentSource(msg.DocumentUri, msg.SourceKind, msg.WorkspaceID, msg.Path, msg.SeedID)
	workspaceID, path, seedID := annotationSourcePointers(source)
	handler := newAnnotationDraftHandler(d, client, markdownAnnotationDraftAccessors(d.store), "document source",
		func(result annotationDraftResult[protocol.MarkdownAnnotation]) protocol.MarkdownAnnotationsClearResultMessage {
			if sourceErr != nil {
				result.err = protocol.Ptr("markdown_annotations_clear: " + sourceErr.Error())
				result.success = false
			}
			return protocol.MarkdownAnnotationsClearResultMessage{
				Event: protocol.EventMarkdownAnnotationsClearResult, RequestID: msg.RequestID,
				DocumentUri: source.documentURI, SourceKind: source.kind, WorkspaceID: workspaceID, Path: path, SeedID: seedID,
				Generation: result.generation, Success: result.success, Error: result.err,
			}
		})
	key := source.draftKey
	if sourceErr != nil {
		key = ""
	}
	handler.clear("markdown_annotations_clear", key, msg.Generation)
}

func decodeMarkdownAnnotations(raw string) ([]protocol.MarkdownAnnotation, error) {
	if strings.TrimSpace(raw) == "" {
		return []protocol.MarkdownAnnotation{}, nil
	}
	var annotations []protocol.MarkdownAnnotation
	if err := json.Unmarshal([]byte(raw), &annotations); err != nil {
		return nil, err
	}
	if annotations == nil {
		annotations = []protocol.MarkdownAnnotation{}
	}
	return annotations, nil
}
