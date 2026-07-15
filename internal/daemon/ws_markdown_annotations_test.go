package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// newMarkdownAnnotationsDaemon returns a test daemon plus a fresh ws client
// channel for driving the markdown annotation handlers directly (the same
// harness style as the notebook/task WS tests).
func newMarkdownAnnotationsDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func mdAnnotationsGet(t *testing.T, d *Daemon, requestID, path string) protocol.MarkdownAnnotationsGetResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleMarkdownAnnotationsGet(client, &protocol.MarkdownAnnotationsGetMessage{
		Cmd:       protocol.CmdMarkdownAnnotationsGet,
		Path:      path,
		RequestID: requestID,
	})
	var msg protocol.MarkdownAnnotationsGetResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func mdAnnotationsSave(t *testing.T, d *Daemon, requestID, path string, annotations []protocol.MarkdownAnnotation, generation int) protocol.MarkdownAnnotationsSaveResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleMarkdownAnnotationsSave(client, &protocol.MarkdownAnnotationsSaveMessage{
		Cmd:         protocol.CmdMarkdownAnnotationsSave,
		Path:        path,
		Annotations: annotations,
		Generation:  generation,
		RequestID:   requestID,
	})
	var msg protocol.MarkdownAnnotationsSaveResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func mdAnnotationsClear(t *testing.T, d *Daemon, requestID, path string, generation int) protocol.MarkdownAnnotationsClearResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleMarkdownAnnotationsClear(client, &protocol.MarkdownAnnotationsClearMessage{
		Cmd:        protocol.CmdMarkdownAnnotationsClear,
		Path:       path,
		Generation: generation,
		RequestID:  requestID,
	})
	var msg protocol.MarkdownAnnotationsClearResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func TestMarkdownAnnotationsGetUnknownPathIsEmptyGenZero(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)

	res := mdAnnotationsGet(t, d, "req-1", "/nowhere/plan.md")
	if !res.Success || res.RequestID != "req-1" {
		t.Fatalf("get = %+v, want success correlated to req-1", res)
	}
	if len(res.Annotations) != 0 || res.Generation != 0 {
		t.Fatalf("get unknown path = %+v, want empty annotations gen 0", res)
	}
}

func TestMarkdownAnnotationsSaveThenGetRoundtripsStructuredFields(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	path := "/tmp/plan.md"

	annotations := []protocol.MarkdownAnnotation{
		{
			ID:   "a1",
			Type: "comment",
			Text: protocol.Ptr("tighten this"),
			Anchor: &protocol.MarkdownAnnotationAnchor{
				BlockID:     "b-3",
				StartLine:   10,
				EndLine:     12,
				Exact:       "the exact text",
				Prefix:      "pre",
				Suffix:      "post",
				Start:       4,
				End:         18,
				ContentHash: "abc123",
			},
			CreatedAt: 1752494400000,
		},
		{
			ID:            "a2",
			Type:          "comment",
			QuickLabelID:  protocol.Ptr("verify-this"),
			QuickLabelTip: protocol.Ptr("Verify by reading the code."),
			CreatedAt:     1752494401000,
		},
		{ID: "a3", Type: "global", Text: protocol.Ptr("overall: solid"), CreatedAt: 1752494402000},
	}

	save := mdAnnotationsSave(t, d, "req-s", "  "+path+"  ", annotations, 1)
	if !save.Success || save.Stale != nil || save.Path != path || save.Generation != 1 {
		t.Fatalf("save = %+v, want success on trimmed path at gen 1", save)
	}

	get := mdAnnotationsGet(t, d, "req-g", path)
	if !get.Success || get.Generation != 1 || len(get.Annotations) != 3 {
		t.Fatalf("get = %+v, want 3 annotations at gen 1", get)
	}
	a1 := get.Annotations[0]
	if a1.ID != "a1" || a1.Type != "comment" || a1.Anchor == nil || a1.Anchor.BlockID != "b-3" ||
		a1.Anchor.StartLine != 10 || a1.Anchor.Exact != "the exact text" || a1.CreatedAt != 1752494400000 {
		t.Fatalf("a1 roundtrip = %+v", a1)
	}
	a2 := get.Annotations[1]
	if protocol.Deref(a2.QuickLabelID) != "verify-this" || protocol.Deref(a2.QuickLabelTip) == "" {
		t.Fatalf("a2 quick-label roundtrip = %+v", a2)
	}
	if get.Annotations[2].Type != "global" || get.Annotations[2].Anchor != nil {
		t.Fatalf("a3 global roundtrip = %+v", get.Annotations[2])
	}
}

