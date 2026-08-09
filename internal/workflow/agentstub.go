package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// AgentCall is the per-call spec handed to AgentStub.Run. Isolation/Model/
// AgentType are NOT part of the journal cache identity — the predicate stays
// ordinal AND prompt_hash AND schema_hash — so a resumed cache-hit replays the
// journaled result and never re-creates a worktree.
type AgentCall struct {
	Ordinal   OrdinalPath
	Prompt    string
	Schema    json.RawMessage
	Isolation string // "" (none) | "worktree"
	Model     string // per-call model override; "" => the agent's default
	AgentType string // native-parity carry; currently unused by the driver
}

// AgentStub is the agent() implementation behind the engine. Run is called on a
// worker goroutine; replay tests only hold for a pure, deterministic Run.
type AgentStub interface {
	// Run returns the result for call. An error models a terminal subagent
	// failure: the engine resolves the promise to null and journals status
	// "errored", never rejects. A live driver MUST honor ctx to tear the
	// subprocess down.
	Run(ctx context.Context, call AgentCall) (json.RawMessage, error)
}

// DefaultStub returns a deterministic result: JSON string of sha256(prompt)[:12].
type DefaultStub struct{}

func (DefaultStub) Run(_ context.Context, call AgentCall) (json.RawMessage, error) {
	sum := sha256.Sum256([]byte(call.Prompt))
	h := hex.EncodeToString(sum[:])[:12]
	b, _ := json.Marshal(h)
	return b, nil
}

// StubFunc adapts a plain ctx-free function to AgentStub.
type StubFunc func(call AgentCall) (json.RawMessage, error)

func (f StubFunc) Run(_ context.Context, call AgentCall) (json.RawMessage, error) {
	return f(call)
}

// ScriptedStub is the resolution-ORDER injection seam: each Run blocks until the
// test releases its ordinal, proving ordinal stability under reordered resolution.
type ScriptedStub struct {
	resultFor func(ordinal OrdinalPath, prompt string) (json.RawMessage, error)

	mu       sync.Mutex
	gates    map[string]chan struct{}
	released map[string]bool
	openAll  bool
}

// NewScriptedStub builds a gated stub; resultFor must be deterministic.
func NewScriptedStub(resultFor func(ordinal OrdinalPath, prompt string) (json.RawMessage, error)) *ScriptedStub {
	return &ScriptedStub{
		resultFor: resultFor,
		gates:     map[string]chan struct{}{},
		released:  map[string]bool{},
	}
}

func (s *ScriptedStub) gate(ordinal string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openAll || s.released[ordinal] {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	if ch, ok := s.gates[ordinal]; ok {
		return ch
	}
	ch := make(chan struct{})
	s.gates[ordinal] = ch
	return ch
}

func (s *ScriptedStub) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	select {
	case <-s.gate(call.Ordinal.String()):
		return s.resultFor(call.Ordinal, call.Prompt)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release opens the gate for one ordinal, before or after its Run begins.
func (s *ScriptedStub) Release(ordinal string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released[ordinal] = true
	if ch, ok := s.gates[ordinal]; ok {
		close(ch)
		delete(s.gates, ordinal)
	}
}

// ReleaseAll opens every gate (present and future).
func (s *ScriptedStub) ReleaseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openAll = true
	for k, ch := range s.gates {
		close(ch)
		delete(s.gates, k)
	}
}
