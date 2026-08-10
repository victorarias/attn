package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func newRegistryFromClient(client github.GitHubClient) *github.ClientRegistry {
	registry := github.NewClientRegistry()
	if client == nil {
		return registry
	}
	if ghClient, ok := client.(*github.Client); ok {
		registry.Register(ghClient.Host(), ghClient)
	}
	return registry
}

// Classifier is an interface for classifying session state
type Classifier interface {
	Classify(text string, timeout time.Duration) (string, error)
}

// FakeClassifier allows controlling classification results in tests
type FakeClassifier struct {
	mu           sync.Mutex
	defaultState string
	responses    map[string]string // keyed by session ID or text hash
	calls        []ClassifyCall
}

// ClassifyCall records a call to Classify
type ClassifyCall struct {
	Text    string
	Timeout time.Duration
	Time    time.Time
}

// NewFakeClassifier creates a fake classifier that returns the default state
func NewFakeClassifier(defaultState string) *FakeClassifier {
	return &FakeClassifier{
		defaultState: defaultState,
		responses:    make(map[string]string),
	}
}

// SetResponse sets a specific response for text containing the given substring
func (f *FakeClassifier) SetResponse(substring, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[substring] = state
}

// Classify returns the configured state for the text
func (f *FakeClassifier) Classify(text string, timeout time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, ClassifyCall{
		Text:    text,
		Timeout: timeout,
		Time:    time.Now(),
	})

	// Check for specific responses
	for substring, state := range f.responses {
		if contains(text, substring) {
			return state, nil
		}
	}

	return f.defaultState, nil
}

// Calls returns all recorded calls
func (f *FakeClassifier) Calls() []ClassifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]ClassifyCall, len(f.calls))
	copy(result, f.calls)
	return result
}

// Reset clears all recorded calls
func (f *FakeClassifier) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func contains(text, substr string) bool {
	return len(substr) > 0 && len(text) >= len(substr) && (text == substr || findSubstring(text, substr))
}

func findSubstring(text, substr string) bool {
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// BroadcastRecorder captures all WebSocket broadcasts for verification
type BroadcastRecorder struct {
	mu     sync.Mutex
	events []*protocol.WebSocketEvent
}

// NewBroadcastRecorder creates a new broadcast recorder
func NewBroadcastRecorder() *BroadcastRecorder {
	return &BroadcastRecorder{}
}

// Record adds an event to the recorder
func (r *BroadcastRecorder) Record(event *protocol.WebSocketEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

// Events returns all recorded events
func (r *BroadcastRecorder) Events() []*protocol.WebSocketEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*protocol.WebSocketEvent, len(r.events))
	copy(result, r.events)
	return result
}

// EventsOfType returns events matching the given type
func (r *BroadcastRecorder) EventsOfType(eventType string) []*protocol.WebSocketEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*protocol.WebSocketEvent
	for _, e := range r.events {
		if e.Event == eventType {
			result = append(result, e)
		}
	}
	return result
}

// WaitForEvent waits for an event of the given type with timeout
func (r *BroadcastRecorder) WaitForEvent(eventType string, timeout time.Duration) *protocol.WebSocketEvent {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := r.EventsOfType(eventType)
		if len(events) > 0 {
			return events[len(events)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// Clear removes all recorded events
func (r *BroadcastRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// Count returns the number of recorded events
func (r *BroadcastRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// WireTrace is the complete record of what a daemon put on the WebSocket, in
// order, from every hub send path.
//
// BroadcastRecorder is the older and narrower instrument: it holds typed events
// and only sees hub.Broadcast. WireTrace holds bytes and sees everything, which
// is what a migration needs — the question "does this refactor change what
// clients receive?" is a question about bytes, and it is unanswerable from a
// recorder that a fifth of the send sites bypass.
type WireTrace struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (t *WireTrace) record(payload []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.payloads = append(t.payloads, append([]byte(nil), payload...))
}

// Payloads returns every payload sent so far, in send order.
func (t *WireTrace) Payloads() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.payloads))
	for i, p := range t.payloads {
		out[i] = append([]byte(nil), p...)
	}
	return out
}

// EventNames returns the `event` field of each payload, in send order. A payload
// that is not a JSON object with an `event` string is reported as "?" rather than
// dropped, so a trace comparison cannot silently lose traffic it failed to parse.
func (t *WireTrace) EventNames() []string {
	payloads := t.Payloads()
	names := make([]string, 0, len(payloads))
	for _, p := range payloads {
		var envelope struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(p, &envelope); err != nil || envelope.Event == "" {
			names = append(names, "?")
			continue
		}
		names = append(names, envelope.Event)
	}
	return names
}

// Clear drops everything recorded so far.
func (t *WireTrace) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.payloads = nil
}

// Count returns how many payloads have been sent.
func (t *WireTrace) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.payloads)
}

