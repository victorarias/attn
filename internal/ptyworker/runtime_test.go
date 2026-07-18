package ptyworker

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"
)

// TestConnCtx_HandleRequest_SetThemeReachesSession covers the set_theme RPC
// dispatch end to end at the manager level: decoding SetThemeParams and
// calling manager.SetTheme, observed via the session actually answering an
// OSC11 color query with the new background afterward. A handler that never
// decodes params or never calls SetTheme leaves the session answering with
// its spawn-time default, which this test would catch.
func TestConnCtx_HandleRequest_SetThemeReachesSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	r := &Runtime{
		cfg:   Config{SessionID: "theme-rpc-sess"},
		state: "working",
		logf:  func(string, ...interface{}) {},
	}
	r.manager = pty.NewManager(pty.DefaultScrollbackSize, r.logf)
	t.Cleanup(r.manager.Shutdown)

	if err := r.manager.Spawn(pty.SpawnOptions{
		ID:    r.cfg.SessionID,
		Agent: "shell",
		CWD:   t.TempDir(),
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	// Drive the RPC dispatch exactly as a real connection would: build the
	// request envelope, hand it to handleRequest, and drain the response off
	// the same sendQ a real connCtx write loop would consume.
	conn := &connCtx{runtime: r, authed: true, connID: "1", sendQ: make(chan any, 4)}
	params, err := json.Marshal(SetThemeParams{Background: "#ff00ff"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	conn.handleRequest(RequestEnvelope{Type: "req", ID: "req-1", Method: MethodSetTheme, Params: params})

	select {
	case msg := <-conn.sendQ:
		res, ok := msg.(ResponseEnvelope)
		if !ok || !res.OK {
			t.Fatalf("set_theme response = %+v, want ok response", msg)
		}
	default:
		t.Fatal("handleRequest(set_theme) sent no response")
	}

	// Observable effect: attach, feed the session an OSC11 query, and confirm
	// the reply carries the new background rather than the built-in default.
	//
	// This can't just fire the query at the shell's prompt and read it back
	// off the attached output stream: the manager writes its reply into the
	// PTY master, and whether that becomes visible on the master-read side
	// again depends on the shell's own tty echo/raw-mode handling (fish, the
	// default login shell in this environment, disables kernel echo for its
	// own line editing, so the reply lands in fish's stdin and never
	// resurfaces as "output"). Sidestep that entirely: have the shell run a
	// script that reads its own stdin explicitly (bash's `read` builtin
	// receives whatever bytes were written to the master regardless of
	// echo/raw-mode) and prints what it got, wrapped in a marker so a partial
	// or wrong reply can't accidentally match.
	scriptPath := t.TempDir() + "/query.sh"
	script := "#!/bin/bash\n" +
		"printf '\\033]11;?\\007'\n" +
		"IFS= read -r -t 3 -n 25 reply\n" +
		"printf 'THEME_REPLY_START%sTHEME_REPLY_END\\n' \"$reply\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write query script: %v", err)
	}

	outputCh := make(chan []byte, 16)
	_, err = r.manager.Attach(r.cfg.SessionID, "test-observer", func(data []byte, _ uint32) bool {
		outputCh <- append([]byte(nil), data...)
		return true
	}, nil)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	t.Cleanup(func() { r.manager.Detach(r.cfg.SessionID, "test-observer") })

	if err := r.manager.Input(r.cfg.SessionID, []byte("bash "+scriptPath+"\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}

	wantReply := "\x1b]11;rgb:ffff/0000/ffff\x1b\\"
	deadline := time.After(5 * time.Second)
	var seen strings.Builder
	for {
		select {
		case chunk := <-outputCh:
			seen.Write(chunk)
			if idx := strings.Index(seen.String(), "THEME_REPLY_START"); idx != -1 {
				if end := strings.Index(seen.String(), "THEME_REPLY_END"); end != -1 {
					got := seen.String()[idx+len("THEME_REPLY_START") : end]
					if got != wantReply {
						t.Fatalf("OSC11 reply captured via stdin read = %q, want %q", got, wantReply)
					}
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for OSC11 reply marker; got output %q", seen.String())
		}
	}
}

func TestRuntime_ExitedSessionCleansUpAfterTTLWithoutConnections(t *testing.T) {
	origTTL := exitedSessionCleanupTTL
	exitedSessionCleanupTTL = 15 * time.Millisecond
	defer func() { exitedSessionCleanupTTL = origTTL }()

	r := &Runtime{stopCh: make(chan struct{})}
	r.noteSessionExited()

	select {
	case <-r.stopCh:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for runtime stop after exit TTL")
	}
}

func TestRuntime_ExitCleanupWaitsForConnectionsToClose(t *testing.T) {
	origTTL := exitedSessionCleanupTTL
	exitedSessionCleanupTTL = 15 * time.Millisecond
	defer func() { exitedSessionCleanupTTL = origTTL }()

	r := &Runtime{stopCh: make(chan struct{})}
	r.noteConnAuthed()
	r.noteSessionExited()

	select {
	case <-r.stopCh:
		t.Fatal("runtime stopped while authed connection was still active")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	r.noteConnClosed()

	select {
	case <-r.stopCh:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for runtime stop after connection close")
	}
}

func TestRuntime_OrphanWatchStopsIdleUnownedWorker(t *testing.T) {
	r := &Runtime{stopCh: make(chan struct{}), orphanTTL: 15 * time.Millisecond}
	r.noteOutputActivity()
	r.armOrphanWatch()

	select {
	case <-r.stopCh:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for orphan watch to stop idle unowned worker")
	}
}

func TestRuntime_OrphanWatchCanceledByAuthedConn(t *testing.T) {
	r := &Runtime{stopCh: make(chan struct{}), orphanTTL: 15 * time.Millisecond}
	r.noteOutputActivity()
	r.armOrphanWatch()
	r.noteConnAuthed()

	select {
	case <-r.stopCh:
		t.Fatal("orphan watch stopped runtime while a daemon connection was authed")
	case <-time.After(60 * time.Millisecond):
		// expected
	}

	r.noteConnClosed()

	select {
	case <-r.stopCh:
		// expected: watch re-armed when the last authed connection dropped
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for orphan stop after last connection closed")
	}
}

func TestRuntime_OrphanWatchDeferredByOutputActivity(t *testing.T) {
	r := &Runtime{stopCh: make(chan struct{}), orphanTTL: 50 * time.Millisecond}
	r.noteOutputActivity()
	r.armOrphanWatch()

	// Keep the child "busy" past the first deadline; the watch must defer.
	deadline := time.Now().Add(120 * time.Millisecond)
	for time.Now().Before(deadline) {
		r.noteOutputActivity()
		select {
		case <-r.stopCh:
			t.Fatal("orphan watch stopped runtime while output was still flowing")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Once output goes quiet, the worker stops after a full idle TTL.
	select {
	case <-r.stopCh:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for orphan stop after output went quiet")
	}
}

func TestRuntime_OrphanWatchDisabledByZeroTTL(t *testing.T) {
	r := &Runtime{stopCh: make(chan struct{}), orphanTTL: 0}
	r.armOrphanWatch()

	select {
	case <-r.stopCh:
		t.Fatal("orphan watch fired despite zero TTL")
	case <-time.After(60 * time.Millisecond):
		// expected
	}
}

func TestRuntime_OrphanWatchNotArmedAfterExit(t *testing.T) {
	origTTL := exitedSessionCleanupTTL
	exitedSessionCleanupTTL = time.Hour
	defer func() { exitedSessionCleanupTTL = origTTL }()

	r := &Runtime{stopCh: make(chan struct{}), orphanTTL: 15 * time.Millisecond}
	r.noteSessionExited()
	r.armOrphanWatch()

	select {
	case <-r.stopCh:
		t.Fatal("orphan watch fired for an exited session (exit cleanup owns that path)")
	case <-time.After(60 * time.Millisecond):
		// expected
	}
}

func TestConnCtx_NextReadTimeout(t *testing.T) {
	tests := []struct {
		name        string
		authed      bool
		subID       string
		watching    bool
		wantTimeout time.Duration
		wantSet     bool
	}{
		{
			name:        "hello deadline before auth",
			authed:      false,
			wantTimeout: connHelloTimeout,
			wantSet:     true,
		},
		{
			name:        "idle rpc connection uses idle deadline",
			authed:      true,
			wantTimeout: connIdleReadTimeout,
			wantSet:     true,
		},
		{
			name:        "attached stream disables read deadline",
			authed:      true,
			subID:       "sub-1",
			wantTimeout: 0,
			wantSet:     false,
		},
		{
			name:        "watch stream disables read deadline",
			authed:      true,
			watching:    true,
			wantTimeout: 0,
			wantSet:     false,
		},
	}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			ctx := &connCtx{
				authed:   tt.authed,
				subID:    tt.subID,
				watching: tt.watching,
			}
			gotTimeout, gotSet := ctx.nextReadTimeout()
			if gotTimeout != tt.wantTimeout {
				t.Fatalf("nextReadTimeout timeout = %v, want %v", gotTimeout, tt.wantTimeout)
			}
			if gotSet != tt.wantSet {
				t.Fatalf("nextReadTimeout setDeadline = %v, want %v", gotSet, tt.wantSet)
			}
		})
	}
}
