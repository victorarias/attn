package ptybackend

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMonitorTimeoutGuard_ResetsOnSlowTimeout(t *testing.T) {
	g := &monitorTimeoutGuard{}

	backoff, err := g.onTimeout("sess", 10*time.Millisecond, errors.New("timeout"), func(string, ...interface{}) {})
	if err != nil {
		t.Fatalf("onTimeout() error = %v, want nil", err)
	}
	if backoff != monitorTimeoutBackoff {
		t.Fatalf("backoff = %s, want %s", backoff, monitorTimeoutBackoff)
	}

	backoff, err = g.onTimeout("sess", monitorFastTimeoutAfter+10*time.Millisecond, errors.New("timeout"), func(string, ...interface{}) {})
	if err != nil {
		t.Fatalf("onTimeout() error = %v, want nil", err)
	}
	if backoff != 0 {
		t.Fatalf("backoff = %s, want 0", backoff)
	}

	// Should behave like the first call again.
	backoff, err = g.onTimeout("sess", 10*time.Millisecond, errors.New("timeout"), func(string, ...interface{}) {})
	if err != nil {
		t.Fatalf("onTimeout() error = %v, want nil", err)
	}
	if backoff != monitorTimeoutBackoff {
		t.Fatalf("backoff = %s, want %s", backoff, monitorTimeoutBackoff)
	}
}

func TestMonitorTimeoutGuard_AbortsAfterFastTimeoutLimit(t *testing.T) {
	g := &monitorTimeoutGuard{}

	for i := 0; i < monitorFastTimeoutLimit-1; i++ {
		backoff, err := g.onTimeout("sess", 10*time.Millisecond, errors.New("timeout"), func(string, ...interface{}) {})
		if err != nil {
			t.Fatalf("onTimeout() error at i=%d = %v, want nil", i, err)
		}
		if backoff != monitorTimeoutBackoff {
			t.Fatalf("backoff at i=%d = %s, want %s", i, backoff, monitorTimeoutBackoff)
		}
	}

	_, err := g.onTimeout("sess", 10*time.Millisecond, errors.New("timeout"), func(string, ...interface{}) {})
	if err == nil {
		t.Fatal("onTimeout() error = nil, want non-nil")
	}
	if !errors.Is(err, errLifecycleWatchTimeoutLoop) {
		t.Fatalf("onTimeout() error = %v, want errors.Is(..., errLifecycleWatchTimeoutLoop)=true", err)
	}
	if !strings.Contains(err.Error(), "session=sess") {
		t.Fatalf("onTimeout() error = %q, want it to include session id", err.Error())
	}
}
