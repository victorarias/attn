Package agent defines the Driver interface and optional capability interfaces
for coding agents managed by attn. Adding a new agent (e.g. pi, gemini-cli)
requires implementing Driver and whichever optional interfaces the agent supports.

HookProvider                    — generates hook/settings configs (e.g. Claude Code hooks)
TranscriptFinder                — locates transcript files on disk
TranscriptWatcherBehaviorProvider — custom real-time transcript state policy
ClassifierProvider              — custom classification backend
LaunchPreparer                  — best-effort setup before launch (e.g. resume copy)
SessionRecoveryPolicyProvider   — startup missing-PTY recovery policy
RecoveredStatePolicyProvider    — recovered-state mapping at startup
PTYStateFilterProvider          — live PTY state filtering
ResumePolicyProvider            — resume ID lifecycle policy
TranscriptClassificationExtractor — stop-time transcript extraction policy
ExecutableClassifierProvider    — classifier hook with explicit executable path
HeadlessTaskProvider            — scoped non-interactive agent execution

Agents that don't implement an optional interface get sensible defaults:
- No hooks: no hook-driven state updates
- No transcript finder: classifier skipped on stop
- No classifier: session falls back to idle after stop

Driver is the core interface every agent must implement.
It provides the minimum needed to spawn and manage an agent process.

Name returns the canonical agent identifier (e.g. "claude", "pi", "gemini-cli").
Must match the SessionAgent enum value in the protocol.

DisplayName returns a human-friendly name for UI display (e.g. "Claude Code", "Pi").

ExecutableEnvVar returns the env var name that overrides the executable
(e.g. "ATTN_CLAUDE_EXECUTABLE"). Return "" if no override is supported.

ResolveExecutable returns the executable path, checking env var override,
configured value, and falling back to DefaultExecutable.

BuildCommand builds the exec.Cmd to launch the agent.
The command should NOT be started — the caller handles that.

BuildEnv returns agent-specific environment variables to merge into
the spawned process environment. The caller handles ATTN_INSIDE_APP,
ATTN_SESSION_ID, etc. — this method only returns agent-specific extras
(e.g. executable overrides).

Capabilities returns which optional features this agent supports.
Used by the daemon to decide which code paths to activate.

Capabilities declares which optional features an agent supports.
This allows the daemon to skip code paths that don't apply to an agent
without requiring stub implementations.

HasHooks indicates the agent supports a hook/settings system
(e.g. Claude Code hooks that report state changes via IPC).
If true, the driver should implement HookProvider or ConfigOverrideProvider.

HasTranscript indicates the agent writes transcript files that attn
can discover and parse. If true, implement TranscriptFinder.

HasTranscriptWatcher indicates the daemon should run real-time transcript
watching for state updates. Requires HasTranscript.

HarnessSignals names which harness-owned PTY signals this agent emits (the
OSC 0 title heartbeat, OSC 777 notifications), or HarnessSignalsNone. They
come from the agent itself rather than from reading its rendered TUI, which
is why they are the only PTY state signals left: the scrapers they replaced
broke on every redraw change and, for copilot, were confirmed silent
through a whole live turn before being deleted.

HasInitialPrompt indicates the agent can start an interactive session and
immediately submit a prompt supplied by attn.

HasWorkspaceContext indicates attn can give the agent hidden launch
instructions for using a workspace context checkout.

HasSelfMonitor indicates the agent can optionally watch its own ticket/event
stream via a live Monitor. It selects chief guidance only; daemon nudge
eligibility is shared across runtimes. Only Claude supports this today.

HasModelPin indicates the agent's launch command accepts a per-session
model pin (SpawnOpts.Model). Delegation rejects --model for agents without it.

HasEffortPin indicates the agent's launch command accepts a per-session
reasoning-effort pin (SpawnOpts.Effort). Delegation rejects --effort for
agents without it.

