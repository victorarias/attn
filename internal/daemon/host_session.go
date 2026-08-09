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

// Conversation sessions: the agent runs in a headless host process instead of a
// PTY — no bytes, grid, or attach, just an envelope stream out and a prompt verb
// in. A plugin driver opts in via the "conversation" capability.

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

// liveRuntimeSessionIDs is every session with a runtime behind it, PTY- or
// host-backed. Liveness checks must ask this, never the PTY backend alone.
func (d *Daemon) liveRuntimeSessionIDs(ctx context.Context) []string {
	var ids []string
	if d.ptyBackend != nil {
		ids = append(ids, d.ptyBackend.SessionIDs(ctx)...)
	}
	return append(ids, d.ensureHostSessions().SessionIDs()...)
}

// hostSessionLogPath keeps a host's stdout/stderr beside the PTY worker logs,
// one file per session.
func hostSessionLogPath(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "log", sessionID+".log")
}

// hostSessionStateDir is under attn's data dir, never the agent's own home.
func hostSessionStateDir(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "state", sessionID)
}

// loginShellEnvForSpawn is the user's login shell environment, captured now if
// the pre-warm has not landed, so the first session matches the second.
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

// spawnHostSession starts the host for a conversation session.
func (d *Daemon) spawnHostSession(opts ptybackend.SpawnOptions) error {
	// Daemon env, then login shell on top: credentials live in shell profiles and
	// an app-launched host would otherwise fail its first prompt with "no API key".
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
func (d *Daemon) spawnSessionRuntime(req *spawnRequest, opts ptybackend.SpawnOptions) error {
	if req.hasPluginDriver && req.pluginDriver.Capabilities[pluginDriverConversationCapability] {
		return d.spawnHostSession(opts)
	}
	return d.ptyBackend.Spawn(context.Background(), opts)
}

// killSessionRuntime stops a session's runtime, whichever kind it is.
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

// handleHostEvent forwards one envelope to every connected client. A stream, not
// a state change: pty_output's direct path, not the bus, and no store writes.
func (d *Daemon) handleHostEvent(event hostsession.Event) {
	d.wsHub.BroadcastValue(&protocol.AgentEventMessage{
		Event: protocol.EventAgentEvent,
		ID:    event.SessionID,
		Seq:   event.Seq,
		Kind:  event.Kind,
		Body:  event.Body,
	})
}

// handleHostExit routes a dead host into the same exit path a dead PTY worker takes.
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
		// The app closed its composer on send, so a prompt that never reached a host
		// comes back as the run it will never open; seq 0 marks a daemon envelope.
		d.handleHostEvent(hostsession.Event{
			SessionID: sessionID,
			Kind:      "run_settled",
			Body:      map[string]interface{}{"error": "this conversation's agent is no longer running"},
		})
	}
}
