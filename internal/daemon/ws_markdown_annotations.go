package daemon

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Markdown annotation drafts are keyed by absolute file path on the daemon
// that OWNS the document: on a hub, remote-workspace commands never reach
// these handlers — websocket.go routes them by workspace_id to the owning
// endpoint (remoteCommandWorkspaceID), so identical absolute paths on
// different endpoints never share a draft/generation/tombstone row. Within
// one daemon the key is the path alone: annotations are a property of the
// document, not the view, so the same file docked in two of this daemon's
// workspaces shows the same drafts. The path is
// normalized exactly like tilecontent's markdown tiles (TrimSpace; the daemon
// already receives absolute paths).
//
// All generation math is server-side in the store; the daemon enforces
// tombstoning by mapping store.ErrStaleMarkdownAnnotationSave to a
// stale=true, success=false result the client treats as benign.
//
// There are deliberately no broadcast events here: the annotation UI is
// single-writer (the tile that saves is the tile that renders), so
// cross-client live sync is out of scope.

// handleMarkdownAnnotationsGet replies with the persisted draft for a path.
// generation is the current floor — max(stored generation, tombstone) — so a
// re-mounting client seeds its counter past any tombstone.
func (d *Daemon) handleMarkdownAnnotationsGet(client *wsClient, msg *protocol.MarkdownAnnotationsGetMessage) {
	handler := newAnnotationDraftHandler(d, client, markdownAnnotationDraftAccessors(d.store), "path",
		func(result annotationDraftResult[protocol.MarkdownAnnotation]) protocol.MarkdownAnnotationsGetResultMessage {
			return protocol.MarkdownAnnotationsGetResultMessage{
				Event:       protocol.EventMarkdownAnnotationsGetResult,
				RequestID:   msg.RequestID,
				Path:        result.key,
				WorkspaceID: msg.WorkspaceID,
				Annotations: result.annotations,
				Generation:  result.generation,
				Success:     result.success,
				Error:       result.err,
			}
		})
	handler.get("markdown_annotations_get", msg.Path, decodeMarkdownAnnotations)
}

// handleMarkdownAnnotationsSave persists the full annotation list for a path.
// A generation at or below the stored generation or the tombstone comes back
// as stale=true, success=false — benign; the client drops its pending list
// and re-hydrates.
func (d *Daemon) handleMarkdownAnnotationsSave(client *wsClient, msg *protocol.MarkdownAnnotationsSaveMessage) {
	handler := newAnnotationDraftHandler(d, client, markdownAnnotationDraftAccessors(d.store), "path",
		func(result annotationDraftResult[protocol.MarkdownAnnotation]) protocol.MarkdownAnnotationsSaveResultMessage {
			return protocol.MarkdownAnnotationsSaveResultMessage{
				Event:       protocol.EventMarkdownAnnotationsSaveResult,
				RequestID:   msg.RequestID,
				Path:        result.key,
				WorkspaceID: msg.WorkspaceID,
				Generation:  result.generation,
				Success:     result.success,
				Stale:       result.stale,
				Error:       result.err,
			}
		})
	handler.save("markdown_annotations_save", msg.Path, msg.Annotations, "", msg.Generation)
}

// handleMarkdownAnnotationsClear tombstones the draft for a path at the given
// generation (idempotent, works on a missing row) and replies with the new
// generation floor. Today only the sidebar "clear all" calls it; PR6's
// clear-on-send reuses the same primitive.
func (d *Daemon) handleMarkdownAnnotationsClear(client *wsClient, msg *protocol.MarkdownAnnotationsClearMessage) {
	handler := newAnnotationDraftHandler(d, client, markdownAnnotationDraftAccessors(d.store), "path",
		func(result annotationDraftResult[protocol.MarkdownAnnotation]) protocol.MarkdownAnnotationsClearResultMessage {
			return protocol.MarkdownAnnotationsClearResultMessage{
				Event:       protocol.EventMarkdownAnnotationsClearResult,
				RequestID:   msg.RequestID,
				Path:        result.key,
				WorkspaceID: msg.WorkspaceID,
				Generation:  result.generation,
				Success:     result.success,
				Error:       result.err,
			}
		})
	handler.clear("markdown_annotations_clear", msg.Path, msg.Generation)
}

// decodeMarkdownAnnotations unmarshals a stored draft blob into protocol
// values, treating empty as an empty list.
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
