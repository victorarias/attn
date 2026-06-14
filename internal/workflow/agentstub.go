package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// AgentCall is the per-call spec handed to AgentStub.Run. It carries the agent()
// invocation's identity (Ordinal), its inputs (Prompt, Schema), and the per-call
// opts that select execution context (Isolation, Model, AgentType). It mirrors
// native's agent(prompt, opts) shape so the real driver can honor each knob.
//
// Isolation is "" (none — share the writable working tree, the E3 default) or
// "worktree" (run in a fresh git worktree as CWD; see driverAgent.Run). Unknown
// values are normalized to "" upstream (host.go) so a typo never silently runs
// in an unexpected mode. Model overrides the agent's default model when non-empty.
// AgentType is carried for native parity but currently unused by the driver.
//
// Isolation/Model/AgentType are NOT part of the journal cache identity: the cache
// predicate stays ordinal AND prompt_hash AND schema_hash (§6). Isolation changes
// WHERE a live call runs, not WHAT its cached result is — so a resumed cache-hit
// replays the journaled result and never re-creates a worktree.
type AgentCall struct {
	Ordinal   OrdinalPath
	Prompt    string
	Schema    json.RawMessage
	Isolation string // "" (none) | "worktree"
	Model     string // per-call model override; "" => the agent's default
	AgentType string // native-parity carry; currently unused by the driver
}

// AgentStub is the agent() implementation behind the engine. DefaultStub is the
// fake (E1/tests); driverAgent (E2/E3) spawns a real headless subagent. Run is
// called on a worker goroutine and MUST be a pure deterministic function of its
// inputs for replay tests to hold (the fakes are; the real driver is not, which
// is why live runs do not assert ordinal replay).
//
// call.Schema is the per-call JSON Schema (nil when the call has no schema). The
// fakes read call.Ordinal/call.Prompt/call.Schema and ignore the rest; the real
// driver advertises the schema through the return_result sink and honors
// call.Isolation/call.Model.
type AgentStub interface {
	// Run returns the result for call. A returned error models a terminal subagent
	// failure: the engine resolves the agent() promise to null and journals status
	// "errored" (never rejects).
	Run(call AgentCall) (json.RawMessage, error)
}

// DefaultStub returns a deterministic result derived from the prompt:
// JSON string of sha256(prompt)[:12].
type DefaultStub struct{}

func (DefaultStub) Run(call AgentCall) (json.RawMessage, error) {
	sum := sha256.Sum256([]byte(call.Prompt))
	h := hex.EncodeToString(sum[:])[:12]
	b, _ := json.Marshal(h)
	return b, nil
}

// StubFunc adapts a plain function to AgentStub.
type StubFunc func(call AgentCall) (json.RawMessage, error)

func (f StubFunc) Run(call AgentCall) (json.RawMessage, error) {
	return f(call)
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

// NewScriptedStub builds a gated stub. resultFor must be deterministic. The
// schema is not part of the resultFor signature because the ordinal-stability
// tests that use ScriptedStub do not vary by schema; Run accepts and ignores it.
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

func (s *ScriptedStub) Run(call AgentCall) (json.RawMessage, error) {
	<-s.gate(call.Ordinal.String())
	return s.resultFor(call.Ordinal, call.Prompt)
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
