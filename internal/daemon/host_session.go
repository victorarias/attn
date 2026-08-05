package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Conversation sessions: attn sessions whose agent runs in a headless host
// process instead of a PTY. The daemon owns the host's lifetime exactly as it
// owns a PTY worker's; what changes is the surface. There are no bytes, no
// grid, and no attach — there is an envelope stream the app draws from and a
// prompt verb going the other way.
//
// A plugin driver opts its agent in by registering the "conversation"
// capability. Everything else about the spawn — argv, env, cwd — comes back
// from the same driver.spawn call a PTY-backed plugin agent uses, so a
// conversation agent is configured, listed, and launched like any other.

// pluginDriverConversationCapability is the driver capability that routes an
// agent's sessions to the host runtime instead of the PTY backend.
const pluginDriverConversationCapability = "conversation"

func (d *Daemon) ensureHostSessions() *hostsession.Manager {
	d.hostSessionsMu.Lock()
	defer d.hostSessionsMu.Unlock()
	if d.hostSessions == nil {
		d.hostSessions = hostsession.New(d.logf, d.handleHostEvent, d.handleHostExit)
	}
	return d.hostSessions
}

// isConversationAgent reports whether sessions for this agent run in a host.
func (d *Daemon) isConversationAgent(agent string) bool {
	driver, ok := d.ensurePluginRegistry().driver(agent)
	return ok && driver.Capabilities[pluginDriverConversationCapability]
}

// isHostSession reports whether a live host owns this session right now.
func (d *Daemon) isHostSession(sessionID string) bool {
	return d.ensureHostSessions().Has(sessionID)
}

// liveRuntimeSessionIDs is every session with a runtime behind it, PTY-backed
// or host-backed. Anything that asks "is this session still alive?" — pruning
// dead rows, reconciling workspace panes, tearing everything down — must ask
// this and not the PTY backend alone, or a live conversation session reads as
// abandoned.
func (d *Daemon) liveRuntimeSessionIDs(ctx context.Context) []string {
	var ids []string
	if d.ptyBackend != nil {
		ids = append(ids, d.ptyBackend.SessionIDs(ctx)...)
	}
	return append(ids, d.ensureHostSessions().SessionIDs()...)
}

// hostSessionLogPath keeps a host's own stdout/stderr beside the PTY worker
// logs, one file per session, so a wedged host is diagnosable the same way a
// wedged worker is.
func hostSessionLogPath(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "log", sessionID+".log")
}

// hostSessionStateDir is where a host keeps whatever it persists for the
// session — for pi, its own session file. Under attn's data dir, never the
// agent's own home, so attn owns what it spawned.
func hostSessionStateDir(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "state", sessionID)
}

// loginShellEnvForSpawn is the user's login shell environment, captured now if
// the daemon's pre-warm has not landed yet. The PTY path makes the same
// fallback, for the same reason: the first session of a daemon's life must not
// get a thinner environment than the second.
func (d *Daemon) loginShellEnvForSpawn() []string {
	if cached := d.cachedLoginShellEnv(); len(cached) > 0 {
		return cached
	}
	shell := pty.GetUserLoginShell()
	if shell == "" {
		return nil
	}
	env, err := pty.ReadLoginShellEnv(shell)
	if err != nil {
		d.logf("host spawn: failed to capture login shell env from %s: %v", shell, err)
		return nil
	}
	return env
}

