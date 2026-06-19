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

	ResumeSessionID   string
	ResumePicker      bool
	YoloMode          bool
	InitialPromptFile string

	// Executable is the selected CLI path for opts.Agent.
	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	ExternalCommand   []string
	ExternalEnv       []string
	ExternalCWD       string
	LifecycleID       string

	// LoginShellEnv, when non-nil, is a pre-computed login shell environment
	// from the daemon's cache. Skips the ~130ms readLoginShellEnv in workers.
	LoginShellEnv []string

	// WorkflowGuidanceEnabled mirrors the daemon's workflows_enabled setting. When
	// true the worker exports ATTN_WORKFLOW_GUIDANCE_ENABLED so the launched agent's
	// instructions include the workflow-trigger guidance.
	WorkflowGuidanceEnabled bool
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
	ID          string
	ExitCode    int
	Signal      string
	LifecycleID string
}

type Backend interface {
	Spawn(ctx context.Context, opts SpawnOptions) error
	Attach(ctx context.Context, sessionID, subscriberID string) (AttachInfo, Stream, error)
	Input(ctx context.Context, sessionID string, data []byte) error
	Resize(ctx context.Context, sessionID string, cols, rows uint16) error
	// Kill returns nil only after the child process has exited.
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

// WorkerProcessProvider is implemented by backends that run each session in its
// own worker subprocess. It exposes those PIDs (sessionID -> worker pid) so
// diagnostics can sum per-session RSS via ps/vmmap — the dominant memory locus
// for the worker backend. Backends without subprocesses (e.g. embedded) do not
// implement it.
type WorkerProcessProvider interface {
	WorkerPIDs(ctx context.Context) map[string]int
}

// SnapshotProvider returns the current rendered screen of a session without
// attaching. Backends that cannot serve a snapshot (e.g. a worker built before
// the capability existed) return an error; callers degrade gracefully.
type SnapshotProvider interface {
	Snapshot(ctx context.Context, sessionID string) (AttachInfo, error)
}

type SessionLivenessProber interface {
	SessionLikelyAlive(ctx context.Context, sessionID string) (bool, error)
}

type RecoverableRuntime interface {
	Backend
	SessionInfoProvider
	SessionLivenessProber
}
