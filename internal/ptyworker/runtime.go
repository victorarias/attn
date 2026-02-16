package ptyworker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/pty"
)

var exitedSessionCleanupTTL = 45 * time.Second

const (
	connSendQueueSize       = 256
	connWriteTimeout        = 2 * time.Second
	connResponseSendTimeout = 500 * time.Millisecond
	connHelloTimeout        = 5 * time.Second
	connIdleReadTimeout     = 3 * time.Minute
)

type Config struct {
	DaemonInstanceID string
	SessionID        string
	Agent            string
	CWD              string
	Cols             uint16
	Rows             uint16
	Label            string

	ResumeSessionID string
	ResumePicker    bool
	ForkSession     bool

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string

	RegistryPath   string
	SocketPath     string
	ControlToken   string
	OwnerPID       int
	OwnerStartedAt string
	OwnerNonce     string

	Logf func(format string, args ...interface{})
}

type Runtime struct {
	cfg      Config
	manager  *pty.Manager
	listener net.Listener
	logf     func(format string, args ...interface{})
	capture  *debugCapture

	stateMu    sync.RWMutex
	state      string
	exitCode   *int
	exitSignal *string

	stopOnce sync.Once
	stopCh   chan struct{}

	lifecycleMu sync.Mutex
	authedConns int
	exited      bool
	cleanupTTL  *time.Timer

	connSeq atomic.Uint64

	watchMu   sync.RWMutex
	watchConn map[*connCtx]struct{}
}

func Run(ctx context.Context, cfg Config) error {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	rt := &Runtime{
		cfg:       cfg,
		state:     "working",
		stopCh:    make(chan struct{}),
		logf:      logf,
		watchConn: make(map[*connCtx]struct{}),
	}
	return rt.run(ctx)
}

