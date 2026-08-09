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
	// OutputEventKindPlacements carries the session's whole kitty placement set
	// as of the chunk stamped Seq. It rides the attach stream rather than a
	// side channel because it only means anything in order against the bytes:
	// the positions were measured on the grid the same-seq output produces.
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

	// WorkflowGuidanceEnabled mirrors the daemon's workflows_enabled setting. When
	// true the worker exports ATTN_WORKFLOW_GUIDANCE_ENABLED so the launched agent's
	// instructions include the workflow-trigger guidance.
	WorkflowGuidanceEnabled bool

	// AutoApprove mirrors the daemon's auto_approve_enabled setting. When true the
	// worker exports ATTN_AUTO_APPROVE so the launched agent starts in its native
	// auto-approve mode (Claude --permission-mode auto). Yolo overrides it.
	AutoApprove bool
	// ApprovalRoute is the effective, launch-time destination for approval
	// requests. The worker persists it so a replacement daemon can reconstruct
	// guardian evidence without consulting a later global setting. Empty is
	// accepted only for legacy/internal callers that predate route recording.
	ApprovalRoute launchcontract.ApprovalRoute
	// TrustWorkingDirectory is set only for unattended daemon-owned launches.
	TrustWorkingDirectory bool

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

	// UnattendedLaunch is the daemon-owned launch contract. When set, it is the
	// sole source for agent, executable, approval, trust, model, effort, and
	// recovery policy across embedded, worker, and reload paths.
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
	// terminal from libghostty-vt (geometry is Cols/Rows). nil when absent.
	GhosttySnapshot []byte
	// GhosttyBlocks are the worker's OSC 133 command blocks resolved to
	// SCREEN-space rows of GhosttySnapshot, captured atomically with it and
	// LastSeq (Phase 3a). nil when absent.
	GhosttyBlocks []pty.AttachBlockData
	// GhosttyPlacements is the kitty placement set of the screen
	// GhosttySnapshot serializes, captured in that same hold. nil when the
	// session holds no images.
	GhosttyPlacements []pty.KittyPlacement
	// GhosttyScrollbackTruncated reports whether the ghostty terminal dropped
	// scrollback lines at its cap before GhosttySnapshot was serialized.
	GhosttyScrollbackTruncated bool
}

type OutputEvent struct {
	Kind   string
	Data   []byte
	Seq    uint32
	Reason string
	// Placements is the full set on OutputEventKindPlacements, empty included —
	// an empty set is how a client learns the last image is gone.
	Placements []pty.KittyPlacement
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool
	State   string

	// LastSignal is the newest level the session's signal observers emitted, and
	// false when it has produced none. It is evidence rather than a state claim,
	// and it is what lets a daemon that restarted learn what a quiet agent's
	// heartbeat currently says instead of waiting for the next repaint that a
	// session parked at its prompt will never produce.
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
	// Resize applies a new grid. xpixel/ypixel are the pane's total size in
	// device pixels, or 0 when the caller has none to report.
	Resize(ctx context.Context, sessionID string, cols, rows, xpixel, ypixel uint16) error
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
	SetStateHandler(func(sessionID string, obs pty.Observation))
}

type SessionInfoProvider interface {
	SessionInfo(ctx context.Context, sessionID string) (SessionInfo, error)
}

// SessionLaunchParams carries the launch flags recorded by a live worker. The
// worker registry is authoritative for the process that actually survived a
// daemon restart; the durable launch intent is the fallback when no worker does.
type SessionLaunchParams struct {
	// Recorded is false for sessions whose worker predates launch-param recording.
	// The daemon must NOT trust the other fields when false and must abort the
	// reload rather than respawn with defaulted launch flags.
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
	Snapshot(ctx context.Context, sessionID string) (pty.SnapshotInfo, error)
}

// KittyImageProvider copies one stored image out of a session's terminal, by
// the ghostty image id a placement carries. Optional like SnapshotProvider: a
// backend that cannot serve one (a worker built before the method existed)
// simply is not asked twice — the caller drops that placement's render, and the
// error names the id it could not find.
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
