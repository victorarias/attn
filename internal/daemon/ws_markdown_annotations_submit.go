package daemon

import (
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Submit statuses on the wire (plain strings in the protocol).
const (
	markdownSubmitStatusDelivered = "delivered"
	markdownSubmitStatusSkipped   = "skipped_pending_approval"
	markdownSubmitStatusError     = "error"
)

// handleMarkdownAnnotationsSubmit formats the persisted annotation draft for
// a path into the feedback payload and delivers it into the target session's
// PTY via typeDoorbell (bracketed paste + Enter, atomic under doorbellMu).
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
	path := strings.TrimSpace(msg.Path)
	target := strings.TrimSpace(msg.TargetSessionID)
	result := protocol.MarkdownAnnotationsSubmitResultMessage{
		Event:           protocol.EventMarkdownAnnotationsSubmitResult,
		RequestID:       msg.RequestID,
		Path:            path,
		TargetSessionID: target,
		Status:          markdownSubmitStatusError,
	}
	fail := func(errText string) {
		result.Error = protocol.Ptr(errText)
		d.sendToClient(client, result)
	}
	if path == "" {
		fail("markdown_annotations_submit: path is required")
		return
	}
	if target == "" {
		fail("markdown_annotations_submit: target_session_id is required")
		return
	}
	draft, err := d.store.GetMarkdownAnnotationDraft(path)
	if err != nil {
		d.logf("markdown_annotations_submit: %s: %v", path, err)
		fail(err.Error())
		return
	}
	annotations, err := decodeMarkdownAnnotations(draft.Annotations)
	if err != nil {
		d.logf("markdown_annotations_submit: %s: corrupt stored draft: %v", path, err)
		fail("stored annotation draft is corrupt: " + err.Error())
		return
	}
	if len(annotations) == 0 {
		fail("no annotations to send")
		return
	}
	session := d.store.Get(target)
	if session == nil {
		fail("session not found: " + target)
		return
	}
	// UX pre-check; typeDoorbell re-checks the state under doorbellMu — that
	// in-lock check is the fence, this one just avoids formatting for nothing.
	if !isNudgeDeliveryAllowed(string(session.State)) {
		result.Status = markdownSubmitStatusSkipped
		d.sendToClient(client, result)
		return
	}
	orphaned := make(map[string]bool, len(msg.OrphanedIds))
	for _, id := range msg.OrphanedIds {
		orphaned[id] = true
	}
	payload := formatMarkdownAnnotationPayload(path, annotations, orphaned)
	if err := d.typeDoorbell(target, payload); err != nil {
		if errors.Is(err, errDoorbellBlockedByApproval) {
			result.Status = markdownSubmitStatusSkipped
			d.sendToClient(client, result)
			return
		}
		d.logf("markdown_annotations_submit: %s -> %s: delivery failed: %v", path, target, err)
		fail(err.Error())
		return
	}
	// Delivered. Tombstone the draft at the generation we read so any
	// straggling debounced save with generation <= floor is rejected (the
	// PR5 resurrection guard). A clear failure after successful delivery
	// still reports delivered — never risk a re-delivery — but carries an
	// error so the UI can re-hydrate instead of assuming an empty draft.
	result.Success = true
	result.Status = markdownSubmitStatusDelivered
	if err := d.store.ClearMarkdownAnnotationDraft(path, draft.Generation, time.Now()); err != nil {
		d.logf("markdown_annotations_submit: %s: delivered but clearing drafts failed: %v", path, err)
		result.Error = protocol.Ptr("delivered; failed to clear drafts: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	if fresh, err := d.store.GetMarkdownAnnotationDraft(path); err == nil {
		result.Generation = protocol.Ptr(fresh.Generation)
	} else {
		d.logf("markdown_annotations_submit: %s: reading post-clear floor: %v", path, err)
	}
	d.sendToClient(client, result)
}
