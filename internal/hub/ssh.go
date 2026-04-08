package hub

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

func sshBaseArgs(target string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		target,
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func remoteShellEnvScript() string {
	assignments := make([]string, 0, 4)
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_ATTN_BIN")); value != "" {
		assignments = append(assignments, "export ATTN_REMOTE_ATTN_BIN="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_SOCKET_PATH")); value != "" {
		assignments = append(assignments, "export ATTN_SOCKET_PATH="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_WS_PORT")); value != "" {
		assignments = append(assignments, "export ATTN_WS_PORT="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REMOTE_DB_PATH")); value != "" {
		assignments = append(assignments, "export ATTN_DB_PATH="+shellQuote(value))
	}
	if value := strings.TrimSpace(os.Getenv("ATTN_REVIEW_LOOP_SCRIPT_B64")); value != "" {
		assignments = append(assignments, "export ATTN_REVIEW_LOOP_SCRIPT_B64="+shellQuote(value))
	}
	if len(assignments) == 0 {
		return ""
	}
	return strings.Join(assignments, "; ") + "; "
}

func remoteShellCommand(script string) string {
	if envScript := remoteShellEnvScript(); envScript != "" {
		script = envScript + script
	}
	return "sh -lc " + shellQuote(script)
}

func remoteAttnCommand(args ...string) string {
	bin := fmt.Sprintf(`ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"; if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then ATTN_BIN="$(command -v %s 2>/dev/null || true)"; fi; if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then printf 'missing attn binary\n' >&2; exit 127; fi; "$ATTN_BIN"`,
		config.BinaryName(),
		config.BinaryName(),
	)
	for _, arg := range args {
		bin += " " + shellQuote(arg)
	}
	return bin
}

func runSSH(ctx context.Context, target, script string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ssh", append(sshBaseArgs(target), remoteShellCommand(script))...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(string(out)), nil
}