// TestHarness wraps a daemon with test utilities
type TestHarness struct {
	Daemon     *Daemon
	Classifier *FakeClassifier
	Recorder   *BroadcastRecorder
	Wire       *WireTrace
	Store      *store.Store
	SockPath   string
}

// TestHarnessBuilder builds test harnesses with various configurations
type TestHarnessBuilder struct {
	socketPath      string
	defaultState    string
	ghClient        github.GitHubClient
	recordBroadcast bool
}

// NewTestHarnessBuilder creates a new builder
func NewTestHarnessBuilder(socketPath string) *TestHarnessBuilder {
	return &TestHarnessBuilder{
		socketPath:      socketPath,
		defaultState:    protocol.StateWaitingInput, // Safe default
		recordBroadcast: true,
	}
}

// WithDefaultClassifierState sets the default classifier state
func (b *TestHarnessBuilder) WithDefaultClassifierState(state string) *TestHarnessBuilder {
	b.defaultState = state
	return b
}

// WithGitHubClient sets a custom GitHub client
func (b *TestHarnessBuilder) WithGitHubClient(client github.GitHubClient) *TestHarnessBuilder {
	b.ghClient = client
	return b
}

// WithoutBroadcastRecording disables broadcast recording
func (b *TestHarnessBuilder) WithoutBroadcastRecording() *TestHarnessBuilder {
	b.recordBroadcast = false
	return b
}

// Build creates the test harness
func (b *TestHarnessBuilder) Build() *TestHarness {
	classifier := NewFakeClassifier(b.defaultState)
	recorder := NewBroadcastRecorder()
	wire := &WireTrace{}
	sessionStore := store.New()

	pidPath := b.socketPath + ".pid"
	dataRoot := filepath.Dir(b.socketPath)
	hub := newWSHub()
	manager := pty.NewManager(nil)

	// Set up broadcast listener if recording is enabled
	if b.recordBroadcast {
		hub.broadcastListener = func(event *protocol.WebSocketEvent) {
			recorder.Record(event)
		}
	}
	// The wire trace is always on: it is the only complete record of hub output,
	// and a test that opts out of it cannot tell a migrated broadcast from a
	// deleted one.
	hub.wireTap = wire.record

	d := &Daemon{
		socketPath:          b.socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               sessionStore,
		wsHub:               hub,
		done:                make(chan struct{}),
		logger:              nil,
		ghRegistry:          newRegistryFromClient(b.ghClient),
		classifier:          classifier,
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[string]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[string]string),
		classifyingTurn:     make(map[string]string),
		pendingConversation: make(map[string]agentConversationObservation),
		plugins:             newPluginRegistry(),
	}

	return &TestHarness{
		Daemon:     d,
		Classifier: classifier,
		Recorder:   recorder,
		Wire:       wire,
		Store:      sessionStore,
		SockPath:   b.socketPath,
	}
}

// Start starts the daemon and waits for the socket to be ready
func (h *TestHarness) Start() {
	go h.Daemon.Start()
	// Poll for socket readiness instead of fixed sleep
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", h.SockPath, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Stop stops the daemon
func (h *TestHarness) Stop() {
	h.Daemon.Stop()
}