func TestMarkdownAnnotationsStaleSaveReportsStaleNotError(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	path := "/tmp/plan.md"

	if res := mdAnnotationsSave(t, d, "s1", path, nil, 2); !res.Success {
		t.Fatalf("save gen 2 = %+v, want success", res)
	}
	stale := mdAnnotationsSave(t, d, "s2", path, nil, 2)
	if stale.Success || !protocol.Deref(stale.Stale) || stale.Error != nil {
		t.Fatalf("repeat save gen 2 = %+v, want success=false stale=true with no error", stale)
	}
}

func TestMarkdownAnnotationsClearTombstonesAndReturnsFloor(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	path := "/tmp/plan.md"

	if res := mdAnnotationsSave(t, d, "s1", path, []protocol.MarkdownAnnotation{{ID: "a", Type: "comment", CreatedAt: 1}}, 3); !res.Success {
		t.Fatalf("save gen 3 = %+v, want success", res)
	}
	clear := mdAnnotationsClear(t, d, "c1", path, 4)
	if !clear.Success || clear.Generation != 4 {
		t.Fatalf("clear = %+v, want success with floor 4", clear)
	}
	// The tombstone rejects a save at/below its generation (debounced-save race).
	ghost := mdAnnotationsSave(t, d, "s2", path, []protocol.MarkdownAnnotation{{ID: "ghost", Type: "comment", CreatedAt: 2}}, 4)
	if ghost.Success || !protocol.Deref(ghost.Stale) {
		t.Fatalf("save at tombstone gen = %+v, want stale rejection", ghost)
	}
	get := mdAnnotationsGet(t, d, "g1", path)
	if !get.Success || len(get.Annotations) != 0 || get.Generation != 4 {
		t.Fatalf("get after clear = %+v, want empty list floor 4", get)
	}
	// Past the tombstone, saving works again.
	if res := mdAnnotationsSave(t, d, "s3", path, nil, 5); !res.Success {
		t.Fatalf("save gen 5 after clear = %+v, want success", res)
	}
}

func TestMarkdownAnnotationsEmptyPathIsErrorResultNotDisconnect(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)

	get := mdAnnotationsGet(t, d, "g", "   ")
	if get.Success || get.Error == nil {
		t.Fatalf("get empty path = %+v, want error result", get)
	}
	save := mdAnnotationsSave(t, d, "s", "", nil, 1)
	if save.Success || save.Error == nil {
		t.Fatalf("save empty path = %+v, want error result", save)
	}
	clear := mdAnnotationsClear(t, d, "c", "", 1)
	if clear.Success || clear.Error == nil {
		t.Fatalf("clear empty path = %+v, want error result", clear)
	}
}

func TestMarkdownAnnotationsCorruptStoredDraftIsErrorResult(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	path := "/tmp/plan.md"

	// Corrupt the stored blob directly (bypasses the typed WS parse, which
	// would reject malformed annotation JSON before any handler runs).
	if err := d.store.SaveMarkdownAnnotationDraft(path, "{not json", 1, time.Now()); err != nil {
		t.Fatalf("seed corrupt draft: %v", err)
	}
	get := mdAnnotationsGet(t, d, "g", path)
	if get.Success || get.Error == nil {
		t.Fatalf("get corrupt draft = %+v, want error result", get)
	}
}
