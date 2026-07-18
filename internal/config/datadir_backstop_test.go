package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDataDir_PanicsWithoutATTNDataDirUnderTest proves the go-test backstop
// actually fires. It cannot assert this in-process: this package's own
// TestMain always sets ATTN_DATA_DIR, and unsetting it here would itself be
// exactly the kind of "test resolves the real data dir" mistake the backstop
// exists to catch. Instead it re-execs this same test binary as a
// subprocess (the standard os/exec crash-test pattern) with ATTN_DATA_DIR
// explicitly unset in the *child's* env, and asserts the child panics.
//
// The parent process's own ATTN_DATA_DIR is never touched.
func TestDataDir_PanicsWithoutATTNDataDirUnderTest(t *testing.T) {
	if os.Getenv("ATTN_TEST_DATADIR_BACKSTOP_HELPER") == "1" {
		// Child process: deliberately call DataDir() without ATTN_DATA_DIR
		// set, to prove it panics rather than silently resolving a real path.
		_ = DataDir()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestDataDir_PanicsWithoutATTNDataDirUnderTest$")
	cmd.Env = childEnvWithout(os.Environ(), "ATTN_DATA_DIR")
	cmd.Env = append(cmd.Env, "ATTN_TEST_DATADIR_BACKSTOP_HELPER=1")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("subprocess DataDir() call without ATTN_DATA_DIR did not fail; want a panic. output:\n%s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.Success() {
		t.Fatalf("subprocess exited with unexpected error %v (want a crash from panic); output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "ATTN_DATA_DIR is not set under go test") {
		t.Fatalf("subprocess output missing backstop panic message; output:\n%s", out)
	}
}

// childEnvWithout returns env with every entry for key removed.
func childEnvWithout(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
