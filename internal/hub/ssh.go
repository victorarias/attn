package hub

import (
	"bytes"
	"context"
	"fmt"
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

func remoteShellCommand(script string) string {
	return "sh -lc " + shellQuote(script)
}

func remoteAttnCommand(args ...string) string {
	bin := fmt.Sprintf(`ATTN_BIN="$HOME/.local/bin/%s"; if [ ! -x "$ATTN_BIN" ]; then ATTN_BIN="$(command -v %s 2>/dev/null || true)"; fi; if [ -z "$ATTN_BIN" ]; then ATTN_BIN=%s; fi; "$ATTN_BIN"`,
		config.BinaryName(),
		config.BinaryName(),
		shellQuote(config.BinaryName()),
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
