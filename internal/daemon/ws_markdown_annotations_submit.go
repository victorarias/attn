package daemon

import (
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// handleMarkdownAnnotationsSubmit formats the persisted annotation draft for
// a document and delivers it to exactly one typed destination: a session via
// typeDoorbell, or the source seed's own log as a note.
//
// Drafts are tombstone-cleared ONLY after a successful delivery; every other
// outcome (validation error, unknown session, pending_approval skip, PTY
// write failure) leaves them intact so the user can retry.
//
// Read→deliver→clear race: a save landing between the draft read and the
// clear is cleared too (ClearMarkdownAnnotationDraft tombstones at
// max(stored, given)). The window is milliseconds, the annotation UI is
// single-writer, and the client flushes its debounced save before submitting
// — accepted.
func (d *Daemon) handleMarkdownAnnotationsSubmit(client *wsClient, msg *protocol.MarkdownAnnotationsSubmitMessage) {
	targetSession := strings.TrimSpace(protocol.Deref(msg.TargetSessionID))
	targetSeed := strings.TrimSpace(protocol.Deref(msg.TargetSeedID))
	source, sourceErr := d.resolveAnnotationDocumentSource(msg.DocumentUri, msg.SourceKind, msg.WorkspaceID, msg.Path, msg.SeedID)
	workspaceID, path, seedID := annotationSourcePointers(source)
	result := protocol.MarkdownAnnotationsSubmitResultMessage{
		Event:       protocol.EventMarkdownAnnotationsSubmitResult,
		RequestID:   msg.RequestID,
		DocumentUri: source.documentURI,
		SourceKind:  source.kind,
		WorkspaceID: workspaceID,
		Path:        path,
		SeedID:      seedID,
		Status:      annotationSubmitStatusError,
	}
	if targetSession != "" {
		result.TargetSessionID = protocol.Ptr(targetSession)
	}
	if targetSeed != "" {
		result.TargetSeedID = protocol.Ptr(targetSeed)
	}
	fail := func(errText string) {
		result.Error = protocol.Ptr(errText)
		d.sendToClient(client, result)
	}
	if sourceErr != nil {
		fail("markdown_annotations_submit: " + sourceErr.Error())
		return
	}
	if (targetSession == "") == (targetSeed == "") {
		fail("markdown_annotations_submit: exactly one of target_session_id or target_seed_id is required")
		return
	}
	if targetSeed != "" && (source.kind != annotationSourceSeed || targetSeed != source.seedID) {
		fail("markdown_annotations_submit: target_seed_id must match the seed document source")
		return
	}
	draft, err := d.store.GetMarkdownAnnotationDraft(source.draftKey)
	if err != nil {
		d.logf("markdown_annotations_submit: %s: %v", source.draftKey, err)
		fail(err.Error())
		return
	}
	annotations, err := decodeMarkdownAnnotations(draft.Annotations)
	if err != nil {
		d.logf("markdown_annotations_submit: %s: corrupt stored draft: %v", source.draftKey, err)
		fail("stored annotation draft is corrupt: " + err.Error())
		return
	}
	if len(annotations) == 0 {
		fail("no annotations to send")
		return
	}
	orphaned := make(map[string]bool, len(msg.OrphanedIds))
	for _, id := range msg.OrphanedIds {
		orphaned[id] = true
	}
	payload := formatMarkdownAnnotationPayload(source, annotations, orphaned)
	if targetSession != "" {
		session := d.store.Get(targetSession)
		if session == nil {
			fail("session not found: " + targetSession)
			return
		}
		// UX pre-check; typeDoorbell re-checks under doorbellMu — that in-lock
		// check is the fence, this one only avoids formatting for nothing.
		if !isNudgeDeliveryAllowed(string(session.State)) {
			result.Status = annotationSubmitStatusSkipped
			d.sendToClient(client, result)
			return
		}
		if err := d.typeDoorbell(targetSession, payload); err != nil {
			if errors.Is(err, errDoorbellBlockedByApproval) {
				result.Status = annotationSubmitStatusSkipped
				d.sendToClient(client, result)
				return
			}
			d.logf("markdown_annotations_submit: %s -> %s: delivery failed: %v", source.draftKey, targetSession, err)
			fail(err.Error())
			return
		}
		result.Status = annotationSubmitStatusDelivered
	} else {
		if _, err := d.appendSeedNote(targetSeed, payload, "", "", "", nil); err != nil {
			d.logf("markdown_annotations_submit: %s -> seed %s: note failed: %v", source.draftKey, targetSeed, err)
			fail(err.Error())
			return
		}
		result.Status = annotationSubmitStatusNoted
	}
	// Delivered or noted. Tombstone the draft at the generation we read so any
	// straggling debounced save with generation <= floor is rejected (the
	// PR5 resurrection guard). A clear failure after the destination accepted
	// the payload still reports success — never risk duplicate delivery.
	result.Success = true
	if err := d.store.ClearMarkdownAnnotationDraft(source.draftKey, draft.Generation, time.Now()); err != nil {
		verb := result.Status
		d.logf("markdown_annotations_submit: %s: %s but clearing drafts failed: %v", source.draftKey, verb, err)
		result.Error = protocol.Ptr(verb + "; failed to clear drafts: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	if fresh, err := d.store.GetMarkdownAnnotationDraft(source.draftKey); err == nil {
		result.Generation = protocol.Ptr(fresh.Generation)
	} else {
		d.logf("markdown_annotations_submit: %s: reading post-clear floor: %v", source.draftKey, err)
	}
	d.sendToClient(client, result)
}
