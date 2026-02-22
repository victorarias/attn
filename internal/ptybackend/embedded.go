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
		manager = pty.NewManager(pty.DefaultScrollbackSize, nil)
	}
	return &EmbeddedBackend{manager: manager}
}

func (b *EmbeddedBackend) SetExitHandler(handler func(ExitInfo)) {
	if handler == nil {
		b.manager.SetExitHandler(nil)
		return
	}
	b.manager.SetExitHandler(func(info pty.ExitInfo) {
		handler(ExitInfo{ID: info.ID, ExitCode: info.ExitCode, Signal: info.Signal})
	})
}

func (b *EmbeddedBackend) SetStateHandler(handler func(sessionID, state string)) {
	b.manager.SetStateHandler(handler)
}

func (b *EmbeddedBackend) Spawn(_ context.Context, opts SpawnOptions) error {
	return b.manager.Spawn(pty.SpawnOptions{
		ID:                opts.ID,
		CWD:               opts.CWD,
		Agent:             opts.Agent,
		Label:             opts.Label,
		Cols:              opts.Cols,
		Rows:              opts.Rows,
		ResumeSessionID:   opts.ResumeSessionID,
		ResumePicker:      opts.ResumePicker,
		ForkSession:       opts.ForkSession,
		Executable:        opts.Executable,
		ClaudeExecutable:  opts.ClaudeExecutable,
		CodexExecutable:   opts.CodexExecutable,
		CopilotExecutable: opts.CopilotExecutable,
		PiExecutable:      opts.PiExecutable,
	})
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
	)
	if err != nil {
		stream.Close()
		return AttachInfo{}, nil, err
	}

	return AttachInfo{
		Scrollback:          info.Scrollback,
		ScrollbackTruncated: info.ScrollbackTruncated,
		LastSeq:             info.LastSeq,
		Cols:                info.Cols,
		Rows:                info.Rows,
		PID:                 info.PID,
		Running:             info.Running,
		ExitCode:            info.ExitCode,
		ExitSignal:          info.ExitSignal,
		ScreenSnapshot:      info.ScreenSnapshot,
		ScreenCols:          info.ScreenCols,
		ScreenRows:          info.ScreenRows,
		ScreenCursorX:       info.ScreenCursorX,
		ScreenCursorY:       info.ScreenCursorY,
		ScreenCursorVisible: info.ScreenCursorVisible,
		ScreenSnapshotFresh: info.ScreenSnapshotFresh,
	}, stream, nil
}

func (b *EmbeddedBackend) Input(_ context.Context, sessionID string, data []byte) error {
	return b.manager.Input(sessionID, data)
}

func (b *EmbeddedBackend) Resize(_ context.Context, sessionID string, cols, rows uint16) error {
	return b.manager.Resize(sessionID, cols, rows)
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
