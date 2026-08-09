package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// A delegated conversation agent reports by shelling out to `attn`, so the host
// it runs in owes it the same identity a PTY agent gets. This was observed
// missing: the agent's tools saw an empty ATTN_SESSION_ID and resolved `attn`
// to whichever install happened to be on the login shell's PATH.

// envDumpingHostCommand is a host that records its own environment and then
// stays alive on stdin, so the spawn completes against a real process and the
// environment asserted on is the one the kernel actually gave it.
func envDumpingHostCommand(t *testing.T) (argv []string, dumpPath string) {
	t.Helper()
	dir := t.TempDir()
	dumpPath = filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "dump-env-host.sh")
	body := "#!/bin/sh\nenv > " + dumpPath + "\nwhile IFS= read -r line; do :; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write env-dumping host: %v", err)
	}
	return []string{script}, dumpPath
}

func readHostEnv(t *testing.T, dumpPath string) map[string]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(dumpPath)
		if err == nil && len(data) > 0 {
			env := map[string]string{}
			for _, line := range strings.Split(string(data), "\n") {
				name, value, ok := strings.Cut(line, "=")
				if ok {
					env[name] = value
				}
			}
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("the host never wrote its environment to %s", dumpPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConversationHostCarriesTheSessionIdentity(t *testing.T) {
	// The attn that owns this daemon, standing in for an installed profile's
	// bundled binary. ActiveAttnExecutable treats the wrapper as authoritative
	// precisely because it names the running app.
	activeAttnDir := t.TempDir()
	activeAttn := filepath.Join(activeAttnDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write the active attn: %v", err)
	}
	t.Setenv("ATTN_WRAPPER_PATH", activeAttn)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, _, cwd := setupDelegationSource(t, d, backend)
	pipe, done := startPluginPipe(t, d, "pi-fixture-plugin", nil)
	defer func() {
		_ = pipe.Close()
		<-done
	}()
	registerTestPluginDriver(t, pipe, "pi-fixture", map[string]bool{
		pluginDriverConversationCapability: true,
		"initial_prompt":                   true,
	})
	argv, dumpPath := envDumpingHostCommand(t)
	serveOneDriverSpawn(t, pipe, argv)

	const sessionID = "conv-identity"
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       "pi-fixture",
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
	t.Cleanup(func() { _ = d.ensureHostSessions().Kill(sessionID) })

	env := readHostEnv(t, dumpPath)
	for name, want := range map[string]string{
		"ATTN_SESSION_ID":     sessionID,
		"ATTN_AGENT":          "pi-fixture",
		"ATTN_DAEMON_MANAGED": "1",
		"ATTN_INSIDE_APP":     "1",
	} {
		if env[name] != want {
			t.Errorf("host env %s = %q, want %q — the agent's tools cannot report as this session", name, env[name], want)
		}
	}

	// And the `attn` those tools find must be the one that spawned them. The
	// login shell's PATH names whichever install the user happens to have on it,
	// which for a session on a non-production profile is the wrong world
	// entirely — a delegated agent would report into production.
	entries := filepath.SplitList(env["PATH"])
	if len(entries) == 0 || entries[0] != activeAttnDir {
		t.Errorf("host PATH = %q, want the active attn's directory %q first", env["PATH"], activeAttnDir)
	}
}
