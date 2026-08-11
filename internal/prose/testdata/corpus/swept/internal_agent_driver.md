Package agent defines the Driver interface and optional capability
interfaces for coding agents managed by attn. A new agent implements Driver
plus whichever optional interfaces it supports; missing ones get defaults
(no hook-driven state, classifier skipped on stop, idle after stop).

Name returns the canonical agent id; must match the protocol's SessionAgent enum.

HarnessSignals names the harness-owned PTY signals this agent emits (OSC
0 title heartbeat, OSC 777), or HarnessSignalsNone. They come from the
agent itself, not from scraping its rendered TUI.

HasSelfMonitor: the agent can watch its own ticket/event stream via a
live Monitor. Selects chief guidance only; nudge eligibility is shared.

HarnessSignalKind identifies a set of harness-owned PTY signals. The parsing
lives in internal/pty; this names which agent's dialect to read.

HarnessSignalsClaude is Claude Code: a braille/U+2733 title heartbeat plus
OSC 777 notifications.

EffectiveCapabilities returns driver capabilities after applying optional
env overrides ATTN_AGENT_<AGENT>_<CAP>=0|1 (suffixes below); <AGENT> is the
uppercased name with non-alphanumerics as underscores ("gemini-cli" -> "GEMINI_CLI").

HARNESS_SIGNALS=0 disables; =1 keeps the driver's kind (there is no
dialect for =1 to invent).

AutoApprove launches the agent in its native auto-approve mode (Claude
--permission-mode auto, Codex approvals_reviewer=auto_review). Yolo wins.

Model, when set, pins the agent's model via --model; empty means default.

Effort, when set, pins reasoning effort via the agent's native mechanism;
only meaningful for drivers with HasEffortPin.

AutoCompactWindow, when > 0, triggers auto-compaction at that token
threshold (Claude: CLAUDE_CODE_AUTO_COMPACT_WINDOW; Codex:
model_auto_compact_token_limit); applied on every launch shape.

WorkspaceContextPath is this session's local checkout of the workspace's
shared context; may become stale after launch.

InjectWorkflowGuidance appends the workflow-trigger guidance to the
launch instructions. Never set for workflow subagents.

NotebookRoot, when set, makes this a chief-of-staff launch (Notebook
guidance); at most one of NotebookRoot and WorkspaceContextPath is set.

TrustWorkingDirectory lets an unattended daemon-owned launch pass the
driver's repository trust gate; interactive launches leave it false.

GenerateHooksConfig returns settings/hooks config file content; the
caller writes it to a temp file and passes --settings to the agent.

HeadlessTaskRequest describes a daemon-owned non-interactive task; it must
not create an interactive attn session. Validation + commit stay daemon-owned.

Schema is the per-call JSON Schema the result sink advertises as the tool
inputSchema. NOT consumed by the driver; it travels via MCPServerArgs.

ResultPath is the per-call file the sink writes the validated payload to.
Like Schema, baked into MCPServerArgs by the caller; drivers ignore it.

Sandbox selects the OS sandbox posture: "" => read-only; "workspace-write"
=> edits + shell, seatbelt-confined to cwd + TMPDIR, network off, no
approval bypass. Any unrecognized value is read-only (fail closed).

CWD is the process working directory; empty falls back to WorkDir.
Scratch files stay rooted at WorkDir to keep the tree clean.

ExtraMCPServers are attached IN ADDITION to the primary MCPServer*
triple, not instead of it.

AllowedTools overrides the default native tool set; empty => provider
default. Codex ignores it.

DisableTools runs with NO tools at all — the explicit tool-less switch,
since empty AllowedTools still falls back to the provider default set.
Uncallable but not free: the definitions still ship in the billed
prefix (see claudeHeadlessArgs).

ExtraWritableRoots widens WRITE access beyond the scratch WorkDir.
Claude: IGNORED (dontAsk is not fs-sandboxed, writes anywhere already).
Codex: each root becomes `--add-dir <root>`.

MaxTurns/MaxBudgetUSD/OutputSchema are Claude-only runaway caps +
structured output; Codex ignores all three. See
docs/plans/2026-07-01-orphaned-ticket-reconciliation.md.

OutputSchema is an inline JSON Schema the final answer must validate
against (claude: --json-schema); returns HeadlessTaskResult.StructuredOutput.

SystemPrompt REPLACES the agent CLI's own system prompt (claude:
--system-prompt). Empty => the CLI's default, which is what every caller
wanted before this field existed.

This is a cost lever, not a behavior lever. A coding CLI's default system
prompt is written for interactive coding and is thousands of tokens the
run pays for on every invocation; a single-shot judgment or one-line
completion needs none of it. Measured on claude-haiku-4-5, tool-less, with
a --json-schema answer: the billed prefix drops from ~49.8K tokens to
~37.0K. Receipt: docs/plans/2026-08-07-session-activity.md.

Both drivers honour it, by different means: Claude gets --system-prompt,
Codex has no system/developer-prompt flag at all, so the driver folds this
text in front of Prompt (see codexPrompt). A caller therefore writes one
split prompt and gets the same evidence boundary on either agent.

usesNativeToolsPath reports whether this request runs the native-tools
headless path (keeper/notebook tasks) rather than the MCP-config path; any
MCP-server, CWD, or Sandbox marker selects the latter.

MCPServerSpec describes one MCP server to attach to a headless run
(HeadlessTaskRequest.ExtraMCPServers).

Name is the server identifier; tool names are prefixed with it for Claude
(mcp__<Name>__<tool>) and keyed under mcp_servers.<Name> for Codex.

EnabledTools is the explicit tool allowlist added to the driver's
enabled_tools / --allowedTools (prefixed for Claude).

FailureOutput is the bounded raw tail of a failed child's stderr/stdout.
It can echo prompt/workspace text, so callers choose whether their
surface may show it.

Text is the child's final assistant text (no-schema path), set on success.

StructuredOutput is the schema-validated result object when OutputSchema
was set; empty means "no verdict" (no schema, cap-hit, or error).

HeadlessTaskAvailabilityProvider reports whether the current process
environment can run the driver's isolated headless mode.

FindTranscript returns the session's transcript path, "" if not found;
cwd/startedAt narrow the search for agents without session-ID filenames.

FindTranscriptForResume returns the transcript for a resumed session,
"" if not applicable or not found.

BootstrapBytes is how many bytes to read from a transcript's end when
starting to watch mid-session.

LaunchPreparer performs best-effort agent-specific setup before launch
(e.g. Claude resume transcript copy).

Register adds a driver to the global registry; panics on a duplicate name.

Get returns the driver for the given agent name, or nil if not found.

MustGet returns the driver for the given agent name, panicking if not found.

GetTranscriptWatcherBehavior returns a transcript watcher behavior — custom
via TranscriptWatcherBehaviorProvider, else the default.
