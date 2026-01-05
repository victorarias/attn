// Package reviewer implements the code review agent.
package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/victorarias/attn/internal/reviewer/mcp"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/claude-agent-sdk-go/sdk"
	"github.com/victorarias/claude-agent-sdk-go/types"
)

// ReviewEvent represents an event emitted during a review.
type ReviewEvent struct {
	Type       string // "started", "chunk", "finding", "resolved", "complete", "error", "cancelled"
	Content    string // For chunk events
	Finding    *Finding
	ResolvedID string // For resolved events - the comment ID that was resolved
	Success bool   // For complete events
	Error   string // For error events
}

// Finding represents a code review finding.
type Finding struct {
	Filepath  string
	LineStart int
	LineEnd   int
	Content   string
	Severity  string // "error", "warning", "suggestion", "info"
	CommentID string // ID of the created comment
}

// ReviewConfig contains configuration for a review.
type ReviewConfig struct {
	RepoPath   string
	Branch     string
	BaseBranch string
	ReviewID   string
	IsRereview bool
	LastReviewSHA string
}

// LogFunc is a function that logs a message.
type LogFunc func(format string, args ...interface{})

// Reviewer orchestrates code reviews using the Claude Agent SDK.
type Reviewer struct {
	store     *store.Store
	transport types.Transport // Custom transport (mock for testing, nil for real)
	logf      LogFunc         // Logger function (optional)

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
}

// New creates a new Reviewer.
func New(store *store.Store) *Reviewer {
	return &Reviewer{
		store: store,
	}
}

// WithTransport sets a custom transport (for testing).
func (r *Reviewer) WithTransport(t types.Transport) *Reviewer {
	r.transport = t
	return r
}

// WithLogger sets a logger function.
func (r *Reviewer) WithLogger(logf LogFunc) *Reviewer {
	r.logf = logf
	return r
}

// log logs a message if a logger is configured.
func (r *Reviewer) log(format string, args ...interface{}) {
	if r.logf != nil {
		r.logf(format, args...)
	}
}

// Run executes a code review and streams events to the callback.
// The callback is called for each event (started, chunk, finding, complete, error).
func (r *Reviewer) Run(ctx context.Context, config ReviewConfig, onEvent func(ReviewEvent)) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return fmt.Errorf("review already in progress")
	}
	r.running = true
	ctx, r.cancel = context.WithCancel(ctx)
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.running = false
		r.cancel = nil
		r.mu.Unlock()
	}()

	// Send started event
	r.log("[reviewer] Starting review for %s (branch: %s, base: %s)", config.RepoPath, config.Branch, config.BaseBranch)
	onEvent(ReviewEvent{Type: "started"})

	// Create MCP tools
	tools := mcp.NewTools(config.RepoPath, config.ReviewID, r.store)

	// Build MCP server with review tools
	mcpServer := r.buildMCPServer(tools, config.ReviewID, onEvent)

	// Build client options
	// MCP tools must be explicitly allowed using format: mcp__<server_name>__<tool_name>
	opts := []types.Option{
		types.WithModel("claude-opus-4-5"),
		sdk.WithClientMCPServer(mcpServer),
		types.WithAllowedTools(
			"mcp__attn-reviewer__get_changed_files",
			"mcp__attn-reviewer__get_diff",
			"mcp__attn-reviewer__list_comments",
			"mcp__attn-reviewer__add_comment",
			"mcp__attn-reviewer__resolve_comment",
		),
	}

	// Use custom transport if provided (for testing)
	if r.transport != nil {
		opt := types.DefaultOptions()
		opt.SetCustomTransport(r.transport)
		opts = append(opts, func(o *types.Options) {
			o.SetCustomTransport(r.transport)
		})
	}

	// Create client
	client := sdk.NewClient(opts...)

	// Connect
	r.log("[reviewer] Connecting to Claude API...")
	if err := client.Connect(ctx); err != nil {
		r.log("[reviewer] Connect error: %v", err)
		onEvent(ReviewEvent{Type: "error", Error: fmt.Sprintf("connect error: %v", err)})
		return err
	}
	r.log("[reviewer] Connected successfully")
	defer client.Close()

	// Debug: verify client state
	if !client.IsConnected() {
		onEvent(ReviewEvent{Type: "error", Error: "client reports not connected after Connect()"})
		return fmt.Errorf("client not connected")
	}

	// Build the review prompt
	prompt := r.buildPrompt(config)

	// Send the query
	r.log("[reviewer] Sending query to agent...")
	if err := client.SendQuery(prompt); err != nil {
		r.log("[reviewer] SendQuery error: %v", err)
		onEvent(ReviewEvent{Type: "error", Error: fmt.Sprintf("send query error: %v", err)})
		return err
	}
	r.log("[reviewer] Query sent, processing messages...")

	// Process messages
	for {
		select {
		case <-ctx.Done():
			onEvent(ReviewEvent{Type: "cancelled"})
			return ctx.Err()

		case msg, ok := <-client.Messages():
			if !ok {
				// Channel closed - check if we got a result
				r.log("[reviewer] Message channel closed")
				if client.ResultReceived() {
					result := client.LastResult()
					r.log("[reviewer] Result received, success=%v", result != nil && result.IsSuccess())
					onEvent(ReviewEvent{
						Type:    "complete",
						Success: result != nil && result.IsSuccess(),
					})
					return nil
				}
				r.log("[reviewer] No result, completing with success=true")
				onEvent(ReviewEvent{Type: "complete", Success: true})
				return nil
			}
			r.log("[reviewer] Received message type: %T", msg)

			// Handle different message types
			switch m := msg.(type) {
			case *types.AssistantMessage:
				// Extract text content and send as chunks
				text := m.Text()
				if text != "" {
					onEvent(ReviewEvent{Type: "chunk", Content: text})
				}

			case *types.ResultMessage:
				onEvent(ReviewEvent{
					Type:    "complete",
					Success: m.IsSuccess(),
					Error:   r.extractResultError(m),
				})
				return nil
			}
		}
	}
}