// spawnHostSession starts the host for a conversation session. It takes the
// same launch description the PTY path would have run.
func (d *Daemon) spawnHostSession(opts ptybackend.SpawnOptions) error {
	// A host gets the same environment a PTY agent gets, layered the same way:
	// the daemon's own environment, then the user's login shell on top. Agents
	// read credentials from there — an API key exported in a shell profile is
	// the ordinary way pi is authenticated — and a host launched from the app
	// (which the window server starts with almost no environment) would
	// otherwise fail its first prompt with "no API key found" for a key the
	// user has had set for years.
	env := pty.MergeEnvironment(os.Environ(), d.loginShellEnvForSpawn())
	env = pty.MergeEnvironment(env, opts.ExternalEnv)
	env = pty.MergeEnvironment(env, []string{
		"ATTN_PI_HOST_SESSION_ID=" + opts.ID,
		"ATTN_PI_HOST_SESSION_DIR=" + hostSessionStateDir(opts.ID),
		"ATTN_PI_HOST_CWD=" + opts.CWD,
	})
	cwd := strings.TrimSpace(opts.ExternalCWD)
	if cwd == "" {
		cwd = opts.CWD
	}
	return d.ensureHostSessions().Spawn(hostsession.SpawnOptions{
		SessionID:   opts.ID,
		LifecycleID: opts.LifecycleID,
		Command:     opts.ExternalCommand,
		Env:         env,
		CWD:         cwd,
		LogPath:     hostSessionLogPath(opts.ID),
	})
}

// spawnSessionRuntime starts whichever runtime this session's agent asked for.
// It is the one fork in the spawn pipeline: everything before it (validation,
// launch intent, the driver's argv) and everything after it (the session row,
// panes, facts) is identical for both kinds.
func (d *Daemon) spawnSessionRuntime(req *spawnRequest, opts ptybackend.SpawnOptions) error {
	if req.hasPluginDriver && req.pluginDriver.Capabilities[pluginDriverConversationCapability] {
		return d.spawnHostSession(opts)
	}
	return d.ptyBackend.Spawn(context.Background(), opts)
}

// killSessionRuntime stops a session's runtime, whichever kind it is. Used by
// the spawn pipeline's rollbacks, where the session may be either.
func (d *Daemon) killSessionRuntime(sessionID string) error {
	if d.isHostSession(sessionID) {
		if err := d.ensureHostSessions().Kill(sessionID); err != nil && !errors.Is(err, hostsession.ErrNotFound) {
			return err
		}
		return nil
	}
	return d.ptyBackend.Kill(context.Background(), sessionID, syscall.SIGTERM)
}

// removeSessionRuntime drops a session's runtime record. A host has none to
// drop: Kill already returned with the process group gone.
func (d *Daemon) removeSessionRuntime(sessionID string) error {
	if d.isHostSession(sessionID) {
		return nil
	}
	return d.ptyBackend.Remove(context.Background(), sessionID)
}

// handleHostEvent forwards one envelope to every connected client.
//
// This is a stream, not a state change: it takes the same direct path
// pty_output does rather than riding the event bus. The daemon's picture of a
// session must be complete without reading one of these, which is why nothing
// here writes to the store.
func (d *Daemon) handleHostEvent(event hostsession.Event) {
	d.wsHub.BroadcastValue(&protocol.AgentEventMessage{
		Event: protocol.EventAgentEvent,
		ID:    event.SessionID,
		Seq:   event.Seq,
		Kind:  event.Kind,
		Body:  event.Body,
	})
}

// handleHostExit routes a dead host into the same exit path a dead PTY worker
// takes: the session's end is the session's end, whatever was running it.
func (d *Daemon) handleHostExit(info hostsession.ExitInfo) {
	d.handlePTYExit(ptybackend.ExitInfo{
		ID:          info.SessionID,
		ExitCode:    info.ExitCode,
		Signal:      info.Signal,
		LifecycleID: info.LifecycleID,
	})
}

// handleAgentPrompt answers the agent_prompt command.
func (d *Daemon) handleAgentPrompt(client *wsClient, msg *protocol.AgentPromptMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	text := strings.TrimSpace(msg.Text)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing a session id")
		return
	}
	if text == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing text")
		return
	}
	if err := d.ensureHostSessions().Prompt(sessionID, text); err != nil {
		d.logf("agent_prompt for session %s failed: %v", sessionID, err)
		d.sendCommandError(client, protocol.CmdAgentPrompt, "no live conversation host for session "+sessionID)
	}
}
