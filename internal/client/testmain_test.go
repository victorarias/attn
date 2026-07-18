package client

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

// TestMain scopes every test in this package to an explicit temp data dir so
// no test can resolve config.DataDir() to the real ~/.attn — see
// docs/plans/2026-07-18-db-loss-mitigation.md. Individual tests that need
// their own isolation (e.g. asserting the default HOME-derived path formula)
// layer a t.Setenv("ATTN_DATA_DIR", ...) on top.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("client: TestMain: MkdirTemp: " + err.Error())
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
