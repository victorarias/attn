package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestScopeTestEnvironment_SanitizesInheritedOverrides proves that
// ScopeTestEnvironment (as wired into this package's own TestMain) actually
// strips hostile ATTN_DB_PATH / ATTN_SOCKET_PATH / ATTN_CONFIG_PATH values
// inherited from the calling shell, rather than merely setting
// ATTN_DATA_DIR on top of them. This is the scenario figgyster flagged on
// PR #584: a developer's shell can carry ATTN_DB_PATH pointed at the real
// ~/.attn/attn.db (e.g. left over from a scoped profile), and without this
// sanitization DBPath()/SocketPath() would still resolve to that real path
// even though ATTN_DATA_DIR is scoped to a temp dir for the test run.
//
// It cannot assert this in-process: this package's own TestMain has already
// sanitized its environment by the time any in-process test body runs, so
// there is nothing hostile left to observe. Instead it re-execs this test
// binary as a subprocess (same pattern as
// TestDataDir_PanicsWithoutATTNDataDirUnderTest) with the hostile overrides
// seeded in the *child's* env, and asserts the child's TestMain-scoped
// DBPath()/SocketPath() still resolve inside the freshly-scoped
// ATTN_DATA_DIR rather than the hostile inherited values.
//
// The parent process's own environment is never touched.
func TestScopeTestEnvironment_SanitizesInheritedOverrides(t *testing.T) {
	const hostileDB = "/tmp/attn-hostile-inherited-test.db"
	const hostileSocket = "/tmp/attn-hostile-inherited-test.sock"
	const hostileConfig = "/tmp/attn-hostile-inherited-config.json"

	if os.Getenv("ATTN_TEST_HOSTILE_OVERRIDE_HELPER") == "1" {
		// Child process, past its own TestMain, which called
		// ScopeTestEnvironment and should have cleared the hostile values
		// seeded below before this test body ever runs.
		dataDir := os.Getenv("ATTN_DATA_DIR")
		if dataDir == "" {
			panic("helper: ATTN_DATA_DIR unexpectedly empty inside TestMain-scoped subprocess")
		}
		if got := DBPath(); got == hostileDB || !strings.HasPrefix(got, dataDir) {
			panic("helper: DBPath() escaped ATTN_DATA_DIR scope: got " + got + ", want prefix " + dataDir)
		}
		if got := SocketPath(); got == hostileSocket || !strings.HasPrefix(got, dataDir) {
			panic("helper: SocketPath() escaped ATTN_DATA_DIR scope: got " + got + ", want prefix " + dataDir)
		}
		return
	}

	env := os.Environ()
	for _, key := range []string{"ATTN_DB_PATH", "ATTN_SOCKET_PATH", "ATTN_CONFIG_PATH"} {
		env = childEnvWithout(env, key)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestScopeTestEnvironment_SanitizesInheritedOverrides$")
	cmd.Env = append(env,
		"ATTN_TEST_HOSTILE_OVERRIDE_HELPER=1",
		"ATTN_DB_PATH="+hostileDB,
		"ATTN_SOCKET_PATH="+hostileSocket,
		"ATTN_CONFIG_PATH="+hostileConfig,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess with inherited hostile overrides failed: %v\noutput:\n%s", err, out)
	}
}
