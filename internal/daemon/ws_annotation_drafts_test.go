package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func annotationDraftResultBytes(t *testing.T, invoke func(*wsClient)) []byte {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 1)}
	invoke(client)
	select {
	case message := <-client.send:
		return message.payload
	default:
		t.Fatal("no annotation draft result was sent")
		return nil
	}
}

func assertAnnotationDraftResultBytes(t *testing.T, want string, invoke func(*wsClient)) {
	t.Helper()
	if got := string(annotationDraftResultBytes(t, invoke)); got != want {
		t.Fatalf("result bytes:\n got: %s\nwant: %s", got, want)
	}
}

func TestSessionAnnotationDraftAdapterPreservesResultBytes(t *testing.T) {
	d := annotationDaemon(t)

	t.Run("save", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"event":"session_annotations_save_result","generation":1,"request_id":"session-save","session_id":"session-1","success":true}`,
			func(client *wsClient) {
				d.handleSessionAnnotationsSave(client, &protocol.SessionAnnotationsSaveMessage{
					SessionID: "  session-1  ", RequestID: "session-save", Generation: 1,
				})
			})
	})

	t.Run("stale save", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"event":"session_annotations_save_result","generation":1,"request_id":"session-stale","session_id":"session-1","stale":true,"success":false}`,
			func(client *wsClient) {
				d.handleSessionAnnotationsSave(client, &protocol.SessionAnnotationsSaveMessage{
					SessionID: "session-1", RequestID: "session-stale", Generation: 1,
				})
			})
	})

	t.Run("get", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"annotations":[],"event":"session_annotations_get_result","generation":1,"request_id":"session-get","session_id":"session-1","success":true}`,
			func(client *wsClient) {
				d.handleSessionAnnotationsGet(client, &protocol.SessionAnnotationsGetMessage{
					SessionID: "session-1", RequestID: "session-get",
				})
			})
	})

	t.Run("clear", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"event":"session_annotations_clear_result","generation":2,"request_id":"session-clear","session_id":"session-1","success":true}`,
			func(client *wsClient) {
				d.handleSessionAnnotationsClear(client, &protocol.SessionAnnotationsClearMessage{
					SessionID: "session-1", RequestID: "session-clear", Generation: 2,
				})
			})
	})

	t.Run("empty key", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"annotations":[],"error":"session_annotations_get: session_id is required","event":"session_annotations_get_result","generation":0,"request_id":"session-empty","session_id":"","success":false}`,
			func(client *wsClient) {
				d.handleSessionAnnotationsGet(client, &protocol.SessionAnnotationsGetMessage{
					SessionID: "   ", RequestID: "session-empty",
				})
			})
	})
}

func TestMarkdownAnnotationDraftAdapterPreservesResultBytes(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	const workspaceID = "workspace-1"

	t.Run("save", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"event":"markdown_annotations_save_result","generation":1,"path":"/tmp/plan.md","request_id":"markdown-save","success":true,"workspace_id":"workspace-1"}`,
			func(client *wsClient) {
				d.handleMarkdownAnnotationsSave(client, &protocol.MarkdownAnnotationsSaveMessage{
					Path: "  /tmp/plan.md  ", RequestID: "markdown-save", WorkspaceID: workspaceID, Generation: 1,
				})
			})
	})

	t.Run("stale save", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"event":"markdown_annotations_save_result","generation":1,"path":"/tmp/plan.md","request_id":"markdown-stale","stale":true,"success":false,"workspace_id":"workspace-1"}`,
			func(client *wsClient) {
				d.handleMarkdownAnnotationsSave(client, &protocol.MarkdownAnnotationsSaveMessage{
					Path: "/tmp/plan.md", RequestID: "markdown-stale", WorkspaceID: workspaceID, Generation: 1,
				})
			})
	})

	t.Run("get", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"annotations":[],"event":"markdown_annotations_get_result","generation":1,"path":"/tmp/plan.md","request_id":"markdown-get","success":true,"workspace_id":"workspace-1"}`,
			func(client *wsClient) {
				d.handleMarkdownAnnotationsGet(client, &protocol.MarkdownAnnotationsGetMessage{
					Path: "/tmp/plan.md", RequestID: "markdown-get", WorkspaceID: workspaceID,
				})
			})
	})

	t.Run("clear", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"event":"markdown_annotations_clear_result","generation":2,"path":"/tmp/plan.md","request_id":"markdown-clear","success":true,"workspace_id":"workspace-1"}`,
			func(client *wsClient) {
				d.handleMarkdownAnnotationsClear(client, &protocol.MarkdownAnnotationsClearMessage{
					Path: "/tmp/plan.md", RequestID: "markdown-clear", WorkspaceID: workspaceID, Generation: 2,
				})
			})
	})

	t.Run("empty key", func(t *testing.T) {
		assertAnnotationDraftResultBytes(t,
			`{"annotations":[],"error":"markdown_annotations_get: path is required","event":"markdown_annotations_get_result","generation":0,"path":"","request_id":"markdown-empty","success":false,"workspace_id":"workspace-1"}`,
			func(client *wsClient) {
				d.handleMarkdownAnnotationsGet(client, &protocol.MarkdownAnnotationsGetMessage{
					Path: "   ", RequestID: "markdown-empty", WorkspaceID: workspaceID,
				})
			})
	})
}
