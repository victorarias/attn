package ptybackend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pty"
)

const (
	OutputEventKindOutput = "output"
	OutputEventKindDesync = "desync"
	OutputEventKindExit   = "exit"
	// OutputEventKindPlacements carries the whole kitty placement set as of the
	// chunk stamped Seq; it rides the attach stream because positions only mean
	// anything in order against the same-seq bytes.
	OutputEventKindPlacements = "kitty_placements"
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

	// Theme seeds the OSC 10/11/12 query answers; zero fields use defaults.
	Theme pty.TerminalTheme

	// Executable is the selected CLI path for opts.Agent.
	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	ExternalCommand   []string
	ExternalEnv       []string
	ExternalCWD       string
	// DaemonEnv is the daemon's exact routing contract. Every runtime reapplies
	// it after login-shell and plugin environment.
	DaemonEnv   []string
	LifecycleID string

	// ResumeConversationFile is an existing conversation file this session
	// picks up from, chosen by the user in the new-session flow. Only a
	// conversation session reads it; a PTY-backed agent resumes through
	// ResumeSessionID instead. The host forks the file rather than appending to
	// it, so the source conversation is never written to and two sessions can
	// start from the same one.
	ResumeConversationFile string

	// LoginShellEnv, when non-nil, is a pre-computed login shell environment
	// from the daemon's cache. Skips the ~130ms readLoginShellEnv in workers.
	LoginShellEnv []string

	// WorkflowGuidanceEnabled mirrors workflows_enabled; exported as
	// ATTN_WORKFLOW_GUIDANCE_ENABLED.
	WorkflowGuidanceEnabled bool

	// AutoApprove mirrors auto_approve_enabled; exported as ATTN_AUTO_APPROVE.
	// Yolo overrides it.
	AutoApprove bool
	// ApprovalRoute is the effective launch-time approval destination,
	// persisted so a replacement daemon can reconstruct guardian evidence.
	// Empty only for legacy callers predating route recording.
	ApprovalRoute launchcontract.ApprovalRoute
	// TrustWorkingDirectory is set only for unattended daemon-owned launches.
	TrustWorkingDirectory bool

	// Model, when set, pins the agent's model via --model (exported as ATTN_MODEL).
	Model string

	// Effort, when set, pins reasoning effort via the agent's native mechanism
	// (exported as ATTN_EFFORT).
	Effort string

	// ContextWindowCap, when > 0, caps the context window at that token
	// threshold (exported as ATTN_AUTO_COMPACT_WINDOW); the daemon owns the policy.
	ContextWindowCap int

	// UnattendedLaunch, when set, is the sole source for agent, executable,
	// approval, trust, model, effort, and recovery policy across all paths.
	UnattendedLaunch launchcontract.UnattendedLaunchSpec
}

func validateUnattendedSpawnOptions(opts SpawnOptions) error {
	if opts.ApprovalRoute != "" && !opts.ApprovalRoute.Valid() {
		return fmt.Errorf("invalid approval route %q", opts.ApprovalRoute)
	}
	launch := opts.UnattendedLaunch
	if launch.IsZero() {
		if opts.ApprovalRoute != "" {
			if want := launchcontract.ResolveApprovalRoute(opts.YoloMode, opts.AutoApprove, launch); opts.ApprovalRoute != want {
				return fmt.Errorf("approval route %q does not match effective launch route %q", opts.ApprovalRoute, want)
			}
		}
		return nil
	}
	if err := launch.Validate(); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(opts.Agent), strings.TrimSpace(launch.Agent)) {
		return fmt.Errorf("unattended launch agent %q does not match spawn agent %q", launch.Agent, opts.Agent)
	}
	if opts.AutoApprove || opts.TrustWorkingDirectory || strings.TrimSpace(opts.Model) != "" ||
		strings.TrimSpace(opts.Effort) != "" || strings.TrimSpace(opts.Executable) != "" {
		return errors.New("unattended launch policy must not be duplicated in spawn options")
	}
	if opts.ApprovalRoute != "" {
		if want := launchcontract.ResolveApprovalRoute(opts.YoloMode, opts.AutoApprove, launch); opts.ApprovalRoute != want {
			return fmt.Errorf("approval route %q does not match effective launch route %q", opts.ApprovalRoute, want)
		}
	}
	return nil
}

