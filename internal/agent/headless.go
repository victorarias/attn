package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const headlessOutputLimit = 8 * 1024

type boundedHeadlessOutput struct {
	bytes.Buffer
}

func (b *boundedHeadlessOutput) Write(p []byte) (int, error) {
	originalLength := len(p)
	if remaining := headlessOutputLimit - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		if _, err := b.Buffer.Write(p); err != nil {
			return 0, err
		}
	}
	return originalLength, nil
}

func runHeadlessCommand(
	ctx context.Context,
	executable string,
	args []string,
	workDir string,
	provider string,
) (HeadlessTaskResult, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	if dir := strings.TrimSpace(workDir); dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = headlessEnvironment(provider)
	var stdout, stderr boundedHeadlessOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		diagnostics := classifyHeadlessFailure(stdout.String() + "\n" + stderr.String())
		return HeadlessTaskResult{
			Diagnostics: diagnostics,
		}, fmt.Errorf("%s: %w", diagnostics, err)
	}
	return HeadlessTaskResult{}, nil
}

func headlessEnvironment(provider string) []string {
	allowedExact := map[string]bool{
		"HOME":                true,
		"PATH":                true,
		"SHELL":               true,
		"USER":                true,
		"LOGNAME":             true,
		"TMPDIR":              true,
		"TMP":                 true,
		"TEMP":                true,
		"LANG":                true,
		"TERM":                true,
		"COLORTERM":           true,
		"SSL_CERT_FILE":       true,
		"SSL_CERT_DIR":        true,
		"NODE_EXTRA_CA_CERTS": true,
		"HTTP_PROXY":          true,
		"HTTPS_PROXY":         true,
		"ALL_PROXY":           true,
		"NO_PROXY":            true,
		"http_proxy":          true,
		"https_proxy":         true,
		"all_proxy":           true,
		"no_proxy":            true,
	}
	allowedPrefixes := []string{"LC_"}
	switch provider {
	case "codex":
		for _, name := range []string{"CODEX_HOME", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY"} {
			allowedExact[name] = true
		}
		allowedPrefixes = append(allowedPrefixes, "OPENAI_", "AZURE_OPENAI_", "AWS_", "GOOGLE_")
	case "claude":
		allowedPrefixes = append(allowedPrefixes, "ANTHROPIC_", "CLAUDE_CODE_USE_", "AWS_", "GOOGLE_", "AZURE_")
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		allowed := allowedExact[name]
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(name, prefix) {
				allowed = true
				break
			}
		}
		if allowed {
			env = append(env, entry)
		}
	}
	return env
}

func classifyHeadlessFailure(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "authentication_failed"),
		strings.Contains(lower, "not logged in"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "api key"):
		return "headless agent authentication failed"
	case strings.Contains(lower, "model") &&
		(strings.Contains(lower, "not found") ||
			strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "unavailable") ||
			strings.Contains(lower, "unsupported")):
		return "headless agent model is invalid or unavailable"
	case strings.Contains(lower, "mcp"),
		strings.Contains(lower, "tool server"),
		strings.Contains(lower, "server failed"):
		return "headless agent janitor tools failed"
	default:
		return "headless agent process failed"
	}
}
