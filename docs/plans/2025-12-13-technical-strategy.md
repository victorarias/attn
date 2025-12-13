# Technical Strategy: Evolving Toward an Agent Orchestrator

**Date:** 2025-12-13
**Status:** Proposed
**Vision:** Evolve from "Claude session manager" to "agent orchestrator" supporting multiple agents, integrations, and richer UI

## Executive Summary

This document defines technical direction to guide refactoring decisions. The goal is to lay foundations now that make future evolution natural, without building prematurely for features that don't exist yet.

**Future directions anticipated:**
- Multiple agent types (Claude, Cortex, Gemini)
- More integrations (Linear, Jira, other task managers)
- More UI complexity (git diffs, richer visualizations)

**What we're NOT doing now:**
- Building WorkItem abstraction (no task tracking yet)
- Building plugin systems (only one agent, one integration)
- Over-engineering for hypothetical requirements

---

## Section 1: The Seams

The system has three natural boundaries that matter for evolution:

```
┌─────────────────────────────────────────────────────────┐
│                        UI Layer                          │
│   (React components, state management, visualizations)   │
└─────────────────────────┬───────────────────────────────┘
                          │ WebSocket + Tauri IPC
┌─────────────────────────┴───────────────────────────────┐
│                     Daemon Layer                         │
│   (orchestration, state aggregation, broadcasting)       │
└───────────┬─────────────────────────────────────────────┘
            │
┌───────────┴───────────────────────────────────────────────┐
│                     Adapter Layer                          │
│  ┌─────────────────┐              ┌─────────────────────┐ │
│  │  Agent Adapters │              │ Integration Adapters│ │
│  │  (Claude, ...)  │              │  (GitHub, ...)      │ │
│  └─────────────────┘              └─────────────────────┘ │
└───────────────────────────────────────────────────────────┘
```

**Today's problem:** Claude-specific code (hooks, transcript parsing, classification) is mixed into the daemon layer. GitHub-specific code is similarly embedded.

**Strategy:** During refactoring, move agent-specific and integration-specific code into isolated adapter packages, even if there's only one adapter of each type.

---

## Section 2: Minimal Abstractions Worth Adding Now

Two interfaces that pay for themselves immediately and enable future extensions:

### 2.1 AttentionSource Interface

```go
// internal/attention/source.go
type AttentionSource interface {
    ID() string
    Kind() string                    // "session", "pr"
    Label() string
    NeedsAttention() bool
    AttentionReason() string         // "waiting_input", "review_requested", "ci_failed"
    LastUpdated() time.Time
    Muted() bool
}
```

**Why now:** Sessions and PRs already share this shape conceptually. The sidebar and attention drawer already treat them similarly. Making it explicit:
- Unifies filtering/sorting logic (currently duplicated in 3+ places)
- Makes "attention count" calculation trivial
- Future entity types just implement the interface

### 2.2 AgentAdapter Interface

```go
// internal/agent/adapter.go
type AgentAdapter interface {
    Name() string                              // "claude", "cortex"
    RegisterSession(id, dir string) error
    UnregisterSession(id string) error
    ParseState(rawEvent []byte) (State, error)
    ClassifyIfNeeded(session *Session) (State, error)
}
```

**Why now:** Isolates all Claude-specific code (hooks generation, transcript parsing, LLM classification) into one package. Even with only Claude, this:
- Makes the daemon agent-agnostic
- Makes Claude adapter testable in isolation
- Documents what "supporting an agent" means

### What We're NOT Adding

- `WorkItem` - No task tracking yet, premature abstraction
- `TaskSource` - No external task integrations yet
- `Plugin` system - Only one agent, one integration; interfaces are enough

---

## Section 3: Architectural Principles

These guide refactoring decisions and code reviews.

### P1. Single Source of Truth for Types

Define protocol types once, generate for other languages.

```
protocol/
├── schema/
│   ├── session.json      # JSON Schema definitions
│   ├── pr.json
│   └── events.json
├── go/                   # Generated Go types
├── ts/                   # Generated TypeScript types
└── generate.go           # Generation script
```

