# Multi-Agent Architecture Design

## Overview

Refactor attn to support multiple AI coding agents (Claude Code, OpenAI Codex, future agents) with a clean abstraction layer. Each agent declares its capabilities, and the UI adapts accordingly.

## Goals

1. **Clean abstraction** - Agent interface that different implementations conform to
2. **Capability-based UI** - Frontend shows/hides features based on what the agent supports
3. **Visual distinction** - Clear indication of which agent type a session uses
4. **Incremental adoption** - Start with minimal Codex support, expand later

## Non-Goals

- Full feature parity between agents (Codex has fewer hooks than Claude)
- Auto-detection of agent type from running process
- Supporting agents that don't have a CLI

## Agent Interface

```go
// internal/agent/agent.go

type AgentType string

const (
    AgentClaude AgentType = "claude"
    AgentCodex  AgentType = "codex"
)

type AgentConfig struct {
    Type        AgentType
    DisplayName string  // "Claude Code", "OpenAI Codex"
    Command     string  // "claude", "codex"
}

type Agent interface {
    // Core - always required
    Config() AgentConfig
    BuildCommand(workdir string, extraArgs []string) (cmd string, args []string)
    FindExecutable() (string, error)

    // Optional capabilities - return nil if not supported
    StateTracker() StateTracker
    TodoTracker() TodoTracker
    TranscriptReader() TranscriptReader
}
```

## Capability Interfaces

Each capability is its own interface, allowing agents to provide different implementations:

```go
// StateTracker provides real-time state updates via hooks
type StateTracker interface {
    // GenerateHooks returns hooks config for state updates
    GenerateHooks(sessionID, socketPath string) *HooksConfig
}

// TodoTracker provides todo list updates
type TodoTracker interface {
    // GenerateHooks returns hooks for todo updates
    GenerateHooks(sessionID, socketPath string) *HooksConfig
    // ParseTodos extracts todos from hook payload
    ParseTodos(data []byte) ([]Todo, error)
}

// TranscriptReader provides access to conversation history
type TranscriptReader interface {
    // FindTranscript locates the transcript file for a session
    FindTranscript(sessionCwd string) (string, error)
    // ParseLastMessage extracts the last assistant message
    ParseLastMessage(transcriptPath string) (string, error)
}
```

## Shared Classifier

Classification (idle vs waiting_input) is **not** agent-specific. We use Claude Haiku to classify any agent's last message:

```go
// internal/classifier/classifier.go (existing location, unchanged interface)

type Classifier struct{}

func (c *Classifier) Classify(lastMessage string) (State, error) {
    // Uses "claude -p" with Haiku to classify
}
```

Any agent that provides a `TranscriptReader` can have its messages classified, even without hooks.

## Agent Implementations

### Claude Agent (Full Capabilities)

```go
// internal/agent/claude.go

type ClaudeAgent struct{}

func (c *ClaudeAgent) Config() AgentConfig {
    return AgentConfig{
        Type:        AgentClaude,
        DisplayName: "Claude Code",
        Command:     "claude",
    }
}

func (c *ClaudeAgent) StateTracker() StateTracker {
    return &ClaudeStateTracker{}  // Full hooks support
}

func (c *ClaudeAgent) TodoTracker() TodoTracker {
    return &ClaudeTodoTracker{}  // TodoWrite hook
}

func (c *ClaudeAgent) TranscriptReader() TranscriptReader {
    return &ClaudeTranscriptReader{}  // ~/.claude/projects/*/session.jsonl
}
```

### Codex Agent (Minimal Capabilities)

```go
// internal/agent/codex.go

type CodexAgent struct{}

func (c *CodexAgent) Config() AgentConfig {
    return AgentConfig{
        Type:        AgentCodex,
        DisplayName: "OpenAI Codex",
        Command:     "codex",
    }
}

func (c *CodexAgent) StateTracker() StateTracker {
    return nil  // No hooks support (yet)
}

func (c *CodexAgent) TodoTracker() TodoTracker {
    return nil  // No TodoWrite equivalent
}

func (c *CodexAgent) TranscriptReader() TranscriptReader {
    return nil  // TODO: implement if we find the format
}
```

## Agent Registry

```go
// internal/agent/registry.go

var registry = map[AgentType]Agent{
    AgentClaude: &ClaudeAgent{},
    AgentCodex:  &CodexAgent{},
}

func Get(t AgentType) (Agent, bool) {
    a, ok := registry[t]
    return a, ok
}

func Available() []AgentType {
    var available []AgentType
    for t, a := range registry {
        if _, err := a.FindExecutable(); err == nil {
            available = append(available, t)
        }
    }
    return available
}
```

