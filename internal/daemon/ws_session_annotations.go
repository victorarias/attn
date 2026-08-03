package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Terminal annotations are persisted per session, keyed by session id, and the
// point of persisting them is that the user's marks are work: they survive the
// turn scrolling away, the pane unmounting, the app quitting, and the machine
// rebooting. Nothing about an annotation lives only in a frontend ref.
//
// Whole-list saves under a monotonic generation, with clear as a tombstone —
// the same ordering rule as markdown annotation drafts, implemented once in
// store/annotation_drafts.go. A save that does not clear the floor comes back
// stale=true, success=false, which the client treats as benign and re-hydrates
// from.
//
// There are deliberately no broadcast events. The writer is the pane that
// renders, and the generation floor makes a second writer re-hydrate rather
// than corrupt the list; live cross-pane sync would need a broadcast and is not
// a flow today.

// handleSessionAnnotationsGet replies with a session's persisted annotations.
// generation is the current floor — max(stored generation, tombstone) — so a
// re-mounting client seeds its counter past a send that already cleared them.
func (d *Daemon) handleSessionAnnotationsGet(client *wsClient, msg *protocol.SessionAnnotationsGetMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionAnnotationsGetResultMessage{
		Event:       protocol.EventSessionAnnotationsGetResult,
		RequestID:   msg.RequestID,
		SessionID:   sessionID,
		Annotations: []protocol.SessionAnnotation{},
	}
	if sessionID == "" {
		result.Error = protocol.Ptr("session_annotations_get: session_id is required")
		d.sendToClient(client, result)
		return
	}
	draft, err := d.store.GetSessionAnnotationDraft(sessionID)
	if err != nil {
		d.logf("session_annotations_get: %s: %v", sessionID, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	annotations, err := decodeSessionAnnotations(draft.Annotations)
	if err != nil {
		d.logf("session_annotations_get: %s: corrupt stored draft: %v", sessionID, err)
		result.Error = protocol.Ptr("stored annotation draft is corrupt: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.Annotations = annotations
	result.Generation = draft.Generation
	d.sendToClient(client, result)
}

// handleSessionAnnotationsSave persists the full annotation list for a session.
func (d *Daemon) handleSessionAnnotationsSave(client *wsClient, msg *protocol.SessionAnnotationsSaveMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionAnnotationsSaveResultMessage{
		Event:      protocol.EventSessionAnnotationsSaveResult,
		RequestID:  msg.RequestID,
		SessionID:  sessionID,
		Generation: msg.Generation,
	}
	if sessionID == "" {
		result.Error = protocol.Ptr("session_annotations_save: session_id is required")
		d.sendToClient(client, result)
		return
	}
	annotations := msg.Annotations
	if annotations == nil {
		annotations = []protocol.SessionAnnotation{}
	}
	annotationsJSON, err := json.Marshal(annotations)
	if err != nil {
		result.Error = protocol.Ptr("session_annotations_save: encoding annotations: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	err = d.store.SaveSessionAnnotationDraft(sessionID, string(annotationsJSON), msg.Generation, time.Now())
	if errors.Is(err, store.ErrStaleAnnotationSave) {
		d.logf("session_annotations_save: %s: stale save at generation %d rejected", sessionID, msg.Generation)
		result.Stale = protocol.Ptr(true)
		d.sendToClient(client, result)
		return
	}
	if err != nil {
		d.logf("session_annotations_save: %s: %v", sessionID, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	d.sendToClient(client, result)
}

// handleSessionAnnotationsClear tombstones a session's annotations and replies
// with the new generation floor.
func (d *Daemon) handleSessionAnnotationsClear(client *wsClient, msg *protocol.SessionAnnotationsClearMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionAnnotationsClearResultMessage{
		Event:      protocol.EventSessionAnnotationsClearResult,
		RequestID:  msg.RequestID,
		SessionID:  sessionID,
		Generation: msg.Generation,
	}
	if sessionID == "" {
		result.Error = protocol.Ptr("session_annotations_clear: session_id is required")
		d.sendToClient(client, result)
		return
	}
	if err := d.store.ClearSessionAnnotationDraft(sessionID, msg.Generation, time.Now()); err != nil {
		d.logf("session_annotations_clear: %s: %v", sessionID, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	draft, err := d.store.GetSessionAnnotationDraft(sessionID)
	if err != nil {
		d.logf("session_annotations_clear: %s: reading floor: %v", sessionID, err)
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.Generation = draft.Generation
	d.sendToClient(client, result)
}

// handleSessionAnnotationsSubmit delivers composed annotation feedback into a
// session through typeDoorbell — bracketed paste, the measured gap, then Enter
// — so "Send all" both types and submits.
//
// The delivery is the daemon's business even though the text is the client's.
// Three things only reachable here make the difference between a submit and a
// paste that happens to end in a carriage return:
//
//   - The approval guard. pending_approval is an annotatable state (an approval
//     prompt is exactly when a user wants to push back), but it is also a state
//     where Enter answers the prompt. typeDoorbell refuses, and the client is
//     told to keep the marks and retry rather than having them spent on a
//     y/n it never meant to answer.
//   - The PTY write fence, which keeps a keystroke racing the gap from landing
//     between the paste and its Enter.
//   - The gap itself. An Enter arriving in the same PTY read as the paste
//     terminator is folded into the pasted text, so the payload sits in the
//     composer and never submits — see doorbellSubmitDelay's receipt.
//
// The annotations are not cleared here. Unlike a markdown draft, whose payload
// the daemon composes from what it stores, the client composed this one and
// clears its own list on a delivered result.
func (d *Daemon) handleSessionAnnotationsSubmit(client *wsClient, msg *protocol.SessionAnnotationsSubmitMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionAnnotationsSubmitResultMessage{
		Event:     protocol.EventSessionAnnotationsSubmitResult,
		RequestID: msg.RequestID,
		SessionID: sessionID,
		Status:    markdownSubmitStatusError,
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
		if errors.Is(err, errDoorbellBlockedByApproval) {
			result.Status = markdownSubmitStatusSkipped
			d.sendToClient(client, result)
			return
		}
		d.logf("session_annotations_submit: %s: delivery failed: %v", sessionID, err)
		fail(err.Error())
		return
	}
	result.Success = true
	result.Status = markdownSubmitStatusDelivered
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
