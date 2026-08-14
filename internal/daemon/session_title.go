package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	// maxSessionTitleRunes bounds a generated session title's display length.
	// Distinct from maxDelegationNameRunes (delegate.go): that 16-rune clamp is
	// for delegation names, not auto-generated titles.
	maxSessionTitleRunes = 48
	sessionTitleTimeout  = 90 * time.Second
)

// sessionTitleOutputSchema is the JSON Schema enforced via Claude's
// --json-schema for the auto-title run.
const sessionTitleOutputSchema = `{
	"type": "object",
	"properties": {
		"title": { "type": "string" }
	},
	"required": ["title"],
	"additionalProperties": false
}`

// maybeGenerateSessionTitle is the Stop-hook seam: when a session still carries
// its default label (cwd basename), it generates a short title from the
// conversation using a cheap model of the session's own agent, and writes it as
// the label. Never panics the daemon on bad input; every early return is either
// silent (retryable states) or logged via d.logf.
func (d *Daemon) maybeGenerateSessionTitle(sessionID, transcriptPath string) {
	if !sessionAutoTitleEnabled() || d.sessionTitleExec == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	if !d.sessionMayBeAutoTitled(session) {
		return
	}

	d.sessionTitleMu.Lock()
	if _, attempted := d.sessionTitleAttempted[sessionID]; attempted {
		d.sessionTitleMu.Unlock()
		return
	}
	d.sessionTitleMu.Unlock()

	path := d.resolveTranscriptPathForSession(session, transcriptPath)
	if strings.TrimSpace(path) == "" {
		return
	}
	slice, err := transcript.ExtractConversationSlice(path, transcript.SliceOptions{
		MaxRescopingTurns: 2,
		MaxAgentTurns:     1,
		TurnCharCap:       1500,
		SummaryCharCap:    2000,
	})
	if err != nil || slice.Empty() || slice.Brief == "" {
		// No genuine user content yet (or the transcript wasn't readable/found).
		// Leave the attempted-guard unmarked so a later Stop retries — marking
		// here would permanently skip titling for a session whose first real
		// turn hasn't landed yet.
		return
	}

	// Marked only now that a real LLM attempt is about to happen: one attempt
	// per session per daemon lifetime, success or failure. Re-check membership
	// under the lock — the early check above and this mark are two separate
	// critical sections with transcript extraction between them, so two
	// concurrent calls for the same session (e.g. two close-together Stop
	// events) can both pass the early check; only the first to reach this
	// point may proceed.
	d.sessionTitleMu.Lock()
	if _, attempted := d.sessionTitleAttempted[sessionID]; attempted {
		d.sessionTitleMu.Unlock()
		return // another caller won the race while we were extracting the slice
	}
	if d.sessionTitleAttempted == nil {
		d.sessionTitleAttempted = make(map[string]struct{})
	}
	d.sessionTitleAttempted[sessionID] = struct{}{}
	d.sessionTitleMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), sessionTitleTimeout)
	defer cancel()
	result, err := d.sessionTitleExec(ctx, session, slice)
	if err != nil {
		d.logf("session title %s: %v", sessionID, err)
		return
	}
	title := sanitizeSessionTitle(result)
	if title == "" {
		return
	}

	// Re-fetch and re-check: the label may have been renamed (by the user or a
	// concurrent caller) while the LLM run was in flight.
	session = d.store.Get(sessionID)
	if session == nil || !d.sessionMayBeAutoTitled(session) {
		return
	}
	d.store.UpdateSessionLabel(sessionID, title)
	session.Label = title
	d.publishFact(FactSessionRenamed, sessionID, nil)
}

// sessionMayBeAutoTitled reports whether nobody has named this session yet.
// Two ways a session is already named: a crew member is bound to it — the
// member's name IS its identity — or its label was moved off the cwd basename
// the launch gave it. The member check cannot be folded into the label one: a
// member launches in its own home, so its name and its default label are the
// same string.
func (d *Daemon) sessionMayBeAutoTitled(session *protocol.Session) bool {
	if session.Label != defaultSessionLabel(session.Directory, session.ID) {
		return false
	}
	return d.crewMemberBoundTo(session.ID) == ""
}

// execSessionTitle dispatches the title generation run per the session's own
// agent driver. Wired onto d.sessionTitleExec in New(); test daemons leave it
// nil.
func (d *Daemon) execSessionTitle(ctx context.Context, session *protocol.Session, slice transcript.ConversationSlice) (string, error) {
	agent := string(session.Agent)
	switch agent {
	case "claude", "codex":
		return d.execSessionTitleHeadless(ctx, agent, slice)
	case "copilot":
		return execSessionTitleCopilot(ctx, buildSessionTitlePrompt(slice), sessionTitleModel(agent))
	default:
		return "", fmt.Errorf("unsupported agent for title generation: %s", agent)
	}
}

