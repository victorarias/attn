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
	ForkSession     bool

	// Executable is the selected CLI path for opts.Agent.
	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	PiExecutable      string
}

type AttachInfo struct {
	Scrollback          []byte
	ScrollbackTruncated bool
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
