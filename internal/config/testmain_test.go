package config

import (
	"os"
	"testing"
)

// TestMain scopes every test in this package to an explicit temp data dir by
// setting ATTN_DATA_DIR before any test runs. Without this, any test that
// reaches attnDir() (directly or via DataDir/DBPath/SocketPath/PluginDir/...)
// panics under the go-test backstop in config.go — see
// requireExplicitDataDirUnderTest and docs/plans/2026-07-18-db-loss-mitigation.md.
//
// This is the package-default; individual tests that need their own
// isolation (e.g. asserting override precedence) layer a t.Setenv on top.
func TestMain(m *testing.M) {
	// The backstop subprocess test deliberately re-execs this binary with
	// ATTN_DATA_DIR unset to prove config.DataDir() panics; don't paper over
	// that by setting it back here. See datadir_backstop_test.go.
	if os.Getenv("ATTN_TEST_DATADIR_BACKSTOP_HELPER") == "1" {
		os.Exit(m.Run())
	}

	dir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("config: TestMain: MkdirTemp: " + err.Error())
	}
	ScopeTestEnvironment(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
