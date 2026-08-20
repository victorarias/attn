package daemon

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Terminal annotations persist per session: whole-list saves under a monotonic
// generation with clear as a tombstone, the ordering rule implemented once in
// store/annotation_drafts.go. A save below the floor returns stale=true,
// success=false and the client re-hydrates. No broadcast events by design —
// live cross-pane sync is not a flow today.

// handleSessionAnnotationsGet replies with a session's persisted annotations.
// generation is the floor, so a re-mounting client seeds past an earlier clear.
func (d *Daemon) handleSessionAnnotationsGet(client *wsClient, msg *protocol.SessionAnnotationsGetMessage) {
	handler := newAnnotationDraftHandler(d, client, sessionAnnotationDraftAccessors(d.store), "session_id",
		func(result annotationDraftResult[protocol.SessionAnnotation]) protocol.SessionAnnotationsGetResultMessage {
			return protocol.SessionAnnotationsGetResultMessage{
				Event:       protocol.EventSessionAnnotationsGetResult,
				RequestID:   msg.RequestID,
				SessionID:   result.key,
				Annotations: result.annotations,
				Note:        result.note,
				Generation:  result.generation,
				Success:     result.success,
				Error:       result.err,
			}
		})
	handler.get("session_annotations_get", msg.SessionID, decodeSessionAnnotations)
}

// handleSessionAnnotationsSave persists the full annotation list for a session.
func (d *Daemon) handleSessionAnnotationsSave(client *wsClient, msg *protocol.SessionAnnotationsSaveMessage) {
	annotations := msg.Annotations
	if annotations == nil {
		annotations = []protocol.SessionAnnotation{}
	}
	handler := newAnnotationDraftHandler(d, client, sessionAnnotationDraftAccessors(d.store), "session_id",
		func(result annotationDraftResult[protocol.SessionAnnotation]) protocol.SessionAnnotationsSaveResultMessage {
			return protocol.SessionAnnotationsSaveResultMessage{
				Event:      protocol.EventSessionAnnotationsSaveResult,
				RequestID:  msg.RequestID,
				SessionID:  result.key,
				Generation: result.generation,
				Success:    result.success,
				Stale:      result.stale,
				Error:      result.err,
			}
		})
	handler.save("session_annotations_save", msg.SessionID, annotations, protocol.Deref(msg.Note), msg.Generation)
}

// handleSessionAnnotationsClear tombstones a session's annotations and replies
// with the new generation floor.
func (d *Daemon) handleSessionAnnotationsClear(client *wsClient, msg *protocol.SessionAnnotationsClearMessage) {
	handler := newAnnotationDraftHandler(d, client, sessionAnnotationDraftAccessors(d.store), "session_id",
		func(result annotationDraftResult[protocol.SessionAnnotation]) protocol.SessionAnnotationsClearResultMessage {
			return protocol.SessionAnnotationsClearResultMessage{
				Event:      protocol.EventSessionAnnotationsClearResult,
				RequestID:  msg.RequestID,
				SessionID:  result.key,
				Generation: result.generation,
				Success:    result.success,
				Error:      result.err,
			}
		})
	handler.clear("session_annotations_clear", msg.SessionID, msg.Generation)
}

// handleSessionAnnotationsSubmit delivers composed annotation feedback through
// typeDoorbell — bracketed paste, the measured gap, then Enter. The daemon owns
// delivery for three reasons only reachable here: the approval guard (Enter in
// pending_approval would answer the prompt, so the submit is refused and the
// client keeps its marks), the PTY write fence, and the gap itself (an Enter in
// the same read as the paste terminator is folded into the pasted text — see
// doorbellSubmitDelay's receipt). Annotations are not cleared here; the client
// composed the payload and clears its own list on a delivered result.
func (d *Daemon) handleSessionAnnotationsSubmit(client *wsClient, msg *protocol.SessionAnnotationsSubmitMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionAnnotationsSubmitResultMessage{
		Event:     protocol.EventSessionAnnotationsSubmitResult,
		RequestID: msg.RequestID,
		SessionID: sessionID,
		Status:    annotationSubmitStatusError,
	}
	fail := func(errText string) {
		result.Error = protocol.Ptr(errText)
		d.sendToClient(client, result)
	}
	if sessionID == "" {
		fail("session_annotations_submit: session_id is required")
		return
	}
	if strings.TrimSpace(msg.Text) == "" {
		fail("session_annotations_submit: text is required")
		return
	}
	if d.store.Get(sessionID) == nil {
		fail("session_annotations_submit: unknown session " + sessionID)
		return
	}
	if err := d.typeDoorbell(sessionID, msg.Text); err != nil {
		if doorbellDeferred(err) {
			result.Status = annotationSubmitStatusSkipped
			d.sendToClient(client, result)
			return
		}
		d.logf("session_annotations_submit: %s: delivery failed: %v", sessionID, err)
		fail(err.Error())
		return
	}
	result.Success = true
	result.Status = annotationSubmitStatusDelivered
	d.sendToClient(client, result)
}

// decodeSessionAnnotations unmarshals a stored draft blob into protocol values,
// treating empty as an empty list.
func decodeSessionAnnotations(raw string) ([]protocol.SessionAnnotation, error) {
	if strings.TrimSpace(raw) == "" {
		return []protocol.SessionAnnotation{}, nil
	}
	var annotations []protocol.SessionAnnotation
	if err := json.Unmarshal([]byte(raw), &annotations); err != nil {
		return nil, err
	}
	if annotations == nil {
		annotations = []protocol.SessionAnnotation{}
	}
	return annotations, nil
}
