package protocol

import (
	"encoding/json"
	"errors"
	"time"
)

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

// ParseMessage parses a JSON message and returns the command type and parsed message
func ParseMessage(data []byte) (string, interface{}, error) {
	// First, extract just the command
	var peek struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", nil, err
	}
	if peek.Cmd == "" {
		return "", nil, errors.New("missing cmd field")
	}

	// Parse based on command type
	switch peek.Cmd {
	case CmdRegister:
		var msg RegisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUnregister:
		var msg UnregisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdState:
		var msg StateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTodos:
		var msg TodosMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQuery:
		var msg QueryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