type AttachInfo struct {
	LastSeq    uint32
	Cols       uint16
	Rows       uint16
	PID        int
	Running    bool
	ExitCode   *int
	ExitSignal *string
	// GhosttySnapshot is the server-authoritative VT serialization of the whole
	// terminal (geometry Cols/Rows); nil when absent.
	GhosttySnapshot []byte
	// GhosttyBlocks are OSC 133 command blocks resolved to SCREEN-space rows of
	// GhosttySnapshot, captured atomically with it and LastSeq; nil when absent.
	GhosttyBlocks []pty.AttachBlockData
	// GhosttyPlacements is the serialized screen's kitty placement set,
	// captured in the same hold; nil when the session holds no images.
	GhosttyPlacements []pty.KittyPlacement
	// GhosttyScrollbackTruncated: scrollback was dropped at the cap before
	// GhosttySnapshot was serialized.
	GhosttyScrollbackTruncated bool
}

type OutputEvent struct {
	Kind   string
	Data   []byte
	Seq    uint32
	Reason string
	// Placements is the full set on OutputEventKindPlacements — an empty set is
	// how a client learns the last image is gone.
	Placements []pty.KittyPlacement
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool
	State   string

	// LastSignal is the newest signal-observer level (HasLastSignal false when
	// none). Evidence, not a state claim: it lets a restarted daemon learn a
	// quiet agent's heartbeat without waiting for a repaint that never comes.
	LastSignal    pty.Observation
	HasLastSignal bool

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
	// Resize applies a new grid; xpixel/ypixel are total device pixels, 0 when unknown.
	Resize(ctx context.Context, sessionID string, cols, rows, xpixel, ypixel uint16) error
	// SetTheme updates the OSC 10/11/12 answer colors. Best-effort: a worker
	// predating the method returns nil.
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
	SetStateHandler(func(sessionID string, obs pty.Observation))
}

type SessionInfoProvider interface {
	SessionInfo(ctx context.Context, sessionID string) (SessionInfo, error)
}

// SessionLaunchParams carries the launch flags recorded by a live worker —
// authoritative after a daemon restart; the durable launch intent is the fallback.
type SessionLaunchParams struct {
	// Recorded is false when the worker predates launch-param recording; the
	// daemon must then abort the reload rather than respawn with defaults.
	Recorded          bool
	YoloMode          bool
	ApprovalRoute     launchcontract.ApprovalRoute
	Executable        string
	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	Model             string
	Effort            string
	UnattendedLaunch  launchcontract.UnattendedLaunchSpec
}

// SessionLaunchParamsProvider returns the recorded launch params for a live
// session; backends that cannot (e.g. embedded) omit it and the reload aborts.
type SessionLaunchParamsProvider interface {
	SessionLaunchParams(ctx context.Context, sessionID string) (SessionLaunchParams, error)
}

// WorkerProcessProvider exposes per-session worker PIDs so diagnostics can sum
// per-session RSS. Backends without subprocesses (e.g. embedded) omit it.
type WorkerProcessProvider interface {
	WorkerPIDs(ctx context.Context) map[string]int
}

// SnapshotProvider returns the current rendered screen of a session without
// attaching. Backends that cannot return an error; callers degrade gracefully.
type SnapshotProvider interface {
	Snapshot(ctx context.Context, sessionID string) (pty.SnapshotInfo, error)
}

// KittyImageProvider copies one stored image out of a session's terminal by
// ghostty image id. Optional; on error the caller drops that placement's render.
type KittyImageProvider interface {
	KittyImage(ctx context.Context, sessionID string, imageID uint32) (pty.KittyImage, error)
}

type SessionLivenessProber interface {
	SessionLikelyAlive(ctx context.Context, sessionID string) (bool, error)
}

type RecoverableRuntime interface {
	Backend
	SessionInfoProvider
	SessionLivenessProber
}