HarnessSignalKind identifies a set of harness-owned PTY signals. The parsing
lives in internal/pty; this names which agent's dialect to read.

HarnessSignalsClaude is Claude Code: a braille/U+2733 title heartbeat plus
OSC 777 notifications.

Env format (per agent, optional):
- ATTN_AGENT_<AGENT>_HOOKS=0|1
- ATTN_AGENT_<AGENT>_TRANSCRIPT=0|1
- ATTN_AGENT_<AGENT>_TRANSCRIPT_WATCHER=0|1
- ATTN_AGENT_<AGENT>_CLASSIFIER=0|1
- ATTN_AGENT_<AGENT>_HARNESS_SIGNALS=0|1
- ATTN_AGENT_<AGENT>_RESUME=0|1
- ATTN_AGENT_<AGENT>_YOLO=0|1
- ATTN_AGENT_<AGENT>_INITIAL_PROMPT=0|1
- ATTN_AGENT_<AGENT>_WORKSPACE_CONTEXT=0|1
- ATTN_AGENT_<AGENT>_SELF_MONITOR=0|1
- ATTN_AGENT_<AGENT>_MODEL_PIN=0|1
- ATTN_AGENT_<AGENT>_EFFORT_PIN=0|1

<AGENT> is uppercased with non-alphanumeric chars replaced by underscores
(e.g. "gemini-cli" -> "GEMINI_CLI").

HARNESS_SIGNALS=0 disables them; =1 keeps the driver's kind: there is no
dialect for =1 to invent for an agent whose driver declares none.

AutoApprove, when true (and not YoloMode), launches the agent in its native
auto-approve mode (Claude --permission-mode auto, Codex
approvals_reviewer=auto_review) so it runs unattended without stalling on
permission gates. Gated by the daemon's auto_approve_enabled setting and
threaded into the worker via ATTN_AUTO_APPROVE. Yolo takes precedence.

Model, when set, pins the interactive agent's model via --model (an alias
like "opus"/"sonnet" or a full model id). Empty means the agent's own
default. Sourced from a delegation's --model flag, or else the daemon's
chief_model_<agent> setting (chief launches only) or default_model_<agent>
setting (every launch), and threaded into the worker via ATTN_MODEL.

Effort, when set, pins the interactive agent's reasoning effort using the
agent's native mechanism (Claude --effort, Codex model_reasoning_effort).
Empty means the agent's own default. Sourced from a delegation's --effort
flag, or else the daemon's chief_effort_<agent> setting (chief launches
only) or default_effort_<agent> setting (every launch), and threaded into
the worker via ATTN_EFFORT. Only meaningful for drivers with HasEffortPin.

AutoCompactWindow, when > 0, caps this launch's effective context window so
auto-compaction triggers at that token threshold instead of the model's full
window (Claude: CLAUDE_CODE_AUTO_COMPACT_WINDOW env var; Codex:
model_auto_compact_token_limit config override). The daemon resolves the
policy (chief_context_window_cap on chief launches,
default_context_window_cap_<agent> on every other launch — see
launchContextWindowCap) and it rides ATTN_AUTO_COMPACT_WINDOW into the
worker; 0 means no cap, and the drivers apply it on every launch shape.