// Cancel cancels an in-progress review.
func (r *Reviewer) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

// IsRunning returns true if a review is in progress.
func (r *Reviewer) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// buildMCPServer creates an MCP server with review tools.
func (r *Reviewer) buildMCPServer(tools *mcp.Tools, reviewID string, onEvent func(ReviewEvent)) *types.MCPServer {
	return types.NewMCPServerBuilder("attn-reviewer").
		// get_changed_files - returns list of changed files
		WithTool("get_changed_files", "Get list of files changed in the working tree", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}, func(input map[string]any) (*types.MCPToolResult, error) {
			r.log("[reviewer] MCP tool called: get_changed_files")
			files, err := tools.GetChangedFiles()
			if err != nil {
				r.log("[reviewer] get_changed_files error: %v", err)
				return &types.MCPToolResult{
					Content: []types.MCPContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
					IsError: true,
				}, nil
			}
			r.log("[reviewer] get_changed_files returned %d files", len(files))
			data, _ := json.Marshal(files)
			return &types.MCPToolResult{
				Content: []types.MCPContent{{Type: "text", Text: string(data)}},
			}, nil
		}).
		// get_diff - returns diff for specified files
		WithTool("get_diff", "Get diff content for specified files", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "File paths to get diffs for. Empty array returns all diffs.",
				},
			},
		}, func(input map[string]any) (*types.MCPToolResult, error) {
			r.log("[reviewer] MCP tool called: get_diff")
			var paths []string
			if p, ok := input["paths"].([]any); ok {
				for _, v := range p {
					if s, ok := v.(string); ok {
						paths = append(paths, s)
					}
				}
			}
			r.log("[reviewer] get_diff paths: %v", paths)
			diffs, err := tools.GetDiff(paths)
			if err != nil {
				r.log("[reviewer] get_diff error: %v", err)
				return &types.MCPToolResult{
					Content: []types.MCPContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
					IsError: true,
				}, nil
			}
			r.log("[reviewer] get_diff returned %d diffs", len(diffs))
			data, _ := json.Marshal(diffs)
			return &types.MCPToolResult{
				Content: []types.MCPContent{{Type: "text", Text: string(data)}},
			}, nil
		}).
		// list_comments - returns existing comments
		WithTool("list_comments", "List all comments for this review", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}, func(input map[string]any) (*types.MCPToolResult, error) {
			r.log("[reviewer] MCP tool called: list_comments")
			comments, err := tools.ListComments()
			if err != nil {
				r.log("[reviewer] list_comments error: %v", err)
				return &types.MCPToolResult{
					Content: []types.MCPContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
					IsError: true,
				}, nil
			}
			r.log("[reviewer] list_comments returned %d comments", len(comments))
			data, _ := json.Marshal(comments)
			return &types.MCPToolResult{
				Content: []types.MCPContent{{Type: "text", Text: string(data)}},
			}, nil
		}).
		// add_comment - creates a new comment
		WithTool("add_comment", "Add a review comment at a specific location", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filepath": map[string]any{
					"type":        "string",
					"description": "Path to the file",
				},
				"line_start": map[string]any{
					"type":        "integer",
					"description": "Starting line number",
				},
				"line_end": map[string]any{
					"type":        "integer",
					"description": "Ending line number (same as line_start for single line)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Comment content",
				},
			},
			"required": []string{"filepath", "line_start", "line_end", "content"},
		}, func(input map[string]any) (*types.MCPToolResult, error) {
			r.log("[reviewer] MCP tool called: add_comment")
			filepath, _ := input["filepath"].(string)
			lineStart := int(input["line_start"].(float64))
			lineEnd := int(input["line_end"].(float64))
			content, _ := input["content"].(string)
			r.log("[reviewer] add_comment: file=%s lines=%d-%d", filepath, lineStart, lineEnd)

			comment, err := tools.AddComment(filepath, lineStart, lineEnd, content)
			if err != nil {
				return &types.MCPToolResult{
					Content: []types.MCPContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
					IsError: true,
				}, nil
			}

			// Emit finding event
			onEvent(ReviewEvent{
				Type: "finding",
				Finding: &Finding{
					Filepath:  filepath,
					LineStart: lineStart,
					LineEnd:   lineEnd,
					Content:   content,
					CommentID: comment.ID,
				},
			})

			data, _ := json.Marshal(comment)
			return &types.MCPToolResult{
				Content: []types.MCPContent{{Type: "text", Text: string(data)}},
			}, nil
		}).
		// resolve_comment - marks a comment as resolved
		WithTool("resolve_comment", "Mark a comment as resolved", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Comment ID to resolve",
				},
			},
			"required": []string{"id"},
		}, func(input map[string]any) (*types.MCPToolResult, error) {
			r.log("[reviewer] MCP tool called: resolve_comment")
			id, _ := input["id"].(string)
			r.log("[reviewer] resolve_comment: id=%s", id)
			if err := tools.ResolveComment(id); err != nil {
				r.log("[reviewer] resolve_comment error: %v", err)
				return &types.MCPToolResult{
					Content: []types.MCPContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
					IsError: true,
				}, nil
			}
			r.log("[reviewer] resolve_comment: success")

			// Emit resolved event
			onEvent(ReviewEvent{
				Type:       "resolved",
				ResolvedID: id,
			})

			return &types.MCPToolResult{
				Content: []types.MCPContent{{Type: "text", Text: fmt.Sprintf("Comment %s resolved", id)}},
			}, nil
		}).
		Build()
}

