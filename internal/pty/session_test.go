package pty

import (
	"io"
	"os"
	"testing"
	"time"
)

func TestHasInteractiveSubscribers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		subscribers map[string]*sessionSubscriber
		want        bool
	}{
		{
			name:        "none",
			subscribers: map[string]*sessionSubscriber{},
			want:        false,
		},
		{
			name: "debug capture only",
			subscribers: map[string]*sessionSubscriber{
				debugCaptureSubscriberID: {id: debugCaptureSubscriberID},
			},
			want: false,
		},
		{
			name: "info probe only",
			subscribers: map[string]*sessionSubscriber{
				"info-123": {id: "info-123"},
			},
			want: false,
		},
		{
			name: "mixed debug and info only",
			subscribers: map[string]*sessionSubscriber{
				debugCaptureSubscriberID: {id: debugCaptureSubscriberID},
				"info-123":               {id: "info-123"},
			},
			want: false,
		},
		{
			name: "interactive subscriber present",
			subscribers: map[string]*sessionSubscriber{
				debugCaptureSubscriberID: {id: debugCaptureSubscriberID},
				"frontend-pane":          {id: "frontend-pane"},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasInteractiveSubscribers(tc.subscribers); got != tc.want {
				t.Fatalf("hasInteractiveSubscribers() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContainsCPRQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "contains cpr query",
			data: []byte("\x1b[6n"),
			want: true,
		},
		{
			name: "ignores other dsr query",
			data: []byte("\x1b[5n"),
			want: false,
		},
		{
			name: "ignores malformed sequence",
			data: []byte("\x1b[6x"),
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsCPRQuery(tc.data); got != tc.want {
				t.Fatalf("containsCPRQuery() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContainsOSCColorQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		code string
		want bool
	}{
		{
			name: "contains osc 10 query",
			data: []byte("\x1b]10;?\x1b\\"),
			code: "10",
			want: true,
		},
		{
			name: "contains osc 11 query",
			data: []byte("\x1b]11;?\x07"),
			code: "11",
			want: true,
		},
		{
			name: "ignores different osc query",
			data: []byte("\x1b]11;?\x1b\\"),
			code: "10",
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsOSCColorQuery(tc.data, tc.code); got != tc.want {
				t.Fatalf("containsOSCColorQuery() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectTerminalQueries(t *testing.T) {
	t.Parallel()

	queries := detectTerminalQueries([]byte("\x1b[6n...\x1b[c...\x1b]10;?\x1b\\...\x1b]11;?\x07"))
	if !queries.da1 || !queries.cpr || !queries.osc10 || !queries.osc11 {
		t.Fatalf("detectTerminalQueries() = %+v, want all queries detected", queries)
	}
}

func TestClaimTerminalQueryResponsesOnlyOnce(t *testing.T) {
	t.Parallel()

	session := &Session{}
	initial := session.claimTerminalQueryResponses(terminalQueries{
		da1:   true,
		cpr:   true,
		osc10: true,
		osc11: true,
	})
	if !initial.da1 || !initial.cpr || !initial.osc10 || !initial.osc11 {
		t.Fatalf("initial claim = %+v, want all true", initial)
	}

	second := session.claimTerminalQueryResponses(terminalQueries{
		da1:   true,
		cpr:   true,
		osc10: true,
		osc11: true,
	})
	if second.any() {
		t.Fatalf("second claim = %+v, want all false", second)
	}
}

func TestWithinStartupQueryWindow(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := &Session{startedAt: now.Add(-startupQueryFallbackWindow / 2)}
	if !session.withinStartupQueryWindow(now) {
		t.Fatal("withinStartupQueryWindow() = false, want true for recent session")
	}

	expired := &Session{startedAt: now.Add(-startupQueryFallbackWindow - time.Millisecond)}
	if expired.withinStartupQueryWindow(now) {
		t.Fatal("withinStartupQueryWindow() = true, want false after startup window")
	}
}

func TestTerminalQueryFallbackMode(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name              string
		agent             string
		startedAt         time.Time
		noInteractiveSubs bool
		wantEnabled       bool
		wantAllowResend   bool
		wantSource        string
	}{
		{
			name:              "shell interactive during startup",
			agent:             "shell",
			startedAt:         now.Add(-startupQueryFallbackWindow / 2),
			noInteractiveSubs: false,
			wantEnabled:       true,
			wantAllowResend:   true,
			wantSource:        "read_loop_startup_interactive",
		},
		{
			name:              "shell unattached during startup",
			agent:             "shell",
			startedAt:         now.Add(-startupQueryFallbackWindow / 2),
			noInteractiveSubs: true,
			wantEnabled:       true,
			wantAllowResend:   true,
			wantSource:        "read_loop_startup_unattached",
		},
		{
			name:              "non shell unattached",
			agent:             "codex",
			startedAt:         now.Add(-startupQueryFallbackWindow / 2),
			noInteractiveSubs: true,
			wantEnabled:       true,
			wantAllowResend:   false,
			wantSource:        "read_loop_unattached",
		},
		{
			name:              "shell interactive after startup window",
			agent:             "shell",
			startedAt:         now.Add(-startupQueryFallbackWindow - time.Millisecond),
			noInteractiveSubs: false,
			wantEnabled:       false,
			wantAllowResend:   false,
			wantSource:        "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := &Session{
				agent:     tc.agent,
				startedAt: tc.startedAt,
			}
			gotEnabled, gotAllowResend, gotSource := session.terminalQueryFallbackMode(now, tc.noInteractiveSubs)
			if gotEnabled != tc.wantEnabled || gotAllowResend != tc.wantAllowResend || gotSource != tc.wantSource {
				t.Fatalf(
					"terminalQueryFallbackMode() = (%v, %v, %q), want (%v, %v, %q)",
					gotEnabled,
					gotAllowResend,
					gotSource,
					tc.wantEnabled,
					tc.wantAllowResend,
					tc.wantSource,
				)
			}
		})
	}
}

func TestWriteTerminalQueryResponsesAllowResend(t *testing.T) {
	t.Parallel()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	defer reader.Close()

	session := &Session{
		ptmx: writer,
	}

	queries := terminalQueries{
		da1: true,
		cpr: true,
	}
	session.writeTerminalQueryResponses(queries, "startup", true, nil)
	session.writeTerminalQueryResponses(queries, "startup", true, nil)

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() failed: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("io.ReadAll() failed: %v", err)
	}

	wantOnce := "\x1b[?1;2c\x1b[1;1R"
	want := wantOnce + wantOnce
	if string(got) != want {
		t.Fatalf("writeTerminalQueryResponses() wrote %q, want %q", string(got), want)
	}
}
