package protocol

import "time"

// Commands
const (
	CmdRegister   = "register"
	CmdUnregister = "unregister"
	CmdState      = "state"
	CmdTodos      = "todos"
	CmdQuery      = "query"
	CmdHeartbeat  = "heartbeat"
)

// States
const (
	StateWorking = "working"
	StateWaiting = "waiting"
)

// RegisterMessage registers a new session with the daemon
type RegisterMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
	Tmux  string `json:"tmux"`
}

// UnregisterMessage removes a session from tracking
type UnregisterMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// StateMessage updates a session's state
type StateMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	State string `json:"state"`
}

// TodosMessage updates a session's todo list
type TodosMessage struct {
	Cmd   string   `json:"cmd"`
	ID    string   `json:"id"`
	Todos []string `json:"todos"`
}

// QueryMessage queries sessions from daemon
type QueryMessage struct {
	Cmd    string `json:"cmd"`
	Filter string `json:"filter,omitempty"` // "waiting", "working", or empty for all
}

// HeartbeatMessage keeps session alive
type HeartbeatMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// Session represents a tracked Claude session
type Session struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Directory  string    `json:"directory"`
	TmuxTarget string    `json:"tmux_target"`
	State      string    `json:"state"`
	StateSince time.Time `json:"state_since"`
	Todos      []string  `json:"todos,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
}

// Response from daemon
type Response struct {
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	Sessions []*Session `json:"sessions,omitempty"`
}