// execSessionTitleHeadless runs the claude/codex title generation through the
// shared HeadlessTaskProvider seam (see internal/daemon/session_instructions.go
// for the same pattern).
func (d *Daemon) execSessionTitleHeadless(ctx context.Context, agent string, slice transcript.ConversationSlice) (string, error) {
	driver := agentdriver.Get(agent)
	if driver == nil {
		return "", fmt.Errorf("%s driver unavailable", agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return "", fmt.Errorf("%s driver does not support headless tasks", agent)
	}
	executable, err := exec.LookPath(driver.ResolveExecutable(d.store.GetSetting(canonicalExecutableSettingKey(agent))))
	if err != nil {
		return "", fmt.Errorf("resolve %s executable: %w", agent, err)
	}
	workDir, err := os.MkdirTemp("", "attn-session-title-*")
	if err != nil {
		return "", fmt.Errorf("create title scratch dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executable,
		Model:        sessionTitleModel(agent),
		Prompt:       buildSessionTitlePrompt(slice),
		WorkDir:      workDir,
		DisableTools: true,
	}
	switch agent {
	case "claude":
		request.MaxTurns = 2
		request.MaxBudgetUSD = "0.05"
		request.OutputSchema = json.RawMessage(sessionTitleOutputSchema)
	case "codex":
		request.ReasoningEffort = "low"
	}

	result, err := provider.RunHeadlessTask(ctx, request)
	if err != nil {
		return "", fmt.Errorf("%s title run failed: %w", agent, err)
	}
	if agent == "claude" && len(result.StructuredOutput) > 0 {
		var parsed struct {
			Title string `json:"title"`
		}
		if jsonErr := json.Unmarshal(result.StructuredOutput, &parsed); jsonErr == nil {
			return parsed.Title, nil
		}
	}
	return result.Text, nil
}

// execSessionTitleCopilot invokes the copilot CLI directly (Copilot does not
// implement HeadlessTaskProvider), mirroring the exact command shape of
// classifier.ClassifyWithCopilot (internal/classifier/classifier.go). Title
// generation is a distinct concern from stop-time state classification, which
// internal/classifier owns exclusively (see internal/classifier/CLAUDE.md), so
// this does not import or call that package.
func execSessionTitleCopilot(ctx context.Context, prompt, model string) (string, error) {
	executable := strings.TrimSpace(os.Getenv("ATTN_COPILOT_EXECUTABLE"))
	if executable == "" {
		executable = "copilot"
	}
	workDir, err := os.MkdirTemp("", "attn-session-title-*")
	if err != nil {
		return "", fmt.Errorf("create title scratch dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	args := []string{
		"-p", prompt,
		"-s",
		"--model", model,
		"--no-color",
		"--no-custom-instructions",
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("copilot title run failed: %w", err)
	}
	return string(output), nil
}

// buildSessionTitlePrompt builds the title-generation prompt shared by every
// driver.
func buildSessionTitlePrompt(slice transcript.ConversationSlice) string {
	return fmt.Sprintf(`You generate short titles for AI-agent terminal sessions. Based on the conversation excerpt below, produce a concise title (3-7 words, at most 48 characters) that captures what the user is working on. Respond with only the title text - no quotes, no trailing punctuation, no explanation.

<conversation>
%s
</conversation>`, slice.Render())
}

// sessionTitleQuoteClosers maps an opening wrapper rune to the closing rune
// sanitizeSessionTitle strips it against.
var sessionTitleQuoteClosers = map[rune]rune{
	'"':  '"',
	'\'': '\'',
	'`':  '`',
	'“':  '”',
}

// sanitizeSessionTitle turns raw model output into a display-ready title, or
// "" when no valid title can be extracted.
func sanitizeSessionTitle(raw string) string {
	var line string
	for _, candidate := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			line = trimmed
			break
		}
	}
	if line == "" {
		return ""
	}

	if runes := []rune(line); len(runes) >= 2 {
		if want, ok := sessionTitleQuoteClosers[runes[0]]; ok && runes[len(runes)-1] == want {
			line = strings.TrimSpace(string(runes[1 : len(runes)-1]))
		}
	}

	if lower := strings.ToLower(line); strings.HasPrefix(lower, "title:") {
		line = strings.TrimSpace(line[len("title:"):])
	}

	line = strings.Join(strings.Fields(line), " ")
	line = strings.TrimRight(line, ".,;:! \t")

	if runes := []rune(line); len(runes) > maxSessionTitleRunes {
		line = strings.TrimRight(string(runes[:maxSessionTitleRunes]), "-_. \t")
	}

	return line
}

// defaultSessionLabel mirrors the recovery-path default at daemon.go:1294-1297:
// filepath.Base(cwd), falling back to sessionID when the basename is "", ".",
// or the path separator.
func defaultSessionLabel(cwd, sessionID string) string {
	label := filepath.Base(cwd)
	if label == "" || label == "." || label == string(filepath.Separator) {
		return sessionID
	}
	return label
}

// sessionAutoTitleEnabled reports whether Stop-time auto-titling is on.
// ATTN_SESSION_AUTO_TITLE of "0"/"false"/"off" (case-insensitive) disables it;
// anything else, including unset, leaves it enabled.
func sessionAutoTitleEnabled() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("ATTN_SESSION_AUTO_TITLE"))) {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

// sessionTitleModel resolves the per-agent title-generation model, honoring a
// per-agent env override.
func sessionTitleModel(agent string) string {
	switch agent {
	case "claude":
		if v := strings.TrimSpace(os.Getenv("ATTN_CLAUDE_TITLE_MODEL")); v != "" {
			return v
		}
		return "haiku"
	case "codex":
		if v := strings.TrimSpace(os.Getenv("ATTN_CODEX_TITLE_MODEL")); v != "" {
			return v
		}
		return "gpt-5.6-luna"
	case "copilot":
		if v := strings.TrimSpace(os.Getenv("ATTN_COPILOT_TITLE_MODEL")); v != "" {
			return v
		}
		return "claude-haiku-4.5"
	default:
		return ""
	}
}