func (r *Runtime) run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}

	r.manager = pty.NewManager(pty.DefaultScrollbackSize, r.logf)
	r.manager.SetStateHandler(func(_ string, state string) {
		r.stateMu.Lock()
		previousState := r.state
		changed := previousState != state
		r.state = state
		r.stateMu.Unlock()
		if r.capture != nil {
			r.capture.recordState(state)
			if isWorkingToStopTransition(previousState, state) {
				path, err := r.capture.dump("working_to_" + state)
				if err != nil {
					r.logf("worker debug capture dump failed: session=%s reason=%s err=%v", r.cfg.SessionID, state, err)
				} else if path != "" {
					r.logf("worker debug capture dump: session=%s reason=working_to_%s path=%s", r.cfg.SessionID, state, path)
				}
			}
		}
		if changed {
			r.broadcastLifecycle(EventEnvelope{
				Type:      "evt",
				Event:     EventStateChanged,
				SessionID: r.cfg.SessionID,
				State:     &state,
			})
		}
	})
	r.manager.SetExitHandler(func(info pty.ExitInfo) {
		r.stateMu.Lock()
		code := info.ExitCode
		r.exitCode = &code
		if info.Signal != "" {
			sig := info.Signal
			r.exitSignal = &sig
		}
		r.stateMu.Unlock()
		if r.capture != nil {
			r.capture.recordNote(fmt.Sprintf("exit code=%d signal=%s", info.ExitCode, info.Signal))
			path, err := r.capture.dump("exit")
			if err != nil {
				r.logf("worker debug capture dump failed: session=%s reason=exit err=%v", r.cfg.SessionID, err)
			} else if path != "" {
				r.logf("worker debug capture dump: session=%s reason=exit path=%s", r.cfg.SessionID, path)
			}
		}
		r.noteSessionExited()
		r.broadcastLifecycle(EventEnvelope{
			Type:       "evt",
			Event:      EventExit,
			SessionID:  r.cfg.SessionID,
			ExitCode:   &code,
			ExitSignal: r.exitSignal,
		})
	})

	if err := os.MkdirAll(filepath.Dir(r.cfg.SocketPath), 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.cfg.RegistryPath), 0700); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}

	_ = os.Remove(r.cfg.SocketPath)
	listener, err := net.Listen("unix", r.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}
	r.listener = listener
	_ = os.Chmod(r.cfg.SocketPath, 0600)

	defer func() {
		r.requestStop()
		if r.manager != nil {
			r.manager.Detach(r.cfg.SessionID, debugCaptureSubscriberID)
		}
		if r.capture != nil {
			path, err := r.capture.dump("runtime_shutdown")
			if err != nil {
				r.logf("worker debug capture dump failed: session=%s reason=runtime_shutdown err=%v", r.cfg.SessionID, err)
			} else if path != "" {
				r.logf("worker debug capture dump: session=%s reason=runtime_shutdown path=%s", r.cfg.SessionID, path)
			}
		}
		if r.manager != nil {
			r.manager.Shutdown()
		}
		r.cleanup()
	}()

	if err := r.manager.Spawn(pty.SpawnOptions{
		ID:                r.cfg.SessionID,
		CWD:               r.cfg.CWD,
		Agent:             r.cfg.Agent,
		Label:             r.cfg.Label,
		Cols:              r.cfg.Cols,
		Rows:              r.cfg.Rows,
		ResumeSessionID:   r.cfg.ResumeSessionID,
		ResumePicker:      r.cfg.ResumePicker,
		ForkSession:       r.cfg.ForkSession,
		ClaudeExecutable:  r.cfg.ClaudeExecutable,
		CodexExecutable:   r.cfg.CodexExecutable,
		CopilotExecutable: r.cfg.CopilotExecutable,
	}); err != nil {
		return fmt.Errorf("spawn PTY session: %w", err)
	}
	r.capture = newDebugCapture(r.cfg, r.logf)
	if r.capture != nil {
		r.capture.recordNote("capture enabled")
		r.capture.recordState("working")
		_, err := r.manager.Attach(
			r.cfg.SessionID,
			debugCaptureSubscriberID,
			func(data []byte, seq uint32) bool {
				if r.capture != nil {
					r.capture.recordOutput(seq, data)
				}
				return true
			},
			func(reason string) {
				if r.capture != nil {
					r.capture.recordNote("capture subscriber drop: " + reason)
				}
			},
		)
		if err != nil {
			r.logf("worker debug capture attach failed: session=%s err=%v", r.cfg.SessionID, err)
			r.capture = nil
		} else {
			r.logf("worker debug capture enabled: session=%s", r.cfg.SessionID)
		}
	}

	info, err := r.sessionInfo()
	if err != nil {
		return fmt.Errorf("load initial session info: %w", err)
	}
	entry := NewRegistryEntry(
		r.cfg.DaemonInstanceID,
		r.cfg.SessionID,
		os.Getpid(),
		info.PID,
		r.cfg.SocketPath,
		r.cfg.Agent,
		r.cfg.CWD,
		r.cfg.ControlToken,
	)
	entry.OwnerPID = r.cfg.OwnerPID
	entry.OwnerStartedAt = r.cfg.OwnerStartedAt
	entry.OwnerNonce = r.cfg.OwnerNonce
	if err := WriteRegistryAtomic(r.cfg.RegistryPath, entry); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		r.requestStop()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-r.stopCh:
				return nil
			default:
			}
			if isTemporary(err) {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept worker connection: %w", err)
		}
		go r.handleConn(conn)
	}
}

