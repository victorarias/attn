package ptybackend

import (
	"context"
	"syscall"

	"github.com/victorarias/attn/internal/pty"
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

	// Theme seeds the colors the session answers OSC 10/11/12 color queries
	// with. Zero-value fields fall back to built-in defaults.
	Theme pty.TerminalTheme

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

	// AutoApprove mirrors the daemon's auto_approve_enabled setting. When true the
	// worker exports ATTN_AUTO_APPROVE so the launched agent starts in its native
	// auto-approve mode (Claude --permission-mode auto). Yolo overrides it.
	AutoApprove bool

	// Model, when set, pins the launched agent's model via --model. Sourced from
	// the chief_model_<agent> setting for chief launches or a delegation's
	// --model flag; the worker exports it as ATTN_MODEL. Empty means the agent's
	// own default.
	Model string

	// Effort, when set, pins the launched agent's reasoning effort via its
	// native mechanism (Claude --effort, Codex model_reasoning_effort). Sourced
	// from a delegation's --effort flag; the worker exports it as ATTN_EFFORT.
	// Empty means the agent's own default.
	Effort string

	// ChiefContextWindowCap, when > 0, is the token threshold the chief-of-staff
	// launch caps its context window at; the worker exports it as
	// ATTN_CHIEF_AUTO_COMPACT_WINDOW and the launched agent applies it (Claude:
	// CLAUDE_CODE_AUTO_COMPACT_WINDOW; Codex: model_auto_compact_token_limit).
	// Sourced from the chief_context_window_cap setting and set only for chief
	// launches, so non-chief sessions stay uncapped.
	ChiefContextWindowCap int
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
	// SetTheme updates the colors the session answers OSC 10/11/12 color
	// queries with. Best-effort: a worker predating the method returns nil.
	SetTheme(ctx context.Context, sessionID string, theme pty.TerminalTheme) error
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

// SessionLaunchParams carries the per-spawn launch flags the daemon does not
// otherwise persist — they arrive per-spawn from the client and otherwise live
// only in the live worker. Backends that run a per-session worker record them in
// the worker registry so the daemon can read them back when it re-spawns the
// agent in place (chief-of-staff assign/demote reload).
type SessionLaunchParams struct {
	// Recorded is false for sessions whose worker predates launch-param recording.
	// The daemon must NOT trust the other fields when false and must abort the
	// reload rather than respawn with defaulted launch flags.
	Recorded          bool
	YoloMode          bool
	Executable        string
	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	Model             string
	Effort            string
}

// SessionLaunchParamsProvider is implemented by backends that can return the
// recorded launch params for a live session (the worker backend, via the
// per-session registry). Backends that cannot (e.g. embedded) omit it, and the
// daemon aborts the reload rather than respawning with defaults.
type SessionLaunchParamsProvider interface {
	SessionLaunchParams(ctx context.Context, sessionID string) (SessionLaunchParams, error)
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
