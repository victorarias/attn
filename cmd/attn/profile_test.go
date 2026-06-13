package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cleanPlan is the pure safety guard for `attn profile clean`. These tests pin
// the rules that decide WHAT gets destroyed, separate from the destruction
// itself (which is exercised end-to-end by the real-app build/teardown).
func TestCleanPlan(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantName  string
		wantForce bool
		wantErr   bool
	}{
		{name: "named profile", args: []string{"agent7"}, wantName: "agent7"},
		{name: "named profile uppercase normalizes", args: []string{"Agent7"}, wantName: "agent7"},
		{name: "dev is a normal named profile", args: []string{"dev"}, wantName: "dev"},
		{name: "force flag captured", args: []string{"agent7", "--force"}, wantName: "agent7", wantForce: true},
		{name: "force short flag", args: []string{"-f", "agent7"}, wantName: "agent7", wantForce: true},

		// Safety: the default/prod profile is refused without --force.
		{name: "no name", args: nil, wantErr: true},
		{name: "default refused without force", args: []string{"default"}, wantErr: true},
		{name: "default allowed with force", args: []string{"default", "--force"}, wantName: "", wantForce: true},

		// Hygiene.
		{name: "unknown flag", args: []string{"--nope", "agent7"}, wantErr: true},
		{name: "two names", args: []string{"a", "b"}, wantErr: true},
		{name: "invalid name", args: []string{"bad name"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotForce, err := cleanPlan(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("cleanPlan(%q) = (%q,%v,nil), want error", tc.args, gotName, gotForce)
				}
				return
			}
			if err != nil {
				t.Fatalf("cleanPlan(%q) unexpected error: %v", tc.args, err)
			}
			if gotName != tc.wantName || gotForce != tc.wantForce {
				t.Fatalf("cleanPlan(%q) = (%q,%v), want (%q,%v)", tc.args, gotName, gotForce, tc.wantName, tc.wantForce)
			}
		})
	}
}

// A missing pid file means no daemon — never an error during clean.
func TestStopProfileDaemonNoPidFile(t *testing.T) {
	msg := stopProfileDaemon(profileResolved{DataDir: t.TempDir()})
	if !strings.Contains(msg, "no pid file") {
		t.Fatalf("stopProfileDaemon (no pid file) = %q, want a 'no pid file' note", msg)
	}
}

// The safety fix: a pid file that no live daemon holds the lock on is stale, and
// must NOT be signaled even when its pid names a live (recycled) process. We use
// pid 1 (launchd: always alive, never ours, unkillable by us) as the canary — if
// the flock liveness gate regressed, stopProfileDaemon would attempt to signal
// it and report an EPERM failure instead of treating the file as stale.
func TestStopProfileDaemonStalePidNotSignaled(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(1)), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := stopProfileDaemon(profileResolved{DataDir: dir})
	if !strings.Contains(msg, "stale") {
		t.Fatalf("stopProfileDaemon (unlocked pid file naming live pid 1) = %q, want a 'stale' skip (it must not signal pid 1)", msg)
	}
}