func (r *Runtime) validate() error {
	if strings.TrimSpace(r.cfg.DaemonInstanceID) == "" {
		return errors.New("missing --daemon-instance-id")
	}
	if strings.TrimSpace(r.cfg.SessionID) == "" {
		return errors.New("missing --session-id")
	}
	if strings.TrimSpace(r.cfg.Agent) == "" {
		return errors.New("missing --agent")
	}
	if strings.TrimSpace(r.cfg.CWD) == "" {
		return errors.New("missing --cwd")
	}
	if strings.TrimSpace(r.cfg.RegistryPath) == "" {
		return errors.New("missing --registry-path")
	}
	if strings.TrimSpace(r.cfg.SocketPath) == "" {
		return errors.New("missing --socket-path")
	}
	if strings.TrimSpace(r.cfg.ControlToken) == "" {
		return errors.New("missing --control-token")
	}
	if r.cfg.OwnerPID <= 0 {
		return errors.New("missing --owner-pid")
	}
	if strings.TrimSpace(r.cfg.OwnerStartedAt) == "" {
		return errors.New("missing --owner-started-at")
	}
	if strings.TrimSpace(r.cfg.OwnerNonce) == "" {
		return errors.New("missing --owner-nonce")
	}
	if r.cfg.Cols == 0 {
		r.cfg.Cols = 80
	}
	if r.cfg.Rows == 0 {
		r.cfg.Rows = 24
	}
	return nil
}

func (r *Runtime) cleanup() {
	r.lifecycleMu.Lock()
	if r.cleanupTTL != nil {
		r.cleanupTTL.Stop()
		r.cleanupTTL = nil
	}
	r.lifecycleMu.Unlock()

	_ = os.Remove(r.cfg.RegistryPath)
	_ = os.Remove(r.cfg.SocketPath)
}

func (r *Runtime) requestStop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		if r.listener != nil {
			_ = r.listener.Close()
		}
	})
}

func (r *Runtime) noteSessionExited() {
	r.lifecycleMu.Lock()
	r.exited = true
	r.maybeScheduleCleanupLocked()
	r.lifecycleMu.Unlock()
}

func (r *Runtime) noteConnAuthed() {
	r.lifecycleMu.Lock()
	r.authedConns++
	if r.cleanupTTL != nil {
		r.cleanupTTL.Stop()
		r.cleanupTTL = nil
	}
	r.lifecycleMu.Unlock()
}

func (r *Runtime) noteConnClosed() {
	r.lifecycleMu.Lock()
	if r.authedConns > 0 {
		r.authedConns--
	}
	r.maybeScheduleCleanupLocked()
	r.lifecycleMu.Unlock()
}

func (r *Runtime) maybeScheduleCleanupLocked() {
	if !r.exited || r.authedConns != 0 || r.cleanupTTL != nil {
		return
	}
	r.cleanupTTL = time.AfterFunc(exitedSessionCleanupTTL, func() {
		r.lifecycleMu.Lock()
		r.cleanupTTL = nil
		shouldStop := r.exited && r.authedConns == 0
		r.lifecycleMu.Unlock()
		if shouldStop {
			r.requestStop()
		}
	})
}

func (r *Runtime) addWatcher(conn *connCtx) {
	r.watchMu.Lock()
	r.watchConn[conn] = struct{}{}
	r.watchMu.Unlock()
}

func (r *Runtime) removeWatcher(conn *connCtx) {
	r.watchMu.Lock()
	delete(r.watchConn, conn)
	r.watchMu.Unlock()
}

func (r *Runtime) broadcastLifecycle(evt EventEnvelope) {
	r.watchMu.RLock()
	targets := make([]*connCtx, 0, len(r.watchConn))
	for watcher := range r.watchConn {
		targets = append(targets, watcher)
	}
	r.watchMu.RUnlock()

	for _, watcher := range targets {
		_ = watcher.sendEvent(evt)
	}
}

type connCtx struct {
	runtime  *Runtime
	conn     net.Conn
	enc      *json.Encoder
	dec      *json.Decoder
	sendMu   sync.RWMutex
	sendQ    chan any
	sendDone chan struct{}
	sendOnce sync.Once
	closed   bool
	connID   string
	authed   bool
	watching bool
	subID    string
	shutdown bool
}

