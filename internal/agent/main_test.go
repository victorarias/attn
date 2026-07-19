package agent

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

// TestMain scopes every test in this package to a throwaway ATTN_TOOL_HOME by
// default, so no test here can resolve toolhome.Dir() (~/.claude, ~/.codex,
// ~/.copilot, ~/.agents skill installs and transcript lookups) to the real
// home directory — see docs/plans/2026-07-18-db-loss-mitigation.md. Tests
// that need specific fixtures under a fake home override this with their own
// t.Setenv(toolhome.EnvVar, ...).
func TestMain(m *testing.M) {
	toolHomeDir, err := os.MkdirTemp("", "attn-agent-test-toolhome-*")
	if err != nil {
		panic("agent: TestMain: MkdirTemp: " + err.Error())
	}
	_ = os.Setenv(toolhome.EnvVar, toolHomeDir)

	code := m.Run()
	os.RemoveAll(toolHomeDir)
	os.Exit(code)
}
