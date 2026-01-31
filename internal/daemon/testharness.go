package daemon

import (
	"net"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
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

// TestHarness wraps a daemon with test utilities
type TestHarness struct {
	Daemon     *Daemon
	Classifier *FakeClassifier
	Recorder   *BroadcastRecorder
	Store      *store.Store
	SockPath   string
}

// TestHarnessBuilder builds test harnesses with various configurations
type TestHarnessBuilder struct {
	socketPath      string
	defaultState    string
	ghClient        github.GitHubClient
	reviewerFactory ReviewerFactory
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

// WithReviewerFactory sets a custom reviewer factory for testing
func (b *TestHarnessBuilder) WithReviewerFactory(factory ReviewerFactory) *TestHarnessBuilder {
	b.reviewerFactory = factory
	return b
}

// Build creates the test harness
func (b *TestHarnessBuilder) Build() *TestHarness {
	classifier := NewFakeClassifier(b.defaultState)
	recorder := NewBroadcastRecorder()
	sessionStore := store.New()

	pidPath := b.socketPath + ".pid"
	hub := newWSHub()

	// Set up broadcast listener if recording is enabled
	if b.recordBroadcast {
		hub.broadcastListener = func(event *protocol.WebSocketEvent) {
			recorder.Record(event)
		}
	}

	d := &Daemon{
		socketPath:      b.socketPath,
		pidPath:         pidPath,
		store:           sessionStore,
		wsHub:           hub,
		done:            make(chan struct{}),
		logger:          nil,
		ghRegistry:      newRegistryFromClient(b.ghClient),
		classifier:      classifier,
		reviewerFactory: b.reviewerFactory,
	}

	return &TestHarness{
		Daemon:     d,
		Classifier: classifier,
		Recorder:   recorder,
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