func (r *Runtime) handleConn(conn net.Conn) {
	ctx := &connCtx{
		runtime:  r,
		conn:     conn,
		enc:      json.NewEncoder(conn),
		dec:      json.NewDecoder(conn),
		sendQ:    make(chan any, connSendQueueSize),
		sendDone: make(chan struct{}),
		connID:   strconv.FormatUint(r.connSeq.Add(1), 10),
	}
	go ctx.writeLoop()
	defer func() {
		if ctx.subID != "" {
			r.manager.Detach(r.cfg.SessionID, ctx.subID)
		}
		if ctx.watching {
			r.removeWatcher(ctx)
		}
		if ctx.authed {
			r.noteConnClosed()
		}
		ctx.closeSend()
		<-ctx.sendDone
		_ = conn.Close()
	}()

	for {
		readTimeout, useDeadline := ctx.nextReadTimeout()
		if useDeadline {
			_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		} else {
			// Streaming connections (attach/watch) stay mostly server-push and
			// can be idle for long periods. Keep them open until peer close.
			_ = conn.SetReadDeadline(time.Time{})
		}

		var req RequestEnvelope
		if err := ctx.dec.Decode(&req); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				ctx.runtime.logf("worker conn read timeout: conn=%s authed=%v", ctx.connID, ctx.authed)
			}
			ctx.runtime.logf("worker conn decode error: conn=%s err=%v", ctx.connID, err)
			return
		}
		if req.Type != "req" {
			ctx.sendError(req.ID, ErrBadRequest, "request type must be req")
			continue
		}
		ctx.handleRequest(req)
		if ctx.shutdown {
			return
		}
	}
}

func (c *connCtx) nextReadTimeout() (time.Duration, bool) {
	if !c.authed {
		return connHelloTimeout, true
	}
	if c.subID != "" || c.watching {
		return 0, false
	}
	return connIdleReadTimeout, true
}

func (c *connCtx) writeLoop() {
	defer close(c.sendDone)
	for msg := range c.sendQ {
		_ = c.conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
		if err := c.enc.Encode(msg); err != nil {
			c.runtime.logf("worker conn write error: conn=%s err=%v", c.connID, err)
			c.closeSend()
			return
		}
	}
}

func (c *connCtx) closeSend() {
	c.sendOnce.Do(func() {
		c.sendMu.Lock()
		c.closed = true
		close(c.sendQ)
		c.sendMu.Unlock()
	})
}

func (c *connCtx) enqueue(v any, wait time.Duration) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.closed {
		return false
	}
	if wait <= 0 {
		select {
		case c.sendQ <- v:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case c.sendQ <- v:
		return true
	case <-timer.C:
		return false
	}
}