// buildPrompt constructs the review prompt.
func (r *Reviewer) buildPrompt(config ReviewConfig) string {
	prompt := fmt.Sprintf(`You are a code reviewer. Review the changes on branch "%s" against "%s".

You have access to these tools and MUST use them immediately without asking for permission:
- get_changed_files() - lists modified files
- get_diff(paths) - shows diff content
- list_comments() - shows existing review comments
- add_comment(filepath, line_start, line_end, content) - adds a review comment
- resolve_comment(id) - marks a comment as resolved

Workflow:
1. Call get_changed_files() to see what changed
2. Call list_comments() to see existing feedback
3. Review each file - use get_diff() to see the changes
4. Use add_comment() for issues you find
5. Use resolve_comment() if a previous issue was fixed

Focus on:
- Code quality and best practices
- Potential bugs or issues
- Performance considerations
- Security concerns

Be constructive and helpful. Focus on significant issues, not style nitpicks.
Do NOT ask for permission - you already have it. Start reviewing immediately.
`, config.Branch, config.BaseBranch)

	if config.IsRereview {
		prompt += fmt.Sprintf("\nThis is a follow-up review. Focus on changes since commit %s.\n", config.LastReviewSHA)
	}

	return prompt
}

// extractResultError extracts error message from a result message.
func (r *Reviewer) extractResultError(m *types.ResultMessage) string {
	if m.IsError {
		if m.Result != nil {
			return *m.Result
		}
		return "unknown error"
	}
	return ""
}
