package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// Store manages session state in memory with optional persistence
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*protocol.Session
	prs      map[string]*protocol.PR
	path     string // path to state file (empty = no persistence)
	dirty    bool   // tracks unsaved changes
}

// New creates a new session store
func New() *Store {
	return &Store{
		sessions: make(map[string]*protocol.Session),
		prs:      make(map[string]*protocol.PR),
	}
}

// NewWithPersistence creates a store that persists to disk
func NewWithPersistence(path string) *Store {
	s := &Store{
		sessions: make(map[string]*protocol.Session),
		prs:      make(map[string]*protocol.PR),
		path:     path,
	}
	s.Load() // load existing state if any
	return s
}

// DefaultStatePath returns the default state file path
func DefaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/.claude-manager-state.json"
	}
	return filepath.Join(home, ".claude-manager-state.json")
}

type persistedState struct {
	Sessions []*protocol.Session `json:"sessions"`
	PRs      []*protocol.PR      `json:"prs,omitempty"`
}

// Load loads sessions from disk
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no state file yet
		}
		return err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		// Try legacy format (just sessions array)
		var sessions []*protocol.Session
		if err := json.Unmarshal(data, &sessions); err != nil {
			return err
		}
		state.Sessions = sessions
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range state.Sessions {
		s.sessions[session.ID] = session
	}
	for _, pr := range state.PRs {
		s.prs[pr.ID] = pr
	}
	return nil
}

// Save persists sessions to disk if path is configured
func (s *Store) Save() {
	if s.path == "" {
		return
	}

	// Read state under lock
	s.mu.RLock()
	state := persistedState{
		Sessions: make([]*protocol.Session, 0, len(s.sessions)),
		PRs:      make([]*protocol.PR, 0, len(s.prs)),
	}
	for _, session := range s.sessions {
		state.Sessions = append(state.Sessions, session)
	}
	for _, pr := range s.prs {
		state.PRs = append(state.PRs, pr)
	}
	s.mu.RUnlock()

	// Write to file without lock
	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	os.WriteFile(s.path, data, 0600)
}

// Add adds a session to the store
func (s *Store) Add(session *protocol.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	s.markDirty()
}

// Get retrieves a session by ID
func (s *Store) Get(id string) *protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// Remove removes a session from the store
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	s.markDirty()
}

// List returns sessions, optionally filtered by state, sorted by label
func (s *Store) List(stateFilter string) []*protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*protocol.Session
	for _, session := range s.sessions {
		if stateFilter == "" || session.State == stateFilter {
			result = append(result, session)
		}
	}

	// Sort by label for stable ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].Label < result[j].Label
	})

	return result
}

// UpdateState updates a session's state
func (s *Store) UpdateState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.State = state
		session.StateSince = time.Now()
		s.markDirty()
	}
}

// UpdateTodos updates a session's todo list
func (s *Store) UpdateTodos(id string, todos []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.Todos = todos
		s.markDirty()
	}
}

// Touch updates a session's last seen time
func (s *Store) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.LastSeen = time.Now()
	}
}

// ToggleMute toggles a session's muted state
func (s *Store) ToggleMute(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.Muted = !session.Muted
		s.markDirty()
	}
}

// SetPRs replaces all PRs, preserving muted state
func (s *Store) SetPRs(prs []*protocol.PR) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Preserve muted state from existing PRs
	for _, pr := range prs {
		if existing, ok := s.prs[pr.ID]; ok {
			pr.Muted = existing.Muted
		}
	}

	// Replace all PRs
	s.prs = make(map[string]*protocol.PR)
	for _, pr := range prs {
		s.prs[pr.ID] = pr
	}
	s.markDirty()
}

// ListPRs returns PRs, optionally filtered by state, sorted by repo/number
func (s *Store) ListPRs(stateFilter string) []*protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*protocol.PR
	for _, pr := range s.prs {
		if stateFilter == "" || pr.State == stateFilter {
			result = append(result, pr)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// ToggleMutePR toggles a PR's muted state
func (s *Store) ToggleMutePR(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pr, ok := s.prs[id]; ok {
		pr.Muted = !pr.Muted
		s.markDirty()
	}
}

// GetPR returns a PR by ID
func (s *Store) GetPR(id string) *protocol.PR {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prs[id]
}

// IsDirty returns whether the store has unsaved changes
func (s *Store) IsDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

// ClearDirty clears the dirty flag (called after successful save)
func (s *Store) ClearDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
}

// markDirty sets the dirty flag
func (s *Store) markDirty() {
	s.dirty = true
}
