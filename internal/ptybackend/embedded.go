package ptybackend

import (
	"context"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/pty"
)

type EmbeddedBackend struct {
	manager *pty.Manager
}

func NewEmbedded(manager *pty.Manager) *EmbeddedBackend {
	if manager == nil {
		manager = pty.NewManager(nil)
	}
	return &EmbeddedBackend{manager: manager}
}

func (b *EmbeddedBackend) SetExitHandler(handler func(ExitInfo)) {
	if handler == nil {
		b.manager.SetExitHandler(nil)
		return
	}
	b.manager.SetExitHandler(func(info pty.ExitInfo) {
		handler(ExitInfo{ID: info.ID, ExitCode: info.ExitCode, Signal: info.Signal, LifecycleID: info.LifecycleID})
	})
}

func (b *EmbeddedBackend) SetStateHandler(handler func(sessionID string, obs pty.Observation)) {
	b.manager.SetStateHandler(handler)
}

func (b *EmbeddedBackend) Spawn(_ context.Context, opts SpawnOptions) error {
	if err := validateUnattendedSpawnOptions(opts); err != nil {
		return err
	}
	return b.manager.Spawn(embeddedSpawnOptions(opts))
}

func embeddedSpawnOptions(opts SpawnOptions) pty.SpawnOptions {
	return pty.SpawnOptions{
		ID:                opts.ID,
		CWD:               opts.CWD,
		Agent:             opts.Agent,
		Label:             opts.Label,
		Cols:              opts.Cols,
		Rows:              opts.Rows,
		ResumeSessionID:   opts.ResumeSessionID,
		ResumePicker:      opts.ResumePicker,
		YoloMode:          opts.YoloMode,
		InitialPromptFile: opts.InitialPromptFile,
		Theme:             opts.Theme,
		Executable:        opts.Executable,
		ClaudeExecutable:  opts.ClaudeExecutable,
		CodexExecutable:   opts.CodexExecutable,
		CopilotExecutable: opts.CopilotExecutable,
		ExternalCommand:   opts.ExternalCommand,
		ExternalEnv:       opts.ExternalEnv,
		ExternalCWD:       opts.ExternalCWD,
		LifecycleID:       opts.LifecycleID,
		LoginShellEnv:     opts.LoginShellEnv,

		WorkflowGuidanceEnabled: opts.WorkflowGuidanceEnabled,
		AutoApprove:             opts.AutoApprove,
		TrustWorkingDirectory:   opts.TrustWorkingDirectory,
		Model:                   opts.Model,
		Effort:                  opts.Effort,
		ChiefContextWindowCap:   opts.ChiefContextWindowCap,
		UnattendedLaunch:        opts.UnattendedLaunch,
	}
}

func (b *EmbeddedBackend) Attach(_ context.Context, sessionID, subscriberID string) (AttachInfo, Stream, error) {
	events := make(chan OutputEvent, 128)
	stream := &embeddedStream{
		events: events,
		closeFn: func() {
			b.manager.Detach(sessionID, subscriberID)
		},
	}

	info, err := b.manager.Attach(
		sessionID,
		subscriberID,
		func(data []byte, seq uint32) bool {
			payload := append([]byte(nil), data...)
			return stream.publish(OutputEvent{Kind: OutputEventKindOutput, Data: payload, Seq: seq})
		},
		func(reason string) {
			_ = stream.publish(OutputEvent{Kind: OutputEventKindDesync, Reason: reason})
			stream.Close()
		},
		// Same stream as the bytes, so a set stays ordered behind the output it
		// was measured on — the worker backend gets that ordering from the
		// connection's send queue, this one from the stream channel.
		pty.OnPlacements(func(update pty.PlacementUpdate) {
			_ = stream.publish(OutputEvent{
				Kind:       OutputEventKindPlacements,
				Seq:        update.Seq,
				Placements: update.Placements,
			})
		}),
	)
	if err != nil {
		stream.Close()
		return AttachInfo{}, nil, err
	}

	return AttachInfo{
		LastSeq:                    info.LastSeq,
		Cols:                       info.Cols,
		Rows:                       info.Rows,
		PID:                        info.PID,
		Running:                    info.Running,
		ExitCode:                   info.ExitCode,
		ExitSignal:                 info.ExitSignal,
		GhosttySnapshot:            info.GhosttySnapshot,
		GhosttyBlocks:              info.GhosttyBlocks,
		GhosttyPlacements:          info.GhosttyPlacements,
		GhosttyScrollbackTruncated: info.GhosttyScrollbackTruncated,
	}, stream, nil
}

// KittyImage serves the pixels behind a placement straight out of the session's
// terminal — no hop, because this backend hosts the terminal in-process.
func (b *EmbeddedBackend) KittyImage(_ context.Context, sessionID string, imageID uint32) (pty.KittyImage, error) {
	return b.manager.KittyImage(sessionID, imageID)
}

func (b *EmbeddedBackend) Snapshot(_ context.Context, sessionID string) (pty.SnapshotInfo, error) {
	return b.manager.Snapshot(sessionID)
}

func (b *EmbeddedBackend) Input(_ context.Context, sessionID string, data []byte) error {
	return b.manager.Input(sessionID, data)
}

func (b *EmbeddedBackend) Resize(_ context.Context, sessionID string, cols, rows, xpixel, ypixel uint16) error {
	return b.manager.Resize(sessionID, cols, rows, xpixel, ypixel)
}

func (b *EmbeddedBackend) SetTheme(_ context.Context, sessionID string, theme pty.TerminalTheme) error {
	return b.manager.SetTheme(sessionID, theme)
}

func (b *EmbeddedBackend) Kill(_ context.Context, sessionID string, sig syscall.Signal) error {
	return b.manager.Kill(sessionID, sig)
}

func (b *EmbeddedBackend) Remove(_ context.Context, sessionID string) error {
	b.manager.Remove(sessionID)
	return nil
}

func (b *EmbeddedBackend) SessionIDs(_ context.Context) []string {
	return b.manager.SessionIDs()
}

func (b *EmbeddedBackend) Recover(_ context.Context) (RecoveryReport, error) {
	return RecoveryReport{Recovered: len(b.manager.SessionIDs())}, nil
}

func (b *EmbeddedBackend) Shutdown(_ context.Context) error {
	b.manager.Shutdown()
	return nil
}

func (b *EmbeddedBackend) SessionInfo(_ context.Context, sessionID string) (SessionInfo, error) {
	info, err := b.manager.SessionInfo(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{
		SessionID:  info.SessionID,
		Agent:      info.Agent,
		CWD:        info.CWD,
		Running:    info.Running,
		Cols:       info.Cols,
		Rows:       info.Rows,
		PID:        info.PID,
		LastSeq:    info.LastSeq,
		ExitCode:   info.ExitCode,
		ExitSignal: info.ExitSignal,
	}, nil
}

type embeddedStream struct {
	events    chan OutputEvent
	closeFn   func()
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

func (s *embeddedStream) Events() <-chan OutputEvent {
	return s.events
}

func (s *embeddedStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if s.closeFn != nil {
			s.closeFn()
		}
		s.mu.Lock()
		close(s.events)
		s.mu.Unlock()
	})
	return nil
}

func (s *embeddedStream) publish(evt OutputEvent) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.events <- evt:
		return true
	default:
		return false
	}
}
