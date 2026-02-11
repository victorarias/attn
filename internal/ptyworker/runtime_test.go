package ptyworker

import (
	"testing"
	"time"
)

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
