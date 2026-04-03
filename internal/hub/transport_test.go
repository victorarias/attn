package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestRemoteTransportReadLimitAllowsLargeMessages(t *testing.T) {
	payload := strings.Repeat("x", 64<<10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Errorf("Write() error = %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(remoteWSMessageReadLimit)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(data) != payload {
		t.Fatalf("Read() payload mismatch: got %d bytes, want %d", len(data), len(payload))
	}
}
