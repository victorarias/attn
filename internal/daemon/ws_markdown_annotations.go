package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Markdown annotation drafts are keyed by absolute file path only (no
// workspace): annotations are a property of the document, not the view, so
// the same file docked in two workspaces shows the same drafts. The path is
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
	path := strings.TrimSpace(msg.Path)
	result := protocol.MarkdownAnnotationsGetResultMessage{
		Event:       protocol.EventMarkdownAnnotationsGetResult,
		RequestID:   msg.RequestID,
		Path:        path,
		Annotations: []protocol.MarkdownAnnotation{},
	}
	if path == "" {
		result.Error = protocol.Ptr("markdown_annotations_get: path is required")
		d.sendToClient(client, result)
		return
	}
	draft, err := d.store.GetMarkdownAnnotationDraft(path)
	if err != nil {
		d.logf("markdown_annotations_get: %s: %v", path, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	annotations, err := decodeMarkdownAnnotations(draft.Annotations)
	if err != nil {
		d.logf("markdown_annotations_get: %s: corrupt stored draft: %v", path, err)
		result.Error = protocol.Ptr("stored annotation draft is corrupt: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.Annotations = annotations
	result.Generation = draft.Generation
	d.sendToClient(client, result)
}

// handleMarkdownAnnotationsSave persists the full annotation list for a path.
// A generation at or below the stored generation or the tombstone comes back
// as stale=true, success=false — benign; the client drops its pending list
// and re-hydrates.
func (d *Daemon) handleMarkdownAnnotationsSave(client *wsClient, msg *protocol.MarkdownAnnotationsSaveMessage) {
	path := strings.TrimSpace(msg.Path)
	result := protocol.MarkdownAnnotationsSaveResultMessage{
		Event:      protocol.EventMarkdownAnnotationsSaveResult,
		RequestID:  msg.RequestID,
		Path:       path,
		Generation: msg.Generation,
	}
	if path == "" {
		result.Error = protocol.Ptr("markdown_annotations_save: path is required")
		d.sendToClient(client, result)
		return
	}
	annotationsJSON, err := json.Marshal(msg.Annotations)
	if err != nil {
		result.Error = protocol.Ptr("markdown_annotations_save: encoding annotations: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	err = d.store.SaveMarkdownAnnotationDraft(path, string(annotationsJSON), msg.Generation, time.Now())
	if errors.Is(err, store.ErrStaleMarkdownAnnotationSave) {
		d.logf("markdown_annotations_save: %s: stale save at generation %d rejected", path, msg.Generation)
		result.Stale = protocol.Ptr(true)
		d.sendToClient(client, result)
		return
	}
	if err != nil {
		d.logf("markdown_annotations_save: %s: %v", path, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	d.sendToClient(client, result)
}

// handleMarkdownAnnotationsClear tombstones the draft for a path at the given
// generation (idempotent, works on a missing row) and replies with the new
// generation floor. Today only the sidebar "clear all" calls it; PR6's
// clear-on-send reuses the same primitive.
func (d *Daemon) handleMarkdownAnnotationsClear(client *wsClient, msg *protocol.MarkdownAnnotationsClearMessage) {
	path := strings.TrimSpace(msg.Path)
	result := protocol.MarkdownAnnotationsClearResultMessage{
		Event:      protocol.EventMarkdownAnnotationsClearResult,
		RequestID:  msg.RequestID,
		Path:       path,
		Generation: msg.Generation,
	}
	if path == "" {
		result.Error = protocol.Ptr("markdown_annotations_clear: path is required")
		d.sendToClient(client, result)
		return
	}
	if err := d.store.ClearMarkdownAnnotationDraft(path, msg.Generation, time.Now()); err != nil {
		d.logf("markdown_annotations_clear: %s: %v", path, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	draft, err := d.store.GetMarkdownAnnotationDraft(path)
	if err != nil {
		d.logf("markdown_annotations_clear: %s: reading floor: %v", path, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.Generation = draft.Generation
	d.sendToClient(client, result)
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
