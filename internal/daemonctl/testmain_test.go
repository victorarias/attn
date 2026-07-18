package daemonctl

import (
	"os"
	"testing"
)

// TestMain scopes every test in this package to an explicit temp data dir so
// no test can resolve config.DataDir() to the real ~/.attn — see
// docs/plans/2026-07-18-db-loss-mitigation.md. Individual tests that need
// their own isolation layer a t.Setenv("ATTN_DATA_DIR", ...) on top.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("daemonctl: TestMain: MkdirTemp: " + err.Error())
	}
	os.Setenv("ATTN_DATA_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
