// Package toolhome resolves the base directory under which external agent
// tools' dotfiles live (~/.claude, ~/.codex, ~/.copilot, ~/.agents).
package toolhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EnvVar overrides the resolved base directory when set. Production leaves
// it unset (real home); tests MUST set it — Dir panics under go test
// otherwise, mirroring config.requireExplicitDataDirUnderTest.
const EnvVar = "ATTN_TOOL_HOME"

// Dir returns EnvVar's value when set (filepath.Clean'd), else
// os.UserHomeDir().
//
// Under testing.Testing() with EnvVar unset it panics: tests must never
// resolve real HOME through tool-dotfile paths (~/.claude, ~/.codex,
// ~/.copilot, ~/.agents). This is the same incident class documented in
// docs/plans/2026-07-18-db-loss-mitigation.md, one step removed: attn's own
// data dir is guarded by config.requireExplicitDataDirUnderTest, but tests
// were still redirecting HOME itself to sandbox other tools' dotfiles.
//
// If you hit this panic: set ATTN_TOOL_HOME to a temp dir, either in a
// package TestMain (os.Setenv, so it applies to the whole package) or in an
// individual test via t.Setenv(toolhome.EnvVar, t.TempDir()) for extra
// per-test isolation. Never redirect HOME to work around this — see
// docs/plans/2026-07-18-db-loss-mitigation.md.
func Dir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvVar)); override != "" {
		return filepath.Clean(override), nil
	}
	if testing.Testing() {
		panic("toolhome: ATTN_TOOL_HOME is not set under go test — tests must never resolve real HOME " +
			"through tool-dotfile paths (~/.claude, ~/.codex, ~/.copilot, ~/.agents). " +
			"Set ATTN_TOOL_HOME to a temp dir (os.Setenv in a package TestMain, or t.Setenv(toolhome.EnvVar, t.TempDir()) per-test). " +
			"Never redirect HOME to work around this: see docs/plans/2026-07-18-db-loss-mitigation.md")
	}
	return os.UserHomeDir()
}
