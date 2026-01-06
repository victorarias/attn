package transport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockTransport_BasicFlow(t *testing.T) {
	transport := NewMockTransport().
		AddMessage(map[string]any{"type": "init", "session_id": "test-123"}).
		AddMessage(map[string]any{"type": "assistant", "text": "Hello"}).
		AddMessage(map[string]any{"type": "result", "success": true})

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Should be ready after connect
	if !transport.IsReady() {
		t.Error("Transport should be ready after Connect")
	}

	// Collect all messages
	var messages []map[string]any
	for msg := range transport.Messages() {
		messages = append(messages, msg)
	}

	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}

	// Verify message content
	if messages[0]["type"] != "init" {
		t.Errorf("First message type should be 'init', got %v", messages[0]["type"])
	}
	if messages[1]["type"] != "assistant" {
		t.Errorf("Second message type should be 'assistant', got %v", messages[1]["type"])
	}
	if messages[2]["type"] != "result" {
		t.Errorf("Third message type should be 'result', got %v", messages[2]["type"])
	}
}

func TestMockTransport_WriteRecording(t *testing.T) {
	transport := NewMockTransport().
		AddMessage(map[string]any{"type": "init"})

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Write some data
	transport.Write(`{"type": "user", "content": "hello"}`)
	transport.Write(`{"type": "user", "content": "world"}`)

	// Drain messages
	for range transport.Messages() {
	}

	// Check writes were recorded
	writes := transport.GetWrites()
	if len(writes) != 2 {
		t.Errorf("Expected 2 writes, got %d", len(writes))
	}

	// Check writes as JSON
	jsonWrites, err := transport.GetWritesAsJSON()
	if err != nil {
		t.Fatalf("GetWritesAsJSON failed: %v", err)
	}
	if len(jsonWrites) != 2 {
		t.Errorf("Expected 2 JSON writes, got %d", len(jsonWrites))
	}
	if jsonWrites[0]["content"] != "hello" {
		t.Errorf("First write content should be 'hello', got %v", jsonWrites[0]["content"])
	}
}

func TestMockTransport_ConnectError(t *testing.T) {
	expectedErr := errors.New("connection refused")
	transport := NewMockTransport().SetConnectError(expectedErr)

	ctx := context.Background()
	err := transport.Connect(ctx)
	if err == nil {
		t.Fatal("Expected Connect to fail")
	}
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
}

func TestMockTransport_ErrorInjection(t *testing.T) {
	expectedErr := errors.New("API error")
	transport := NewMockTransport().
		AddMessage(map[string]any{"type": "init"}).
		AddMessage(map[string]any{"type": "assistant", "text": "Hello"}).
		AddMessage(map[string]any{"type": "result"}). // This won't be sent
		InjectErrorAtMessage(2, expectedErr)

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var messages []map[string]any
	for msg := range transport.Messages() {
		messages = append(messages, msg)
	}

	// Should have 3 messages: init, assistant, error
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages (including error), got %d", len(messages))
	}

	// Last message should be an error
	lastMsg := messages[len(messages)-1]
	if lastMsg["type"] != "error" {
		t.Errorf("Last message should be error, got %v", lastMsg["type"])
	}
	if lastMsg["error"] != expectedErr.Error() {
		t.Errorf("Error message mismatch, got %v", lastMsg["error"])
	}
}

func TestMockTransport_Delays(t *testing.T) {
	delay := 50 * time.Millisecond
	transport := NewMockTransport().
		AddMessage(map[string]any{"type": "init"}).
		AddMessageWithDelay(map[string]any{"type": "delayed"}, delay)

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	start := time.Now()
	var messages []map[string]any
	for msg := range transport.Messages() {
		messages = append(messages, msg)
	}
	elapsed := time.Since(start)

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	// Should have taken at least the delay time
	if elapsed < delay {
		t.Errorf("Expected at least %v delay, got %v", delay, elapsed)
	}
}

func TestMockTransport_Cancellation(t *testing.T) {
	// Create transport with many delayed messages
	transport := CancelableSequence("test-session", 100, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is always called to avoid context leak
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Read a few messages then cancel
	msgCount := 0
	for range transport.Messages() {
		msgCount++
		if msgCount >= 3 {
			cancel() // Cancel early to test cancellation behavior
			break
		}
	}

	// Drain any remaining messages (should be none or few)
	time.Sleep(50 * time.Millisecond)

	// Transport should not be ready after close
	transport.Close()
	if transport.IsReady() {
		t.Error("Transport should not be ready after Close")
	}
}

func TestMockTransport_Close(t *testing.T) {
	transport := NewMockTransport().
		AddMessage(map[string]any{"type": "init"})

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Close before reading
	if err := transport.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Should not be ready
	if transport.IsReady() {
		t.Error("Transport should not be ready after Close")
	}

	// Double close should be safe
	if err := transport.Close(); err != nil {
		t.Fatalf("Double Close failed: %v", err)
	}
}

func TestMockTransport_WriteBeforeConnect(t *testing.T) {
	transport := NewMockTransport()

	err := transport.Write("test")
	if err == nil {
		t.Fatal("Write before Connect should fail")
	}

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Error("Error should be TransportError")
	}
}

func TestFixtureBuilder_SimpleReviewSequence(t *testing.T) {
	transport := SimpleReviewSequence("review-123")

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var messages []map[string]any
	for msg := range transport.Messages() {
		messages = append(messages, msg)
	}

	// Should have: init, text, tool_use, text, tool_use, text, tool_use, text, result = 9 messages
	if len(messages) < 5 {
		t.Errorf("Expected at least 5 messages in review sequence, got %d", len(messages))
	}

	// First message should be init
	if messages[0]["type"] != "system" {
		t.Errorf("First message should be system (init), got %v", messages[0]["type"])
	}

	// Last message should be result
	if messages[len(messages)-1]["type"] != "result" {
		t.Errorf("Last message should be result, got %v", messages[len(messages)-1]["type"])
	}
}

func TestFixtureBuilder_StreamingTextSequence(t *testing.T) {
	chunks := []string{"Hello ", "world ", "from ", "Claude!"}
	transport := StreamingTextSequence("stream-123", chunks, 10*time.Millisecond)

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var messages []map[string]any
	for msg := range transport.Messages() {
		messages = append(messages, msg)
	}

	// init + 4 chunks + result = 6
	if len(messages) != 6 {
		t.Errorf("Expected 6 messages, got %d", len(messages))
	}
}

func TestFixtureBuilder_ErrorMidStream(t *testing.T) {
	expectedErr := errors.New("rate limit exceeded")
	transport := ErrorMidStreamSequence("error-123", 2, expectedErr)

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var messages []map[string]any
	for msg := range transport.Messages() {
		messages = append(messages, msg)
	}

	// Should have: init, assistant, error = 3
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}

	// Last should be error
	if messages[2]["type"] != "error" {
		t.Errorf("Last message should be error, got %v", messages[2]["type"])
	}
}
