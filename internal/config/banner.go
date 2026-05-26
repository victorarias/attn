package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SuppressProfileBannerEnv is set only for an internally launched attn
// wrapper process whose stderr is rendered inside an application terminal.
// The wrapper must clear it before launching the interactive child process.
const SuppressProfileBannerEnv = "ATTN_SUPPRESS_PROFILE_BANNER"

// PrintProfileBanner writes a single-line banner to w when a non-default
// ATTN_PROFILE is active. No-op on the default profile so regular users
// never see it. Call from CLI entry points that interact with daemon
// state — NOT from hook commands (they run on every Claude action and
// would flood output).
func PrintProfileBanner(w io.Writer) {
	if os.Getenv(SuppressProfileBannerEnv) == "1" {
		return
	}
	profile := Profile()
	if profile == "" {
		return
	}
	fmt.Fprintf(w, "[attn profile=%s socket=%s port=%s]\n",
		profile,
		CollapseHome(SocketPath()),
		WSPort(),
	)
}

// CollapseHome returns `path` with the user's home directory replaced by
// a leading "~". Used by the CLI banner and "no daemon at X" error
// messages to keep paths readable. Returns `path` unchanged if it
// doesn't live under $HOME or if $HOME can't be resolved.
func CollapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = filepath.Clean(home)
	cleaned := filepath.Clean(path)
	if cleaned == home {
		return "~"
	}
	if strings.HasPrefix(cleaned, home+string(filepath.Separator)) {
		return "~" + cleaned[len(home):]
	}
	return path
}
