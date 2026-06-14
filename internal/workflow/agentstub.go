package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// AgentStub is the fake agent() implementation. It is the ONLY thing E2 swaps for
// a real subprocess. Run is called on a worker goroutine and MUST be a pure
// deterministic function of its inputs for replay tests to hold.
type AgentStub interface {
	// Run returns the canned result for (ordinal, prompt). A returned error models
	// a terminal subagent failure: the engine resolves the agent() promise to null
	// and journals status "errored" (never rejects).
	Run(ordinal OrdinalPath, prompt string) (json.RawMessage, error)
}

// DefaultStub returns a deterministic result derived from the prompt:
// JSON string of sha256(prompt)[:12].
type DefaultStub struct{}

func (DefaultStub) Run(_ OrdinalPath, prompt string) (json.RawMessage, error) {
	sum := sha256.Sum256([]byte(prompt))
	h := hex.EncodeToString(sum[:])[:12]
	b, _ := json.Marshal(h)
	return b, nil
}

// StubFunc adapts a plain function to AgentStub.
type StubFunc func(ordinal OrdinalPath, prompt string) (json.RawMessage, error)

func (f StubFunc) Run(ordinal OrdinalPath, prompt string) (json.RawMessage, error) {
	return f(ordinal, prompt)
}

// ScriptedStub is the resolution-ORDER injection seam. It gates when each call's
// result is released back to the loop, letting a test release results in an
// arbitrary (e.g. reversed) order to prove ordinal stability under reordered
// resolution. Run blocks until the test releases the matching ordinal.
//
// Usage: construct with the deterministic result function, then from the test
// goroutine call Release(ordinalString) in whatever order you choose. Each Run
// blocks on its ordinal's gate until released. ReleaseAll() opens every gate.
type ScriptedStub struct {
	resultFor func(ordinal OrdinalPath, prompt string) (json.RawMessage, error)

	mu       sync.Mutex
	gates    map[string]chan struct{}
	released map[string]bool
	openAll  bool
}

// NewScriptedStub builds a gated stub. resultFor must be deterministic.
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

func (s *ScriptedStub) Run(ordinal OrdinalPath, prompt string) (json.RawMessage, error) {
	<-s.gate(ordinal.String())
	return s.resultFor(ordinal, prompt)
}

// Release opens the gate for a single ordinal (by its String()), unblocking that
// call's Run. Safe to call before or after the corresponding Run begins.
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