func (c *connCtx) handleRequest(req RequestEnvelope) {
	switch req.Method {
	case MethodHello:
		var params HelloParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid hello params")
			return
		}
		if !IsCompatibleVersion(params.RPCMajor, params.RPCMinor) {
			c.runtime.logf("worker conn hello version mismatch: conn=%s got=%d.%d", c.connID, params.RPCMajor, params.RPCMinor)
			c.sendError(
				req.ID,
				ErrUnsupportedVersion,
				fmt.Sprintf(
					"rpc version incompatible: got=%d.%d supported=%d.%d..%d.%d",
					params.RPCMajor, params.RPCMinor,
					RPCMajor, MinCompatibleRPCMinor,
					RPCMajor, RPCMinor,
				),
			)
			c.shutdown = true
			return
		}
		if params.DaemonInstanceID != c.runtime.cfg.DaemonInstanceID || params.ControlToken != c.runtime.cfg.ControlToken {
			c.runtime.logf("worker conn hello unauthorized: conn=%s", c.connID)
			c.sendError(req.ID, ErrUnauthorized, "daemon identity or control token mismatch")
			c.shutdown = true
			return
		}
		if !c.authed {
			c.authed = true
			c.runtime.noteConnAuthed()
			c.runtime.logf("worker conn authed: conn=%s", c.connID)
		}
		c.sendResult(req.ID, HelloResult{
			WorkerVersion:    "attn",
			RPCMajor:         RPCMajor,
			RPCMinor:         RPCMinor,
			DaemonInstanceID: c.runtime.cfg.DaemonInstanceID,
			SessionID:        c.runtime.cfg.SessionID,
		})
		return
	}

	if !c.authed {
		c.runtime.logf("worker conn request before hello: conn=%s method=%s", c.connID, req.Method)
		c.sendError(req.ID, ErrUnauthorized, "hello required before method calls")
		c.shutdown = true
		return
	}

	switch req.Method {
	case MethodInfo:
		info, err := c.runtime.infoResult()
		if err != nil {
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.sendResult(req.ID, info)
	case MethodAttach:
		var params AttachParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				c.sendError(req.ID, ErrBadRequest, "invalid attach params")
				return
			}
		}
		if c.subID != "" {
			c.runtime.manager.Detach(c.runtime.cfg.SessionID, c.subID)
		}
		subID := strings.TrimSpace(params.SubscriberID)
		if subID == "" {
			subID = "conn-" + c.connID
		}
		info, err := c.runtime.manager.Attach(
			c.runtime.cfg.SessionID,
			subID,
			func(data []byte, seq uint32) bool {
				encoded := base64.StdEncoding.EncodeToString(data)
				return c.sendEvent(EventEnvelope{
					Type:      "evt",
					Event:     EventOutput,
					SessionID: c.runtime.cfg.SessionID,
					Seq:       &seq,
					Data:      &encoded,
				})
			},
			func(reason string) {
				_ = c.sendEvent(EventEnvelope{
					Type:      "evt",
					Event:     EventDesync,
					SessionID: c.runtime.cfg.SessionID,
					Reason:    &reason,
				})
			},
		)
		if err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.subID = subID
		c.runtime.logf("worker conn attached: conn=%s sub=%s", c.connID, subID)
		c.sendResult(req.ID, AttachResult{
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
		})
	case MethodDetach:
		if c.subID != "" {
			c.runtime.manager.Detach(c.runtime.cfg.SessionID, c.subID)
			c.runtime.logf("worker conn detached: conn=%s sub=%s", c.connID, c.subID)
			c.subID = ""
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodInput:
		var params InputParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid input params")
			return
		}
		data, err := base64.StdEncoding.DecodeString(params.Data)
		if err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid base64 input payload")
			return
		}
		if c.runtime.capture != nil {
			c.runtime.capture.recordInput(data)
		}
		if err := c.runtime.manager.Input(c.runtime.cfg.SessionID, data); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			if strings.Contains(strings.ToLower(err.Error()), "not running") {
				c.sendError(req.ID, ErrSessionNotRunning, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodResize:
		var params ResizeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid resize params")
			return
		}
		if params.Cols == 0 || params.Rows == 0 {
			c.sendError(req.ID, ErrBadRequest, "cols and rows must be > 0")
			return
		}
		if err := c.runtime.manager.Resize(c.runtime.cfg.SessionID, params.Cols, params.Rows); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodSignal:
		var params SignalParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid signal params")
			return
		}
		sig := parseSignal(params.Signal)
		if err := c.runtime.manager.Kill(c.runtime.cfg.SessionID, sig); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodRemove:
		// Respond before killing so the RPC doesn't block on process exit.
		// Kill can wait up to defaultKillTimeout (10s) for the process to
		// exit, which exceeds the daemon's 5s RPC timeout and causes
		// spurious "i/o timeout" probe failures.  The subsequent
		// requestStop() shuts down the worker, which SIGHUPs the child
		// anyway if it's still alive.
		c.sendResult(req.ID, map[string]any{"ok": true})
		c.shutdown = true
		_ = c.runtime.manager.Kill(c.runtime.cfg.SessionID, syscall.SIGTERM)
		c.runtime.manager.Remove(c.runtime.cfg.SessionID)
		c.runtime.requestStop()
	case MethodWatch:
		if !c.watching {
			c.watching = true
			c.runtime.addWatcher(c)
			c.runtime.logf("worker conn lifecycle watch enabled: conn=%s", c.connID)
		}
		c.sendResult(req.ID, map[string]any{"ok": true})

		c.runtime.stateMu.RLock()
		state := c.runtime.state
		exitCode := c.runtime.exitCode
		exitSignal := c.runtime.exitSignal
		c.runtime.stateMu.RUnlock()
		if state == "" {
			state = "working"
		}
		_ = c.sendEvent(EventEnvelope{
			Type:      "evt",
			Event:     EventStateChanged,
			SessionID: c.runtime.cfg.SessionID,
			State:     &state,
		})
		if exitCode != nil || exitSignal != nil {
			_ = c.sendEvent(EventEnvelope{
				Type:       "evt",
				Event:      EventExit,
				SessionID:  c.runtime.cfg.SessionID,
				ExitCode:   exitCode,
				ExitSignal: exitSignal,
			})
		}
	case MethodHealth:
		info, err := c.runtime.infoResult()
		if err != nil {
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{
			"ok":      true,
			"running": info.Running,
		})
	default:
		c.sendError(req.ID, ErrBadRequest, "unknown method")
	}
}