**Tool:** [quicktype](https://quicktype.io/) or custom Go template. JSON Schema is the source, everything else derived.

**Enforcement:** `make generate-types` runs in CI, fails if generated files don't match schema.

### P2. Explicit Communication Contracts

Every daemon↔app operation falls into one of three patterns:

| Pattern | When | Example |
|---------|------|---------|
| **Command→Result** | Mutations that can fail | `create_worktree` → `create_worktree_result` |
| **Query→Response** | Fetching data | `list_worktrees` → `worktrees_updated` |
| **Broadcast** | Server-initiated push | `session_state_changed` (no request) |

**Rule:** If it can fail, it needs a result event. No silent fire-and-forget for mutations.

**Naming convention:**
- Commands: `verb_noun` (e.g., `create_worktree`, `approve_pr`)
- Results: `verb_noun_result` (e.g., `create_worktree_result`)
- Broadcasts: `noun_event` (e.g., `session_state_changed`, `prs_updated`)

### P3. Adapter Isolation

Agent and integration code lives in dedicated packages. Daemon imports interfaces, never implementations directly.

```go
// internal/daemon/daemon.go
type Daemon struct {
    agent       agent.Adapter        // Interface, not *claude.Adapter
    integration integration.Source   // Interface, not *github.Client
    // ...
}

func New(opts ...Option) *Daemon {
    d := &Daemon{}
    for _, opt := range opts {
        opt(d)
    }
    return d
}

// Usage:
daemon.New(
    daemon.WithAgent(claude.NewAdapter()),
    daemon.WithIntegration(github.NewSource()),
)
```

**Rule:** `daemon/` package never imports `agent/claude/` or `integration/github/` directly. Only through interfaces.

---

## Section 4: Tooling Recommendations

Targeted additions that solve specific problems.

### T1. Type Generation Pipeline

```makefile
# Makefile addition
generate-types:
    quicktype --src protocol/schema/*.json --lang go -o internal/protocol/generated.go
    quicktype --src protocol/schema/*.json --lang typescript -o app/src/types/generated.ts
```

**Why:** Eliminates the 6-struct duplication problem permanently. Schema changes propagate automatically.

**Alternative:** If quicktype doesn't fit, use Go templates to generate TypeScript from Go struct tags.

### T2. Frontend Unit Testing

```bash
cd app && pnpm add -D vitest @testing-library/react happy-dom
```

Vitest over Jest because:
- Native ESM support (matches Vite)
- Faster cold starts
- Same API as Jest (easy migration)

**Initial coverage targets:**
- Custom hooks (useAttention, usePRsNeedingAttention)
- State indicator component
- Protocol message parsing

### T3. Integration Test Harness

Create a test daemon mode that:
- Runs in-memory (no SQLite file)
- Exposes a test client for sending fake hook events
- Allows asserting on WebSocket broadcasts

```go
// test/harness/daemon.go
type TestDaemon struct {
    daemon     *daemon.Daemon
    store      *store.Store
    broadcasts []protocol.WebSocketEvent  // Captured broadcasts
    classifier *fakeClassifier
}

func NewTestDaemon() *TestDaemon {
    store := store.NewInMemory()
    classifier := &fakeClassifier{}

    d := daemon.New(
        daemon.WithStore(store),
        daemon.WithClassifier(classifier),  // Inject fake
        daemon.WithBroadcastCapture(),      // Capture instead of send
    )

    return &TestDaemon{d, store, nil, classifier}
}

func (td *TestDaemon) SimulateHook(event any) {
    // Convert event to JSON, call daemon's message handler directly
    // Bypasses Unix socket entirely
}

func (td *TestDaemon) AssertBroadcast(t *testing.T, expected any) {
    // Check td.broadcasts contains expected event
}
```

**What this enables testing:**
- Race conditions (hook arrives before registration)
- Timestamp logic bugs (stale classifier overwrites newer state)
- Broadcast failures (message dropped, wrong format)
- State machine edge cases (double-stop, stop-before-register)

**Daemon refactors needed:**
- Classifier injected via option (not hardcoded)
- Broadcast destination injectable (real WebSocket vs capture slice)
- Message handling callable directly (not just via socket)

### T4. Consider Protobuf (Future)

Not immediate, but worth evaluating if protocol complexity grows significantly. Benefits:
- Enforced backward compatibility
- Smaller wire format
- Cross-language generation built-in

Keep JSON for now, but structure code so serialization is isolated and swappable.

---

## Section 5: File Organization for Scale

### Go Backend

```
internal/
├── agent/                      # Agent abstraction layer
│   ├── adapter.go              # AgentAdapter interface
│   ├── registry.go             # Agent registration
│   └── claude/                 # Claude-specific implementation
│       ├── adapter.go          # Implements AgentAdapter
│       ├── hooks.go            # Hook generation
│       ├── transcript.go       # Transcript parsing
│       └── classifier.go       # LLM classification
│
├── attention/                  # Attention abstraction layer
│   ├── source.go               # AttentionSource interface
│   └── aggregator.go           # Combines sources, computes counts
│
├── integration/                # External system adapters
│   ├── source.go               # IntegrationSource interface
│   └── github/                 # GitHub-specific implementation
│       ├── client.go
│       ├── polling.go
│       └── ratelimit.go
│
├── daemon/                     # Core orchestration (agent-agnostic)
│   ├── daemon.go               # Main loop, socket handling
│   ├── websocket.go            # WebSocket hub
│   └── handlers.go             # Message dispatch
│
├── store/                      # Persistence
│   ├── store.go
│   ├── sqlite.go
│   └── migrations.go           # Schema migrations
│
└── protocol/                   # Communication contracts
    ├── schema/                 # JSON Schema source of truth
    │   ├── session.json
    │   ├── pr.json
    │   └── events.json
    ├── generated.go            # Generated from schema
    └── parse.go
```

**Key changes from current:**
- Agent code moves from `daemon/` → `agent/claude/`
- GitHub code moves from `github/` → `integration/github/`
- Protocol types generated from schema
- Migrations added to store
- New `attention/` package for unified attention logic

### React Frontend

```
app/src/
├── components/
│   ├── common/                 # Shared components
│   │   ├── StateIndicator.tsx
│   │   └── LoadingSpinner.tsx
│   ├── sessions/               # Session-specific components
│   ├── prs/                    # PR-specific components
│   └── layout/                 # App shell, sidebar, etc.
│
├── hooks/
│   ├── useAttention.ts         # Unified attention logic
│   ├── useDaemonSocket.ts      # WebSocket connection
│   └── useKeyboardShortcuts.ts
│
├── store/
│   ├── daemon.ts               # Daemon state (sessions, PRs)
│   └── terminal.ts             # Local terminal sessions
│
└── types/
    ├── generated.ts            # Generated from protocol schema
    └── ui.ts                   # UI-specific types
```

---

## Section 6: Migration Path

How to get from current state to target state incrementally.

### Phase 1: Isolate Adapters (During Tech Debt Cleanup)

1. Create `internal/agent/` with interface
2. Move Claude-specific code to `internal/agent/claude/`
3. Update daemon to use interface
4. Create `internal/integration/` with interface
5. Move GitHub code to `internal/integration/github/`

**No new features, just reorganization.**

### Phase 2: Add Attention Abstraction

1. Create `internal/attention/source.go` interface
2. Make Session implement AttentionSource
3. Make PR implement AttentionSource
4. Create aggregator that combines both
5. Update frontend to use unified attention data

### Phase 3: Type Generation

1. Write JSON Schema for existing types
2. Set up quicktype generation
3. Replace hand-written types with generated ones
4. Add CI check that generated files match schema

### Phase 4: Test Harness

1. Refactor daemon for dependency injection
2. Create in-memory store option
3. Create test harness package
4. Add integration tests for critical paths

---

## Decision Log

| Decision | Rationale | Alternatives Considered |
|----------|-----------|------------------------|
| Interfaces over plugins | Only one agent/integration now; interfaces are simpler | Plugin system, trait objects |
| JSON Schema for types | Language-agnostic, good tooling | Protobuf (heavier), Go as source (TS generation harder) |
| AttentionSource interface | Unifies existing duplication | Keep separate Session/PR handling |
| No WorkItem abstraction | No task tracking yet | Add generic WorkItem now |

---

## Success Criteria

This strategy is working if:

1. **Adding a new agent** requires only:
   - New package under `internal/agent/`
   - Implementing AgentAdapter interface
   - Registering with daemon at startup

2. **Adding a new integration** requires only:
   - New package under `internal/integration/`
   - Implementing IntegrationSource interface
   - Registering with daemon at startup

3. **Protocol changes** require only:
   - Updating JSON Schema
   - Running `make generate-types`
   - No manual type synchronization

4. **Testing daemon behavior** doesn't require:
   - Running real Claude
   - Connecting to real GitHub
   - Setting up Unix sockets

---

## Appendix: Current vs Target Entity Model

### Current (Implicit)

```
Session ─── has state ─── needs attention?
PR ─────── has fields ─── needs attention?
           (computed separately)
```

### Target (Explicit)

```
┌─────────────────────┐
│  AttentionSource    │ (interface)
├─────────────────────┤
│ NeedsAttention()    │
│ AttentionReason()   │
│ LastUpdated()       │
│ Muted()             │
└─────────────────────┘
          △
          │ implements
    ┌─────┴─────┐
    │           │
┌───┴───┐   ┌───┴───┐
│Session│   │  PR   │
└───────┘   └───────┘
```

Both feed into `AttentionAggregator` which provides:
- Unified list of items needing attention
- Total attention count
- Filtering by kind, mute status, etc.