SettingsPath is a generated settings/hooks file path for agents that
support it (e.g. Claude's --settings <path>).

WorkspaceContextPath is this session's local checkout of the workspace's
shared context. It may become stale after launch.

InjectWorkflowGuidance, when true, appends the workflow-trigger guidance to
this session's launch instructions (system prompt / developer instructions).
It is gated by the daemon's workflows_enabled setting and is never set for
workflow subagents, which spawn through the headless path instead.

NotebookRoot, when set, makes this a chief-of-staff launch: the agent
receives Notebook guidance (its profile-wide durable home) instead of the
workspace-context checkout guidance. In practice the launch path sets at
most one of NotebookRoot and WorkspaceContextPath.

TrustWorkingDirectory allows an unattended, daemon-owned launch to pass
the driver's repository trust gate. Interactive launches leave this false.

HookProvider generates hook/settings configurations for agents that support them.
Some agents consume these through a generated settings file.

GenerateHooksConfig returns the content of a settings/hooks config file.
The caller writes it to a temp file and passes --settings to the agent.

HeadlessTaskRequest describes a daemon-owned non-interactive task. The task
must not create an interactive attn session. The agent runs in native-tools
mode: it gets its OWN file tools and a writable working dir (WorkDir). The
daemon writes inputs into WorkDir and reads the agent's output file back;
validation + commit stay daemon-owned.

ReasoningEffort selects the provider's reasoning setting for this one
bounded headless request. Empty preserves the provider default.

--- E2 additive (all optional; the janitor sets none of these) ---

Schema, when non-empty, is the per-call JSON Schema the result sink
advertises as the tool inputSchema. It is NOT consumed by the driver; it
travels to the sink via MCPServerArgs (the caller builds those argv).
Stored on the request for documentation/threading symmetry; drivers ignore it.

ResultPath is the per-call file the sink writes the validated payload to.
Like Schema, the caller bakes it into MCPServerArgs; drivers ignore it.

--- E3 additive (all optional; the janitor sets none of these) ---

Sandbox selects the OS sandbox posture of the headless run. Accepted values:
- ""               => read-only (DEFAULT, byte-identical to the janitor):
Codex `--sandbox read-only` + every feature stripped;
Claude locked to the MCP tool allowlist only.
- "workspace-write" => writable: the agent may edit files and run shell,
confined by the macOS seatbelt to cwd + TMPDIR with
network disabled by default. NO other features are
re-enabled, and no approval bypass is used.
Any UNRECOGNIZED value is treated as read-only (fail closed).

CWD is the process working directory for the run. Empty => fall back to
WorkDir (back-compat). The writable engine path sets this to the run's
working tree so edits land where the workflow expects them; scratch files
(last-message, schema, result) stay rooted at WorkDir to keep the tree clean.

ExtraMCPServers are attached IN ADDITION to the primary MCPServer* triple
(not instead of it), so a workflow session's MCP tools reach the subagent
alongside return_result. The janitor sets none.

AllowedTools optionally overrides the default native tool set
(Claude: Read,Write,Edit,Grep,Glob). Empty => provider default. (Codex
ignores this field; its native tooling comes from the workspace-write
sandbox defaults, not a CLI list.) Used by the native-tools headless path
(the keeper/notebook tasks), which wires no MCP server.

DisableTools, when true, runs the native-tools headless path with NO
tools at all — a pure single-shot completion. This overrides
AllowedTools entirely: an empty AllowedTools alone still falls back to
the provider's native default tool set, so DisableTools is the explicit
way to get a truly tool-less run. Empty/false is byte-for-byte identical
to today's behavior. Used by the ticket reconciliation classifier, which
judges a pre-extracted transcript slice with no need to touch disk.

It makes the tools uncallable, not free: their definitions still ship in
the billed prefix. See claudeHeadlessArgs for why they are not also
stripped.

ExtraWritableRoots optionally widens the set of directories the agent may
WRITE to, beyond the scratch WorkDir. The notebook narration tasks use this
so a headless agent can write the curated journal / raw tier under the
notebook root (which lives outside the scratch tempdir).

Provider behavior:
- Claude: IGNORED. Claude headless runs with --permission-mode dontAsk,
which is NOT filesystem-sandboxed — it can already write anywhere the OS
user can, given absolute paths. No widening is needed or applied.
- Codex: each root is passed as `--add-dir <root>` so the
workspace-write sandbox (which otherwise confines writes to the cwd
WorkDir) also permits writes under these roots. Reads are unrestricted
under workspace-write, so transcript dirs need no widening.

Empty (the keeper's compaction case) leaves both providers' existing
scratch-only behavior unchanged.

--- reconciliation additive (all optional; the keeper/workflow set none) ---
Runaway caps + structured output for judgment-style runs (the ticket
reconciliation classifier). Claude-only: Claude Code is the one agent CLI
with enforceable turn/dollar caps and schema-validated output, which is why
reconciliation always runs `claude -p` regardless of the judged agent (see
docs/plans/2026-07-01-orphaned-ticket-reconciliation.md). Codex ignores all
three, like AllowedTools — `codex exec` has no equivalent flag to translate
them into, so a caller that must be bounded on both drivers has to get its
ceiling from something the caller owns: the run's context deadline, and
DisableTools, which leaves a run with nothing to loop over.

MaxBudgetUSD caps API spend, as a decimal string (claude: --max-budget-usd).
Empty => uncapped.

OutputSchema, when non-empty, is passed inline as a JSON Schema the final
answer must validate against (claude: --json-schema). Unlike Schema (the
MCP result-sink path), this IS consumed by the driver argv; the validated
object comes back in HeadlessTaskResult.StructuredOutput.

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

usesNativeToolsPath reports whether this request runs through the native-tools
headless path (the keeper / notebook narration tasks) rather than the
MCP-config / writable-tree path (the workflow engine). The keeper sets none of
the MCP-server fields and neither CWD nor Sandbox; the workflow engine always
sets at least a writable CWD+Sandbox, and additionally an MCP result sink when
a call needs a schema-validated return. Any one of those markers selects the
MCP-config path.

MCPServerSpec describes one MCP server to attach to a headless run. It mirrors
the primary MCPServer* triple as a value so a list of additional servers can
be threaded through HeadlessTaskRequest.ExtraMCPServers.

Name is the server identifier; tool names are prefixed with it for Claude
(mcp__<Name>__<tool>) and keyed under mcp_servers.<Name> for Codex.

EnabledTools is the explicit tool allowlist exposed by this server. Each is
added to the driver's enabled_tools / --allowedTools (prefixed for Claude).

FailureOutput is the bounded raw tail of the failed child's stderr and
stdout — the ground truth behind the Diagnostics keyword bucket. Set only
on a failed run. It can echo prompt/workspace text, so callers choose
whether their surface may show it (the reconcile failure comment does;
keeper journal surfaces stick to Diagnostics).

Text is the child's captured final assistant text (the no-schema path).
Drivers populate this on a successful run.

StructuredOutput is the schema-validated result object when the request
set OutputSchema (claude: the result envelope's structured_output field).
Empty when no schema was requested or the run ended before producing one
(cap-hit, error) — callers must treat empty as "no verdict".

TotalCostUSD / NumTurns are spend telemetry from the result envelope
(claude --output-format json). Zero when the provider doesn't report them.

HeadlessTaskAvailabilityProvider reports whether the current process
environment can run the driver's isolated headless mode.

FindTranscript returns the path to the transcript file for a session.
Returns "" if not found. For agents without a session ID in the filename
(e.g. Codex, Copilot), cwd and startedAt help narrow the search.

FindTranscriptForResume returns the transcript for a resumed session.
Returns "" if not applicable or not found.

BootstrapBytes returns how many bytes to read from the end of a transcript
when starting to watch mid-session (to catch recent context).

Classify determines whether the agent is waiting for input or done.
Returns "waiting_input", "idle", or "unknown".

LaunchPreparer performs best-effort agent-specific setup before launch
(e.g. Claude resume transcript copy for session handoff).

Register adds a driver to the global registry.
Panics if a driver with the same name is already registered.

Get returns the driver for the given agent name, or nil if not found.

MustGet returns the driver for the given agent name, panicking if not found.

GetTranscriptWatcherBehavior returns a transcript watcher behavior for drivers
that support transcript watching. Drivers may provide a custom behavior via
TranscriptWatcherBehaviorProvider; otherwise a default behavior is used.
