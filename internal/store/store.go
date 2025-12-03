package store

import (
	"sync"
	"time"

	"github.com/victorarias/claude-manager/internal/protocol"
)

// Store manages session state in memory
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*protocol.Session
}

// New creates a new session store
func New() *Store {
	return &Store{
		sessions: make(map[string]*protocol.Session),
	}
}

// Add adds a session to the store
func (s *Store) Add(session *protocol.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
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
}

// List returns sessions, optionally filtered by state
func (s *Store) List(stateFilter string) []*protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*protocol.Session
	for _, session := range s.sessions {
		if stateFilter == "" || session.State == stateFilter {
			result = append(result, session)
		}
	}
	return result
}

// UpdateState updates a session's state
func (s *Store) UpdateState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.State = state
		session.StateSince = time.Now()
	}
}

// UpdateTodos updates a session's todo list
func (s *Store) UpdateTodos(id string, todos []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, ok := s.sessions[id]; ok {
		session.Todos = todos
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
