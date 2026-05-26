package ptybackend

import (
	"context"
	"syscall"
)

const (
	OutputEventKindOutput = "output"
	OutputEventKindDesync = "desync"
	OutputEventKindExit   = "exit"
)

type SpawnOptions struct {
	ID    string
	CWD   string
	Agent string
	Label string

	Cols uint16
	Rows uint16

	ResumeSessionID string
	ResumePicker    bool
	YoloMode        bool

	// Executable is the selected CLI path for opts.Agent.
	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	PiExecutable      string

	// LoginShellEnv, when non-nil, is a pre-computed login shell environment
	// from the daemon's cache. Skips the ~130ms readLoginShellEnv in workers.
	LoginShellEnv []string
}

type AttachInfo struct {
	Scrollback          []byte
	ScrollbackTruncated bool
	ReplaySegments      []ReplaySegment
	ReplayTruncated     bool
	LastSeq             uint32
	Cols                uint16
	Rows                uint16
	PID                 int
	Running             bool
	ExitCode            *int
	ExitSignal          *string
	ScreenSnapshot      []byte
	ScreenCols          uint16
	ScreenRows          uint16
	ScreenCursorX       uint16
	ScreenCursorY       uint16
	ScreenCursorVisible bool
	ScreenSnapshotFresh bool
}

type ReplaySegment struct {
	Cols uint16
	Rows uint16
	Data []byte
}

type OutputEvent struct {
	Kind   string
	Data   []byte
	Seq    uint32
	Reason string
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool
	State   string

	Cols    uint16
	Rows    uint16
	PID     int
	LastSeq uint32

	ExitCode   *int
	ExitSignal *string
}

type Stream interface {
	Events() <-chan OutputEvent
	Close() error
}

type RecoveryReport struct {
	Recovered int
	Pruned    int
	Missing   int
	Failed    int
}

type ExitInfo struct {
	ID       string
	ExitCode int
	Signal   string
}

type Backend interface {
	Spawn(ctx context.Context, opts SpawnOptions) error
	Attach(ctx context.Context, sessionID, subscriberID string) (AttachInfo, Stream, error)
	Input(ctx context.Context, sessionID string, data []byte) error
	Resize(ctx context.Context, sessionID string, cols, rows uint16) error
	Kill(ctx context.Context, sessionID string, sig syscall.Signal) error
	Remove(ctx context.Context, sessionID string) error
	SessionIDs(ctx context.Context) []string
	Recover(ctx context.Context) (RecoveryReport, error)
	Shutdown(ctx context.Context) error
}

type LifecycleHooks interface {
	SetExitHandler(func(ExitInfo))
	SetStateHandler(func(sessionID, state string))
}

// TerminatingRemover is implemented by backends that can remove a runtime
// while delivering its terminating signal as one operation. Worker-backed
// terminals acknowledge removal before waiting for a TUI to exit, so daemon
// state teardown is not blocked by an unresponsive child.
type TerminatingRemover interface {
	TerminateAndRemove(ctx context.Context, sessionID string, sig syscall.Signal) error
}

type SessionInfoProvider interface {
	SessionInfo(ctx context.Context, sessionID string) (SessionInfo, error)
}

type SessionLivenessProber interface {
	SessionLikelyAlive(ctx context.Context, sessionID string) (bool, error)
}

type RecoverableRuntime interface {
	Backend
	SessionInfoProvider
	SessionLivenessProber
}
