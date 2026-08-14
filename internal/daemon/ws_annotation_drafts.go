package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Submit statuses on the wire (plain strings in the protocol).
const (
	annotationSubmitStatusDelivered = "delivered"
	annotationSubmitStatusSkipped   = "skipped_pending_approval"
	annotationSubmitStatusError     = "error"
)

type annotationDraft struct {
	annotations string
	note        string
	generation  int
}

type annotationDraftAccessors struct {
	get   func(string) (annotationDraft, error)
	save  func(string, string, string, int, time.Time) error
	clear func(string, int, time.Time) error
}

type annotationDraftResult[T any] struct {
	key         string
	annotations []T
	note        *string
	generation  int
	success     bool
	stale       *bool
	err         *string
}

type annotationDraftHandler[T any] struct {
	daemon    *Daemon
	accessors annotationDraftAccessors
	keyField  string
	send      func(annotationDraftResult[T])
}

func newAnnotationDraftHandler[T, R any](
	d *Daemon,
	client *wsClient,
	accessors annotationDraftAccessors,
	keyField string,
	buildResult func(annotationDraftResult[T]) R,
) annotationDraftHandler[T] {
	return annotationDraftHandler[T]{
		daemon:    d,
		accessors: accessors,
		keyField:  keyField,
		send: func(result annotationDraftResult[T]) {
			d.sendToClient(client, buildResult(result))
		},
	}
}

func (h annotationDraftHandler[T]) get(operation, rawKey string, decode func(string) ([]T, error)) {
	result := annotationDraftResult[T]{
		key:         strings.TrimSpace(rawKey),
		annotations: []T{},
	}
	if result.key == "" {
		result.err = protocol.Ptr(fmt.Sprintf("%s: %s is required", operation, h.keyField))
		h.send(result)
		return
	}
	draft, err := h.accessors.get(result.key)
	if err != nil {
		h.daemon.logf("%s: %s: %v", operation, result.key, err)
		result.err = protocol.Ptr(err.Error())
		h.send(result)
		return
	}
	annotations, err := decode(draft.annotations)
	if err != nil {
		h.daemon.logf("%s: %s: corrupt stored draft: %v", operation, result.key, err)
		result.err = protocol.Ptr("stored annotation draft is corrupt: " + err.Error())
		h.send(result)
		return
	}
	result.success = true
	result.annotations = annotations
	if draft.note != "" {
		result.note = protocol.Ptr(draft.note)
	}
	result.generation = draft.generation
	h.send(result)
}

func (h annotationDraftHandler[T]) save(operation, rawKey string, annotations []T, note string, generation int) {
	result := annotationDraftResult[T]{
		key:        strings.TrimSpace(rawKey),
		generation: generation,
	}
	if result.key == "" {
		result.err = protocol.Ptr(fmt.Sprintf("%s: %s is required", operation, h.keyField))
		h.send(result)
		return
	}
	annotationsJSON, err := json.Marshal(annotations)
	if err != nil {
		result.err = protocol.Ptr(operation + ": encoding annotations: " + err.Error())
		h.send(result)
		return
	}
	err = h.accessors.save(result.key, string(annotationsJSON), note, generation, time.Now())
	if errors.Is(err, store.ErrStaleAnnotationSave) {
		h.daemon.logf("%s: %s: stale save at generation %d rejected", operation, result.key, generation)
		result.stale = protocol.Ptr(true)
		h.send(result)
		return
	}
	if err != nil {
		h.daemon.logf("%s: %s: %v", operation, result.key, err)
		result.err = protocol.Ptr(err.Error())
		h.send(result)
		return
	}
	result.success = true
	h.send(result)
}

func (h annotationDraftHandler[T]) clear(operation, rawKey string, generation int) {
	result := annotationDraftResult[T]{
		key:        strings.TrimSpace(rawKey),
		generation: generation,
	}
	if result.key == "" {
		result.err = protocol.Ptr(fmt.Sprintf("%s: %s is required", operation, h.keyField))
		h.send(result)
		return
	}
	if err := h.accessors.clear(result.key, generation, time.Now()); err != nil {
		h.daemon.logf("%s: %s: %v", operation, result.key, err)
		result.err = protocol.Ptr(err.Error())
		h.send(result)
		return
	}
	draft, err := h.accessors.get(result.key)
	if err != nil {
		h.daemon.logf("%s: %s: reading floor: %v", operation, result.key, err)
		result.err = protocol.Ptr(err.Error())
		h.send(result)
		return
	}
	result.success = true
	result.generation = draft.generation
	h.send(result)
}

func sessionAnnotationDraftAccessors(s *store.Store) annotationDraftAccessors {
	return annotationDraftAccessors{
		get: func(key string) (annotationDraft, error) {
			draft, err := s.GetSessionAnnotationDraft(key)
			if err != nil {
				return annotationDraft{}, err
			}
			return annotationDraft{
				annotations: draft.Annotations,
				note:        draft.Note,
				generation:  draft.Generation,
			}, nil
		},
		save:  s.SaveSessionAnnotationDraft,
		clear: s.ClearSessionAnnotationDraft,
	}
}

func markdownAnnotationDraftAccessors(s *store.Store) annotationDraftAccessors {
	return annotationDraftAccessors{
		get: func(key string) (annotationDraft, error) {
			draft, err := s.GetMarkdownAnnotationDraft(key)
			if err != nil {
				return annotationDraft{}, err
			}
			return annotationDraft{
				annotations: draft.Annotations,
				generation:  draft.Generation,
			}, nil
		},
		save: func(key, annotations, _ string, generation int, now time.Time) error {
			return s.SaveMarkdownAnnotationDraft(key, annotations, generation, now)
		},
		clear: s.ClearMarkdownAnnotationDraft,
	}
}