func (c *connCtx) sendResult(reqID string, result any) bool {
	payload, err := json.Marshal(result)
	if err != nil {
		return c.sendError(reqID, ErrInternal, err.Error())
	}
	ok := c.send(ResponseEnvelope{Type: "res", ID: reqID, OK: true, Result: payload})
	if !ok {
		c.shutdown = true
	}
	return ok
}

func (c *connCtx) sendError(reqID, code, msg string) bool {
	ok := c.send(ResponseEnvelope{
		Type:  "res",
		ID:    reqID,
		OK:    false,
		Error: &RPCError{Code: code, Message: msg},
	})
	if !ok {
		c.shutdown = true
	}
	return ok
}

func (c *connCtx) sendEvent(evt EventEnvelope) bool {
	return c.enqueue(evt, 0)
}

func (c *connCtx) send(v any) bool {
	return c.enqueue(v, connResponseSendTimeout)
}

func (r *Runtime) sessionInfo() (pty.AttachInfo, error) {
	tmpSubID := fmt.Sprintf("info-%d", time.Now().UnixNano())
	info, err := r.manager.Attach(r.cfg.SessionID, tmpSubID, func([]byte, uint32) bool { return true }, nil)
	if err != nil {
		return pty.AttachInfo{}, err
	}
	r.manager.Detach(r.cfg.SessionID, tmpSubID)
	return info, nil
}

func (r *Runtime) infoResult() (InfoResult, error) {
	info, err := r.sessionInfo()
	if err != nil {
		return InfoResult{}, err
	}
	r.stateMu.RLock()
	state := r.state
	exitCode := r.exitCode
	exitSignal := r.exitSignal
	r.stateMu.RUnlock()

	if state == "" {
		state = "working"
	}
	result := InfoResult{
		Running:   info.Running,
		Agent:     r.cfg.Agent,
		CWD:       r.cfg.CWD,
		Cols:      info.Cols,
		Rows:      info.Rows,
		WorkerPID: os.Getpid(),
		ChildPID:  info.PID,
		LastSeq:   info.LastSeq,
		State:     state,
	}
	if exitCode != nil {
		code := *exitCode
		result.ExitCode = &code
	}
	if exitSignal != nil {
		sig := *exitSignal
		result.ExitSignal = &sig
	}
	if info.ExitCode != nil {
		result.ExitCode = info.ExitCode
	}
	if info.ExitSignal != nil {
		result.ExitSignal = info.ExitSignal
	}
	return result, nil
}

func parseSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	case "SIGHUP", "HUP":
		return syscall.SIGHUP
	case "SIGKILL", "KILL":
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func isTemporary(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Temporary()
	}
	return false
}
