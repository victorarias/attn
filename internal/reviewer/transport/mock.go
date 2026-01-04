// Package transport provides transport implementations for the reviewer agent.
package transport

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// MockTransport implements the claude-agent-sdk-go Transport interface for testing.
// It allows scripting message sequences with delays and error injection.
type MockTransport struct {
	mu sync.Mutex

	// Scripted messages to return
	messages []ScriptedMessage

	// Channel for delivering messages
	msgChan chan map[string]any

	// Recording of what was written
	writes []string

	// Current position in message sequence
	position int

	// Connection state
	connected bool
	closed    bool

	// Error to return on Connect
	connectError error

	// Error to return at message N (0-indexed)
	errorAtMessage int
	errorToInject  error

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// ScriptedMessage represents a message to be returned by the mock transport.
type ScriptedMessage struct {
	Message map[string]any
	Delay   time.Duration
}

// NewMockTransport creates a new mock transport.
func NewMockTransport() *MockTransport {
	return &MockTransport{
		messages:       make([]ScriptedMessage, 0),
		writes:         make([]string, 0),
		msgChan:        make(chan map[string]any, 100),
		errorAtMessage: -1, // No error by default
	}
}

// AddMessage adds a scripted message to the sequence.
func (t *MockTransport) AddMessage(msg map[string]any) *MockTransport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.messages = append(t.messages, ScriptedMessage{Message: msg, Delay: 0})
	return t
}

// AddMessageWithDelay adds a scripted message with a delay.
func (t *MockTransport) AddMessageWithDelay(msg map[string]any, delay time.Duration) *MockTransport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.messages = append(t.messages, ScriptedMessage{Message: msg, Delay: delay})
	return t
}

// SetConnectError sets an error to return on Connect.
func (t *MockTransport) SetConnectError(err error) *MockTransport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectError = err
	return t
}

// InjectErrorAtMessage sets an error to inject at message N (0-indexed).
func (t *MockTransport) InjectErrorAtMessage(n int, err error) *MockTransport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorAtMessage = n
	t.errorToInject = err
	return t
}

// GetWrites returns all strings that were written to the transport.
func (t *MockTransport) GetWrites() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]string, len(t.writes))
	copy(result, t.writes)
	return result
}

// GetWritesAsJSON parses all writes as JSON and returns them.
func (t *MockTransport) GetWritesAsJSON() ([]map[string]any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]map[string]any, 0, len(t.writes))
	for _, w := range t.writes {
		var m map[string]any
		if err := json.Unmarshal([]byte(w), &m); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, nil
}

// --- Transport interface implementation ---

// Connect establishes the connection.
func (t *MockTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connectError != nil {
		return t.connectError
	}

	t.connected = true
	t.ctx, t.cancel = context.WithCancel(ctx)

	// Start goroutine to send scripted messages
	go t.sendMessages()

	return nil
}

// sendMessages sends scripted messages with delays.
func (t *MockTransport) sendMessages() {
	for i, sm := range t.messages {
		// Check for cancellation
		select {
		case <-t.ctx.Done():
			close(t.msgChan)
			return
		default:
		}

		// Check for error injection
		t.mu.Lock()
		if i == t.errorAtMessage && t.errorToInject != nil {
			t.mu.Unlock()
			// Send error as a message with error field
			t.msgChan <- map[string]any{
				"type":  "error",
				"error": t.errorToInject.Error(),
			}
			close(t.msgChan)
			return
		}
		t.mu.Unlock()

		// Apply delay
		if sm.Delay > 0 {
			select {
			case <-t.ctx.Done():
				close(t.msgChan)
				return
			case <-time.After(sm.Delay):
			}
		}

		// Send message
		select {
		case <-t.ctx.Done():
			close(t.msgChan)
			return
		case t.msgChan <- sm.Message:
			t.mu.Lock()
			t.position = i + 1
			t.mu.Unlock()
		}
	}

	// All messages sent, close channel
	close(t.msgChan)
}

// Close terminates the connection and cleans up resources.
func (t *MockTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.closed = true
	t.connected = false

	if t.cancel != nil {
		t.cancel()
	}

	return nil
}

// Write sends data to the CLI.
func (t *MockTransport) Write(data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return &TransportError{Message: "not connected"}
	}

	t.writes = append(t.writes, data)
	return nil
}

// EndInput signals that no more input will be sent.
func (t *MockTransport) EndInput() error {
	// No-op for mock
	return nil
}

// Messages returns a channel of parsed JSON messages from the CLI.
func (t *MockTransport) Messages() <-chan map[string]any {
	return t.msgChan
}

// IsReady returns true if the transport is connected and ready.
func (t *MockTransport) IsReady() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected && !t.closed
}

// TransportError represents a transport error.
type TransportError struct {
	Message string
}

func (e *TransportError) Error() string {
	return e.Message
}
