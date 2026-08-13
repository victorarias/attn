package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// writeCodexInteractiveRollout writes a codex rollout into codexHome with the given
// native session id, cwd, and session_meta timestamps, mirroring the shape
// codex-cli persists under ~/.codex/sessions.
func writeCodexInteractiveRollout(t *testing.T, codexHome, nativeID, cwd string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "05", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("rollout-%s-%s.jsonl", at.UTC().Format("2006-01-02T15-04-05"), nativeID))
	line := fmt.Sprintf(
		`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","timestamp":"%s","cwd":"%s","source":"cli"}}`+"\n",
		at.UTC().Format(time.RFC3339Nano), nativeID, at.UTC().Format(time.RFC3339Nano), cwd,
	)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes rollout: %v", err)
	}
	return path
}

// Regression for the wrong-rollout bug: two codex processes sharing one cwd
// (here two interactive sessions; live it was attn's own classifier) are
// indistinguishable to the finder's cwd+newest guess, so resolution must
// prefer the codex-native session id the hooks synced for this session.
func TestResolveTranscriptPathForSession_PrefersCodexNativeSessionID(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cwd := "/repo/project"
	now := time.Now()

	own := writeCodexInteractiveRollout(t, codexHome, "native-own", cwd, now.Add(-time.Minute))
	newerNeighbor := writeCodexInteractiveRollout(t, codexHome, "native-neighbor", cwd, now.Add(-5*time.Second))

	d.store.Add(&protocol.Session{
		ID:        "sess",
		Label:     "sess",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
	})
	d.store.SetResumeSessionID("sess", "native-own")

	got := d.resolveTranscriptPathForSession(d.store.Get("sess"), "")
	if got != own {
		t.Fatalf("resolveTranscriptPathForSession() = %q, want own rollout %q (newer neighbor=%q)", got, own, newerNeighbor)
	}
}

// Without an exact native or live-watcher identity, a neighboring rollout in
// the same cwd must never be returned.
func TestResolveTranscriptPathForSession_RejectsCWDGuessWithoutNativeID(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cwd := "/repo/project"
	now := time.Now()

	neighbor := writeCodexInteractiveRollout(t, codexHome, "native-only", cwd, now.Add(-time.Minute))

	d.store.Add(&protocol.Session{
		ID:        "sess",
		Label:     "sess",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
	})

	got := d.resolveTranscriptPathForSession(d.store.Get("sess"), "")
	if got != "" {
		t.Fatalf("resolveTranscriptPathForSession() = %q, want no exact path (neighbor=%q)", got, neighbor)
	}
}
