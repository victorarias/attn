package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/victorarias/attn/internal/launchenv"
)

// DefaultContextWindowCap is the auto-compaction token threshold attn applies to
// capped Claude/Codex launches — the chief-of-staff session and headless runs —
// when the operator has not configured a value. It is applied as
// CLAUDE_CODE_AUTO_COMPACT_WINDOW (Claude) / model_auto_compact_token_limit
// (Codex) so compaction fires here instead of at the model's full window. See the
// chief_context_window_cap and headless_context_window_cap settings.
const DefaultContextWindowCap = 128000

// headlessContextWindowCap is the process-global auto-compaction token threshold
// applied to every headless run. Headless runs execute in the daemon process and
// all funnel through this package's spawn seam, so one process-global value (set
// by the daemon from the headless_context_window_cap setting) governs them
// uniformly — there is no per-run override and nothing threads through the
// request builders, which keeps this refactor-proof against the background-task
// changes moving those call sites. 0 means uncapped; the daemon resolves the
// default before any headless run starts.
var headlessContextWindowCap atomic.Int64

// SetHeadlessContextWindowCap sets the token threshold applied to headless runs
// (CLAUDE_CODE_AUTO_COMPACT_WINDOW for Claude, model_auto_compact_token_limit for
// Codex). A value <= 0 clears the cap. The daemon calls this at startup and
// whenever the setting changes.
func SetHeadlessContextWindowCap(tokens int) {
	if tokens < 0 {
		tokens = 0
	}
	headlessContextWindowCap.Store(int64(tokens))
}

// HeadlessContextWindowCap returns the current process-global headless cap in
// tokens, or 0 when uncapped. Both the Claude env seam and the Codex arg builders
// read it; it is exported so callers can observe the value they set.
func HeadlessContextWindowCap() int {
	return int(headlessContextWindowCap.Load())
}

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
		// Cap the effective context window so auto-compaction fires at the
		// configured threshold instead of the model's full window. Injected here
		// (not inherited) because the allowlist above drops CLAUDE_CODE_* and the
		// daemon deliberately scrubs this var from its own environment.
		if window := HeadlessContextWindowCap(); window > 0 {
			env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+strconv.Itoa(window))
		}
	}
	return launchenv.WithActiveAttnFirst(env, launchenv.ActiveAttnExecutable())
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
