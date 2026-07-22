package ptyworker

import "encoding/json"

const (
	RPCMajor = 1
	RPCMinor = 1
)

// MinCompatibleRPCMinor defines the oldest peer minor version that is
// compatible with this worker RPC major line.
const MinCompatibleRPCMinor = 0

const (
	MethodHello = "hello"
	MethodInfo  = "info"
	// MethodSnapshot returns the current rendered screen + LastSeq without
	// attaching. Added without an RPC version bump: older workers reject it
	// with ErrBadRequest ("unknown method"), and the daemon degrades to an
	// unseeded observer rather than failing.
	MethodSnapshot = "snapshot"
	MethodAttach   = "attach"
	MethodWatch    = "watch"
	MethodDetach   = "detach"
	MethodInput    = "input"
	MethodResize   = "resize"
	// MethodSetTheme updates the colors the session answers OSC 10/11/12 color
	// queries with. Added without an RPC version bump, following the
	// MethodSnapshot precedent: older workers reject it with ErrBadRequest
	// ("unknown method"), and callers treat that as non-fatal.
	MethodSetTheme = "set_theme"
	MethodSignal   = "signal"
	MethodRemove   = "remove"
	MethodHealth   = "health"
)

const (
	EventOutput       = "output"
	EventDesync       = "desync"
	EventStateChanged = "state_changed"
	EventExit         = "exit"
)

const (
	ErrBadRequest         = "bad_request"
	ErrUnsupportedVersion = "unsupported_version"
	ErrUnauthorized       = "unauthorized"
	ErrSessionNotFound    = "session_not_found"
	ErrSessionNotRunning  = "session_not_running"
	ErrIO                 = "io_error"
	ErrInternal           = "internal_error"
)

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RequestEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ResponseEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type EventEnvelope struct {
	Type       string  `json:"type"`
	Event      string  `json:"event"`
	SessionID  string  `json:"session_id"`
	Seq        *uint32 `json:"seq,omitempty"`
	Data       *string `json:"data,omitempty"`
	Reason     *string `json:"reason,omitempty"`
	State      *string `json:"state,omitempty"`
	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`
}

type HelloParams struct {
	RPCMajor         int    `json:"rpc_major"`
	RPCMinor         int    `json:"rpc_minor"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	ControlToken     string `json:"control_token"`
}

type HelloResult struct {
	WorkerVersion    string `json:"worker_version"`
	RPCMajor         int    `json:"rpc_major"`
	RPCMinor         int    `json:"rpc_minor"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	SessionID        string `json:"session_id"`
}

type InfoResult struct {
	Running   bool   `json:"running"`
	Agent     string `json:"agent"`
	CWD       string `json:"cwd"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
	WorkerPID int    `json:"worker_pid"`
	ChildPID  int    `json:"child_pid"`
	LastSeq   uint32 `json:"last_seq"`
	State     string `json:"state"`

	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`
}

type AttachResult struct {
	Scrollback          []byte          `json:"scrollback,omitempty"`
	ScrollbackTruncated bool            `json:"scrollback_truncated"`
	ReplaySegments      []ReplaySegment `json:"replay_segments,omitempty"`
	ReplayTruncated     bool            `json:"replay_truncated,omitempty"`
	LastSeq             uint32          `json:"last_seq"`
	Cols                uint16          `json:"cols"`
	Rows                uint16          `json:"rows"`
	PID                 int             `json:"pid"`
	Running             bool            `json:"running"`

	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`

	ScreenSnapshot      []byte `json:"screen_snapshot,omitempty"`
	ScreenCols          uint16 `json:"screen_cols,omitempty"`
	ScreenRows          uint16 `json:"screen_rows,omitempty"`
	ScreenCursorX       uint16 `json:"screen_cursor_x,omitempty"`
	ScreenCursorY       uint16 `json:"screen_cursor_y,omitempty"`
	ScreenCursorVisible bool   `json:"screen_cursor_visible,omitempty"`
	ScreenSnapshotFresh bool   `json:"screen_snapshot_fresh,omitempty"`

	// GhosttySnapshot is the server-authoritative VT serialization of the whole
	// terminal from libghostty-vt (geometry is Cols/Rows). Omitted when absent.
	GhosttySnapshot []byte `json:"ghostty_snapshot,omitempty"`
}

type ReplaySegment struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
	Data []byte `json:"data"`
}

type AttachParams struct {
	SubscriberID string `json:"subscriber_id,omitempty"`
}

type InputParams struct {
	Data string `json:"data"`
}

type ResizeParams struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type SignalParams struct {
	Signal string `json:"signal"`
}

type SetThemeParams struct {
	Foreground string `json:"foreground"`
	Background string `json:"background"`
	Cursor     string `json:"cursor"`
}

func IsCompatibleVersion(peerMajor, peerMinor int) bool {
	if peerMajor != RPCMajor {
		return false
	}
	if peerMinor < MinCompatibleRPCMinor {
		return false
	}
	if peerMinor > RPCMinor {
		return false
	}
	return true
}