## Protocol Changes

### TypeSpec Schema

```tsp
// internal/protocol/schema/main.tsp

model AgentCapabilities {
    state_tracking: boolean;
    todo_tracking: boolean;
    transcript_access: boolean;
}

model Session {
    // ... existing fields ...
    agent_type: "claude" | "codex";
    agent_display_name: string;
    capabilities: AgentCapabilities;
}
```

### Registration Message

```json
{
    "cmd": "register",
    "id": "session-123",
    "label": "attn",
    "cwd": "/Users/victor/projects/attn",
    "agent_type": "claude"
}
```

The daemon looks up the agent type, attaches capabilities, and broadcasts to the frontend.

## CLI Changes

New `--agent` flag to select agent type:

```bash
attn                    # Default (claude)
attn --agent codex      # Use Codex
attn -a codex           # Short form
```

### Launch Flow

```
attn --agent codex -s myproject
       │
       ▼
┌─────────────────────────────────┐
│ 1. Parse --agent flag (default: claude)
│ 2. agent.Get(agentType)
│ 3. agent.FindExecutable() - verify installed
│ 4. Register session with daemon (includes agent_type)
│ 5. agent.BuildCommand() - get launch cmd
│ 6. If agent.StateTracker() != nil, generate hooks
│ 7. Exec agent (with or without hooks)
└─────────────────────────────────┘
```

## Frontend Adaptation

The UI adapts based on capabilities:

### Session Indicator (Sidebar)

```tsx
function SessionIndicator({ session }: { session: Session }) {
  if (!session.capabilities.state_tracking) {
    // No state tracking - show agent type icon
    return <AgentIcon type={session.agent_type} />;
  }

  // State-based indicator (green/orange/gray)
  return <StateIndicator state={session.state} />;
}
```

### Capability-Based Rendering

| Capability | If true | If false |
|------------|---------|----------|
| `state_tracking` | Green/orange/gray indicator | Neutral icon (agent logo) |
| `todo_tracking` | Show todo list section | Hide todo section |
| `transcript_access` | Enable classification | Skip classification |

### Visual Distinction

| Element | Claude | Codex |
|---------|--------|-------|
| Icon | Anthropic logo | OpenAI logo |
| State dot | Green/orange/gray | Neutral (blue or hidden) |
| Todo section | Shown | Hidden |
| Agent badge | "Claude Code" | "OpenAI Codex" |

## Package Structure

### New Structure

```
internal/
├── agent/
│   ├── agent.go              # Agent interface, capability interfaces
│   ├── registry.go           # Agent registry
│   ├── claude.go             # ClaudeAgent implementation
│   ├── claude_state.go       # ClaudeStateTracker (hooks)
│   ├── claude_todo.go        # ClaudeTodoTracker
│   ├── claude_transcript.go  # ClaudeTranscriptReader
│   └── codex.go              # CodexAgent implementation
├── classifier/               # KEEP - shared classifier service
│   └── classifier.go
├── hooks/                    # DELETE - absorbed into agent/claude_state.go
├── transcript/               # DELETE - absorbed into agent/claude_transcript.go
└── ... (daemon, store, protocol unchanged)
```

### Migration Steps

1. Create `internal/agent/` package with interfaces
2. Implement `ClaudeAgent` by moving existing code
3. Implement minimal `CodexAgent` (just launch, no capabilities)
4. Update `cmd/attn/main.go` to use agent registry
5. Update protocol schema with agent fields
6. Run `make generate-types`
7. Update frontend to read capabilities and adapt UI
8. Delete deprecated packages (`internal/hooks/`, `internal/transcript/`)

## Future Enhancements

Once the architecture is in place, Codex could gain capabilities:

1. **TranscriptReader** - Parse `~/.codex/history.jsonl` or session files
2. **Classification via file watch** - Detect transcript changes, classify last message
3. **Notify hook** - Codex's `notify` config fires on `agent-turn-complete`

The architecture supports these without restructuring.

## Testing Strategy

1. **Unit tests** for agent registry and capability detection
2. **Integration tests** for launch flow with both agents
3. **Frontend tests** for capability-based rendering
4. **Manual testing** with both Claude and Codex sessions

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Codex transcript format changes | Implement TranscriptReader later, start with no classification |
| Codex not installed on user's machine | `FindExecutable()` returns error, UI shows agent unavailable |
| Breaking existing Claude functionality | Migrate incrementally, tests verify behavior preserved |
