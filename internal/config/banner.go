package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PrintProfileBanner writes a single-line banner to w when a non-default
// ATTN_PROFILE is active. No-op on the default profile so regular users
// never see it. Call from CLI entry points that interact with daemon
// state — NOT from hook commands (they run on every Claude action and
// would flood output).
func PrintProfileBanner(w io.Writer) {
	profile := Profile()
	if profile == "" {
		return
	}
	fmt.Fprintf(w, "[attn profile=%s socket=%s port=%s]\n",
		profile,
		collapseHome(SocketPath()),
		WSPort(),
	)
}

func collapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = filepath.Clean(home)
	path = filepath.Clean(path)
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}
