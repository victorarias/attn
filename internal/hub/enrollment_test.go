//go:build !windows

package hub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const testHomeDaemonID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// shimSSH replaces ssh on PATH with a script, so the enrollment step can be
// driven through the exit codes a remote `attn enrollment enroll` really emits.
func shimSSH(t *testing.T, script string) {
	t.Helper()
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "ssh")
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("write ssh shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type testLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLog) logf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func (l *testLog) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

func (l *testLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func enrollWithShim(t *testing.T, script, homeDaemonID string) (*testLog, error) {
	t.Helper()
	shimSSH(t, script)
	log := &testLog{}
	b := NewBootstrapper(log.logf)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return log, b.enrollRemote(ctx, "fake-target", "", homeDaemonID)
}

func TestEnrollRemote_RecordsANewEnrollment(t *testing.T) {
	script := "#!/bin/sh\nprintf '{\"status\":\"enrolled\",\"home_daemon_id\":\"" + testHomeDaemonID + "\",\"message\":\"enrolled\"}\\n'\n"
	log, err := enrollWithShim(t, script, testHomeDaemonID)
	if err != nil {
		t.Fatalf("enrollRemote: %v", err)
	}
	if !log.contains("enrolled fake-target as an outpost of " + testHomeDaemonID) {
		t.Fatalf("enrollment was not reported:\n%s", log)
	}
}

func TestEnrollRemote_AlreadyEnrolledIsQuiet(t *testing.T) {
	script := "#!/bin/sh\nprintf '{\"status\":\"unchanged\",\"home_daemon_id\":\"" + testHomeDaemonID + "\",\"message\":\"already an outpost\"}\\n'\n"
	log, err := enrollWithShim(t, script, testHomeDaemonID)
	if err != nil {
		t.Fatalf("enrollRemote: %v", err)
	}
	if log.contains("enrolled fake-target") {
		t.Fatalf("an unchanged enrollment was announced as new:\n%s", log)
	}
}

func TestEnrollRemote_RefusalStopsTheSyncAndCarriesTheReason(t *testing.T) {
	// The banner every remote `attn` prints is on stderr with the refusal; the
	// person whose sync just failed does not need the remote's socket path.
	script := "#!/bin/sh\n" +
		"printf '[attn profile=fence socket=~/.attn-fence/attn.sock port=21320]\\n' >&2\n" +
		"printf 'this daemon (d-x) is already an outpost of d-other\\n' >&2\nexit 3\n"
	_, err := enrollWithShim(t, script, testHomeDaemonID)
	if err == nil {
		t.Fatal("enrollRemote against a foreign home succeeded, want a refusal")
	}
	for _, want := range []string{"fake-target", "already an outpost of d-other"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not carry %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "[attn profile=") {
		t.Fatalf("refusal carries the remote's profile banner: %v", err)
	}
}

func TestEnrollRemote_UnansweredCheckDoesNotBlockTheSync(t *testing.T) {
	// An older remote binary has no `enrollment` command. Enrollment is a record,
	// not a precondition for sessions, so the sync continues and says why.
	script := "#!/bin/sh\nprintf 'attn: unknown command \"enrollment\"\\n' >&2\nexit 2\n"
	log, err := enrollWithShim(t, script, testHomeDaemonID)
	if err != nil {
		t.Fatalf("enrollRemote with an unanswered check = %v, want the sync to continue", err)
	}
	if !log.contains("enrollment of fake-target skipped") {
		t.Fatalf("skipped enrollment was not reported:\n%s", log)
	}
}

func TestEnrollRemote_SkippedWhenTheDialerIsNotAHome(t *testing.T) {
	// The shim refuses everything: reaching ssh at all would be the bug.
	script := "#!/bin/sh\nexit 3\n"
	log, err := enrollWithShim(t, script, "")
	if err != nil {
		t.Fatalf("enrollRemote without a home id = %v, want it skipped", err)
	}
	if !log.contains("this daemon is not a home daemon") {
		t.Fatalf("skipped enrollment was not reported:\n%s", log)
	}
}

func TestRemoteEnrollScript_CarriesTheHomeAndTheProfile(t *testing.T) {
	script := remoteEnrollScript("dev", testHomeDaemonID)
	for _, want := range []string{"attn-dev", "enrollment", "enroll", "--home", testHomeDaemonID, "--json"} {
		if !strings.Contains(script, want) {
			t.Fatalf("remoteEnrollScript() = %q, want it to carry %q", script, want)
		}
	}
}

func TestForeignHomeNotice(t *testing.T) {
	const remoteSelf = "d-cccccccccccccccccccccccccccccccc"
	const otherHome = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	cases := []struct {
		name       string
		home       string
		remoteSelf string
		remoteHome string
		wantNotice bool
	}{
		{name: "enrolled here", home: testHomeDaemonID, remoteSelf: remoteSelf, remoteHome: testHomeDaemonID},
		{name: "its own home", home: testHomeDaemonID, remoteSelf: remoteSelf, remoteHome: remoteSelf},
		{name: "older remote says nothing", home: testHomeDaemonID, remoteSelf: remoteSelf},
		{name: "dialer is not a home", remoteSelf: remoteSelf, remoteHome: otherHome},
		{name: "enrolled elsewhere", home: testHomeDaemonID, remoteSelf: remoteSelf, remoteHome: otherHome, wantNotice: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &protocol.InitialStateMessage{
				DaemonInstanceID: protocol.Ptr(tc.remoteSelf),
				HomeDaemonID:     protocol.Ptr(tc.remoteHome),
			}
			notice := foreignHomeNotice(tc.home, msg)
			if tc.wantNotice && notice == "" {
				t.Fatal("want a notice about a remote enrolled to another home, got none")
			}
			if !tc.wantNotice && notice != "" {
				t.Fatalf("want no notice, got %q", notice)
			}
			if tc.wantNotice && !strings.Contains(notice, "attn enrollment leave") {
				t.Fatalf("notice does not say how to move it: %q", notice)
			}
		})
	}
}
