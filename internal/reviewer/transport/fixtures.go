package transport

import (
	"time"
)

// FixtureBuilder helps construct scripted message sequences for testing.
type FixtureBuilder struct {
	transport *MockTransport
}

// NewFixtureBuilder creates a new fixture builder.
func NewFixtureBuilder() *FixtureBuilder {
	return &FixtureBuilder{
		transport: NewMockTransport(),
	}
}

// Build returns the configured MockTransport.
func (b *FixtureBuilder) Build() *MockTransport {
	return b.transport
}

// WithConnectError sets a connection error.
func (b *FixtureBuilder) WithConnectError(err error) *FixtureBuilder {
	b.transport.SetConnectError(err)
	return b
}

// WithErrorAtMessage injects an error at message N.
func (b *FixtureBuilder) WithErrorAtMessage(n int, err error) *FixtureBuilder {
	b.transport.InjectErrorAtMessage(n, err)
	return b
}

// --- Init Response ---

// AddInitResponse adds a successful initialization response.
func (b *FixtureBuilder) AddInitResponse(sessionID string) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": sessionID,
		"version":    "1.0.0",
	})
	return b
}

// --- Assistant Messages ---

// AddAssistantText adds an assistant message with text content.
func (b *FixtureBuilder) AddAssistantText(text string) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude-sonnet-4-5",
			"content": []any{
				map[string]any{"type": "text", "text": text},
			},
		},
	})
	return b
}

// AddAssistantTextWithDelay adds an assistant message with text content and delay.
func (b *FixtureBuilder) AddAssistantTextWithDelay(text string, delay time.Duration) *FixtureBuilder {
	b.transport.AddMessageWithDelay(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude-sonnet-4-5",
			"content": []any{
				map[string]any{"type": "text", "text": text},
			},
		},
	}, delay)
	return b
}

// AddAssistantThinking adds an assistant message with thinking content.
func (b *FixtureBuilder) AddAssistantThinking(thinking string) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude-sonnet-4-5",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": thinking, "signature": "mock"},
			},
		},
	})
	return b
}

// --- Tool Use ---

// AddToolUse adds a tool use message.
func (b *FixtureBuilder) AddToolUse(toolID, toolName string, input map[string]any) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude-sonnet-4-5",
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    toolID,
					"name":  toolName,
					"input": input,
				},
			},
		},
	})
	return b
}

// AddToolUseWithDelay adds a tool use message with delay.
func (b *FixtureBuilder) AddToolUseWithDelay(toolID, toolName string, input map[string]any, delay time.Duration) *FixtureBuilder {
	b.transport.AddMessageWithDelay(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude-sonnet-4-5",
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    toolID,
					"name":  toolName,
					"input": input,
				},
			},
		},
	}, delay)
	return b
}

// --- Result Messages ---

// AddResult adds a successful result message.
func (b *FixtureBuilder) AddResult(sessionID string) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type":        "result",
		"subtype":     "success",
		"session_id":  sessionID,
		"is_error":    false,
		"duration_ms": 1000,
		"num_turns":   1,
	})
	return b
}

// AddResultWithDelay adds a successful result message with a delay.
// Use this to give MCP tool handlers time to complete before the result.
func (b *FixtureBuilder) AddResultWithDelay(sessionID string, delay time.Duration) *FixtureBuilder {
	b.transport.AddMessageWithDelay(map[string]any{
		"type":        "result",
		"subtype":     "success",
		"session_id":  sessionID,
		"is_error":    false,
		"duration_ms": 1000,
		"num_turns":   1,
	}, delay)
	return b
}

// AddErrorResult adds an error result message.
func (b *FixtureBuilder) AddErrorResult(sessionID, errorMsg string) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type":        "result",
		"subtype":     "error",
		"session_id":  sessionID,
		"is_error":    true,
		"error":       errorMsg,
		"duration_ms": 500,
		"num_turns":   0,
	})
	return b
}

// --- Control Responses ---

// AddControlResponse adds a control response (e.g., for MCP tool calls).
func (b *FixtureBuilder) AddControlResponse(requestID string, response map[string]any) *FixtureBuilder {
	b.transport.AddMessage(map[string]any{
		"type": "control",
		"response": map[string]any{
			"subtype":    "response",
			"request_id": requestID,
			"response":   response,
		},
	})
	return b
}

// --- Common Scenarios ---

// SimpleReviewSequence creates a sequence simulating a simple review.
// 1. Init
// 2. Get changed files
// 3. Get diff
// 4. Add comment
// 5. Result
func SimpleReviewSequence(sessionID string) *MockTransport {
	return NewFixtureBuilder().
		AddInitResponse(sessionID).
		AddAssistantText("I'll review the changes on this branch.").
		AddToolUse("tool-1", "get_changed_files", map[string]any{}).
		AddAssistantText("Found 2 changed files. Let me examine them.").
		AddToolUse("tool-2", "get_diff", map[string]any{
			"paths": []string{"main.go"},
		}).
		AddAssistantText("I found an issue with error handling.").
		AddToolUse("tool-3", "add_comment", map[string]any{
			"filepath":   "main.go",
			"line_start": 42,
			"line_end":   42,
			"content":    "Consider adding error handling for this operation.",
		}).
		AddAssistantText("Review complete. I found 1 issue that needs attention.").
		AddResult(sessionID).
		Build()
}

// StreamingTextSequence creates a sequence with multiple text chunks (simulating streaming).
func StreamingTextSequence(sessionID string, chunks []string, chunkDelay time.Duration) *MockTransport {
	builder := NewFixtureBuilder().AddInitResponse(sessionID)

	for _, chunk := range chunks {
		builder.AddAssistantTextWithDelay(chunk, chunkDelay)
	}

	return builder.AddResult(sessionID).Build()
}

// ErrorMidStreamSequence creates a sequence that errors partway through.
func ErrorMidStreamSequence(sessionID string, errorAtMessage int, err error) *MockTransport {
	return NewFixtureBuilder().
		AddInitResponse(sessionID).
		AddAssistantText("Starting review...").
		AddToolUse("tool-1", "get_changed_files", map[string]any{}).
		WithErrorAtMessage(errorAtMessage, err).
		Build()
}

// CancelableSequence creates a long sequence with delays for testing cancellation.
func CancelableSequence(sessionID string, messageCount int, delay time.Duration) *MockTransport {
	builder := NewFixtureBuilder().AddInitResponse(sessionID)

	for i := 0; i < messageCount; i++ {
		builder.AddAssistantTextWithDelay("Processing...", delay)
	}

	return builder.AddResult(sessionID).Build()
}
