package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// headlessOutputLimit bounds the captured stdout buffer. It is large enough to
// hold a Claude `--output-format json` result object or a Codex final message
// (the E2 text-capture path) while still being a hard ceiling against a runaway
// child. classifyHeadlessFailure only does substring checks, so scanning the
// whole buffer on the failure path stays cheap.
const headlessOutputLimit = 1 << 20 // 1 MiB

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

// headlessFailureOutputLimit bounds HeadlessTaskResult.FailureOutput. The tail
// is kept (fatal errors land last on both streams); anything longer is cut.
const headlessFailureOutputLimit = 4 << 10 // 4 KiB per stream

// runHeadlessCommand runs a bounded headless agent process. It returns the
// captured stdout bytes (bounded) so drivers can extract the child's final text
// for the no-schema path. On a non-zero exit it returns a non-nil error with
// Diagnostics and FailureOutput set; the error string itself stays free of
// child output (it travels into keeper/journal surfaces that must not echo
// workspace content) — callers that want the raw cause opt in via
// FailureOutput. The error contract is otherwise unchanged; the workflow
// boundary (driverAgent) is responsible for the error->null adaptation.
func runHeadlessCommand(
	ctx context.Context,
	executable string,
	args []string,
	workDir string,
	provider string,
) (HeadlessTaskResult, []byte, error) {
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
			Diagnostics:   diagnostics,
			FailureOutput: headlessFailureOutput(stdout.String(), stderr.String()),
		}, stdout.Bytes(), fmt.Errorf("%s: %w", diagnostics, err)
	}
	return HeadlessTaskResult{}, stdout.Bytes(), nil
}

// headlessFailureOutput assembles the bounded raw-output tail preserved on a
// failed run: the ground truth behind the Diagnostics bucket. stderr first —
// that is where agent CLIs put fatal errors; stdout may hold a partial result
// envelope.
func headlessFailureOutput(stdout, stderr string) string {
	var parts []string
	if s := strings.TrimSpace(stderr); s != "" {
		parts = append(parts, "stderr: "+tailString(s, headlessFailureOutputLimit))
	}
	if s := strings.TrimSpace(stdout); s != "" {
		parts = append(parts, "stdout: "+tailString(s, headlessFailureOutputLimit))
	}
	return strings.Join(parts, "\n")
}

// tailString keeps the last limit bytes of s, marking the cut.
func tailString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return "…(truncated) " + s[len(s)-limit:]
}

// headlessToolNames returns the single-tool list to thread through a driver's
// argv when toolName is set, or the janitor default pair when it is empty. The
// empty case preserves the janitor's exact {read_context, replace_context} argv.
func headlessToolNames(toolName string) []string {
	if name := strings.TrimSpace(toolName); name != "" {
		return []string{name}
	}
	return []string{"read_context", "replace_context"}
}

// headlessTempDir returns a directory for per-run scratch files. It prefers the
// request's work dir (already a throwaway temp dir for janitor/workflow runs) so
// the scratch file is cleaned up with the run, falling back to the OS temp dir.
func headlessTempDir(workDir string) string {
	if dir := strings.TrimSpace(workDir); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return os.TempDir()
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
	if provider == "claude" {
		env = append(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
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
		return "headless agent MCP tool server failed"
	default:
		return "headless agent process failed"
	}
}
