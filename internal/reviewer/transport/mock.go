// Package transport provides transport implementations for the reviewer agent.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
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
	connected     bool
	closed        bool
	channelClosed bool // Tracks if msgChan was closed (by error injection or Close)

	// Error to return on Connect
	connectError error

	// Error to return at message N (0-indexed)
	errorAtMessage int
	errorToInject  error

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Auto-respond to control requests (for SDK integration testing)
	autoRespondToControl bool
	sessionID            string
	mcpServerName        string // Name of MCP server for tool call requests

	// Request ID counter for generating control request IDs
	requestIDCounter int
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

// EnableAutoControlResponse enables automatic responses to SDK control requests.
// This is required for testing with the Claude Agent SDK.
// The mcpServerName should match the name used in the MCP server builder.
func (t *MockTransport) EnableAutoControlResponse(sessionID, mcpServerName string) *MockTransport {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.autoRespondToControl = true
	t.sessionID = sessionID
	t.mcpServerName = mcpServerName
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
	// Pre-compute all messages including control requests to avoid race conditions
	// where the SDK processes the result before we inject control requests
	t.mu.Lock()
	autoRespond := t.autoRespondToControl
	mcpServer := t.mcpServerName
	t.mu.Unlock()

	var allMessages []ScriptedMessage
	for _, sm := range t.messages {
		allMessages = append(allMessages, sm)

		// If auto-respond is enabled and this is a tool_use message,
		// add control requests immediately after
		if autoRespond && mcpServer != "" {
			controlReqs := t.buildControlRequests(sm.Message, mcpServer)
			for _, req := range controlReqs {
				allMessages = append(allMessages, ScriptedMessage{Message: req, Delay: 0})
			}
		}
	}

	for i, sm := range allMessages {
		// Check for cancellation
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		// Check for error injection (only for original messages, not injected control requests)
		t.mu.Lock()
		if i == t.errorAtMessage && t.errorToInject != nil {
			t.mu.Unlock()
			// Send error as a message with error field
			select {
			case t.msgChan <- map[string]any{
				"type":  "error",
				"error": t.errorToInject.Error(),
			}:
			case <-t.ctx.Done():
			}
			// Always close channel on error - errors are terminal
			t.mu.Lock()
			t.channelClosed = true
			t.mu.Unlock()
			close(t.msgChan)
			return
		}
		t.mu.Unlock()

		// Apply delay
		if sm.Delay > 0 {
			select {
			case <-t.ctx.Done():
				return
			case <-time.After(sm.Delay):
			}
		}

		// Send message
		select {
		case <-t.ctx.Done():
			return
		case t.msgChan <- sm.Message:
			t.mu.Lock()
			t.position = i + 1
			t.mu.Unlock()
		}
	}

	// If auto-respond is enabled, keep channel open for control responses
	// Otherwise close it immediately
	t.mu.Lock()
	keepOpen := t.autoRespondToControl
	t.mu.Unlock()

	if keepOpen {
		// Wait for context cancellation - channel will be closed by Close()
		<-t.ctx.Done()
	} else {
		// Close channel when all messages sent (original behavior)
		close(t.msgChan)
	}
}

// buildControlRequests checks if an assistant message contains tool_use blocks
// and returns control_request messages to trigger MCP tool execution.
func (t *MockTransport) buildControlRequests(msg map[string]any, mcpServer string) []map[string]any {
	if msg["type"] != "assistant" {
		return nil
	}

	msgData, ok := msg["message"].(map[string]any)
	if !ok {
		return nil
	}

	content, ok := msgData["content"].([]any)
	if !ok {
		return nil
	}

	var controlReqs []map[string]any

	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] != "tool_use" {
			continue
		}

		toolName, _ := block["name"].(string)
		toolInput, _ := block["input"].(map[string]any)
		if toolInput == nil {
			toolInput = make(map[string]any)
		}

		// Generate request ID
		t.mu.Lock()
		t.requestIDCounter++
		requestID := fmt.Sprintf("mock-req-%d", t.requestIDCounter)
		t.mu.Unlock()

		// Build control_request for MCP tool call
		controlReq := map[string]any{
			"type":       "control_request",
			"request_id": requestID,
			"request": map[string]any{
				"subtype":     "mcp_tool_call",
				"server_name": mcpServer,
				"tool_name":   toolName,
				"input":       toolInput,
			},
		}

		controlReqs = append(controlReqs, controlReq)
	}

	return controlReqs
}

// Close terminates the connection and cleans up resources.
func (t *MockTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}

	t.closed = true
	t.connected = false
	alreadyClosed := t.channelClosed
	t.channelClosed = true
	cancel := t.cancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Give goroutines a moment to exit, then close channel (if not already closed)
	time.Sleep(10 * time.Millisecond)

	if !alreadyClosed {
		// Close channel - use recover in case of race
		func() {
			defer func() { recover() }()
			close(t.msgChan)
		}()
	}

	return nil
}

// Write sends data to the CLI.
func (t *MockTransport) Write(data string) error {
	t.mu.Lock()
	if !t.connected {
		t.mu.Unlock()
		return &TransportError{Message: "not connected"}
	}

	t.writes = append(t.writes, data)
	autoRespond := t.autoRespondToControl
	sessionID := t.sessionID
	t.mu.Unlock()

	// Handle control requests if auto-respond is enabled
	if autoRespond {
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["type"] == "control_request" {
				t.handleControlRequest(msg, sessionID)
			}
		}
	}

	return nil
}

// handleControlRequest processes a control request and injects a response.
func (t *MockTransport) handleControlRequest(msg map[string]any, sessionID string) {
	requestID, _ := msg["request_id"].(string)
	request, _ := msg["request"].(map[string]any)
	if request == nil {
		return
	}

	subtype, _ := request["subtype"].(string)

	var responseData map[string]any

	switch subtype {
	case "initialize":
		// Respond with successful init
		responseData = map[string]any{
			"session_id": sessionID,
			"version":    "1.0.0-mock",
		}

	case "user_message":
		// Acknowledge user message - just echo success
		responseData = map[string]any{
			"acknowledged": true,
		}

	default:
		// Generic success response
		responseData = map[string]any{
			"success": true,
		}
	}

	// Inject control response into message channel
	response := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "response",
			"request_id": requestID,
			"response":   responseData,
		},
	}

	// Send response synchronously - need to ensure it goes before any scripted messages
	t.mu.Lock()
	channelClosed := t.channelClosed
	t.mu.Unlock()

	if !channelClosed {
		// Try to send with timeout - use defer/recover to handle race with channel close
		func() {
			defer func() { recover() }()
			select {
			case t.msgChan <- response:
			case <-t.ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}()
	}
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
