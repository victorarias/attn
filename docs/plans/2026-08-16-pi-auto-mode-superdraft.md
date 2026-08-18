# Superdraft: pi auto mode — the safety envelope's autonomy dial

> Rock 4 of [the pi vision](../vision/pi-attn-plugins.md). Grounding:
> pi 0.84.2 source (`tool_call` interception, `ctx.modelRegistry.complete`,
> SDK-supplied `ExtensionUIContext`), Claude Code's documented auto-mode
> behavior, and the existing suite seam in `plugins/attn-pi`.

## The experience (what it feels like)

- pi runs with real autonomy: the agent never stops mid-run to ask "may I?".
  No click-to-approve ceremony — that's the vision's stated non-goal.
- The safety envelope is invisible when work is normal: reading anything,
  writing inside the working directory (the cwd of the pi process), running
  everyday commands — zero friction, zero classifier calls, zero latency.
- Outside the envelope, a background classifier (cheap model, pi's own auth)
  judges the call against the conversation's intent. Allowed → runs.
  Denied → the *agent* gets the reason as the tool result and adapts;
  the run keeps going. The user is informed, never interrupted.
- Approval is conversational, not a dialog. A denied agent says "I'm blocked
  on X, ok to proceed?" in its reply; the user answers "you're approved to
  do X"; the classifier reads the transcript, so that reply *is* the grant —
  verified: this is exactly Claude's documented mechanism ("explicit user
  intent" is a classifier precedence tier that clears soft denies but never
  hard denies; no rule is created). Our denial tool-result says so
  explicitly, so the model knows asking works.
- Conversational grants die with the transcript (compaction eats them —
  a documented Claude gotcha). A durable grant is an allow-list entry in
  the config file.
- Denials are visible, not silent: a status widget in TUI pi; a notification
  and session activity in attn. Every denial names what was blocked and why.
- Boundaries stated in conversation bind: "don't push until I review" is
  enforced by the classifier because it reads the transcript.
- Circuit breaker: repeated denials of the same intent mean the classifier
  lacks context, not that the agent is misbehaving. Then — and only then —
  a human question surfaces: a `ctx.ui.confirm` in TUI; in attn, a normal
  waiting-on-you attention signal (yellow, not flashing yellow — no
  `pending_approval` turn). One approval teaches it and auto resumes.
- Toggle: `/auto` command + `--auto` flag + status indicator, pi's shipped
  `plan-mode` example extension (in pi's repo, not ours) as the structural
  donor. The attn launch surface carries it too: a spawn parameter sets
  auto on/off per session, defaulting from config. Proposed default: **on**
  in attn sessions, off in bare pi (bare pi users chose YOLO knowingly).

## Expectations to honor (and one to break)

- Claude auto-mode users expect: read-only always free; irreversible things
  (force push, reset --hard, rm on pre-session files, prod deploys, secrets
  leaving the machine) blocked by default; trusted infrastructure declared
  once in prose config, not per-command rules.
- Deliberately broken expectation: Claude still prompts (ask rules, denial
  retry tab). We don't — deny-with-reason + circuit breaker replaces
  prompting. Simpler, and matches "autonomy over approval".
- Two static lists, no rule *engine*: an **allow list** (narrow, e.g.
  `npm test`, `go build ./...`) that skips the classifier and takes load
  off it, and a **hard-deny list** for the never-evers. No ask rules, no
  precedence machinery. Broad allow patterns (`*`) are refused at config
  load — a broad allow is what the classifier exists to replace.

## Decision path (per tool call, in order)

- Hard denies → block, no classifier.
- Allow list → run, no classifier.
- Read-only tools (`read`, `grep`, `find`, `ls`) → allow.
- `write`/`edit` with resolved path inside the working directory, outside
  protected paths (`.git`, `.pi`, agent/shell config) → allow.
- `bash`: static read-only-command check (the boring `ls|cat|git status`
  set) → allow; anything else → classifier. Network is never envelope:
  `curl` and friends are always classified, even a plain GET — reads can
  still exfiltrate (secrets in a URL, internal services).
- `write`/`edit` outside the working directory → classifier.
- v1 scope stops there: bash + writes outside cwd are the classified
  surface; custom/unknown tools default-deny with a reason until they get
  an envelope entry (named limit, visible to the agent).
- Classifier sees: recent transcript + pending call + environment prose.
  Never tool results (injection surface).
- Classifier-internal precedence (mirrors Claude's documented order):
  config hard denies are unconditional; explicit user intent naming the
  exact action clears everything softer; then default policy judgment.
- Verdict caching: an **allow** verdict is reused for the same normalized
  intent for the rest of the session. A **deny** verdict is dropped as soon
  as new user input arrives — the user's next message may be the grant
  ("you're approved to do X"), and a cached deny would eat it.

## Two-layer classifier (proposal)

- Layer 1 is the static path above — free and instant.
- Layer 2a: the cheap model returns allow / deny / **uncertain**.
- Layer 2b: uncertain (or self-flagged high-stakes) escalates to a **named**
  stronger model from config — never implicitly the session's main model —
  for the final verdict instead of denying outright. Fewer false denials
  without paying big-model latency on every call.
- Both layers pass `ctx.signal` (Esc aborts) and report usage into session
  totals.

## Implementation shape

- A standalone extension module in `plugins/attn-pi` (own dir beside
  `suite/`), pure pi API — no relay socket dependency, so it works in bare
  pi via `-e` and later `pi install`. The suite composes it for attn
  sessions and adds the attn-side reporting (denial events over the relay).
- TUI pi: dialogs and widgets via `ctx.ui`. nisse: same extension loaded via
  `extensionFactories`; the host supplies an `ExtensionUIContext` mapping
  dialogs onto attn surfaces — one codebase, three surfaces.
- Classifier model comes from pi's model registry: whatever providers the
  user is logged into. Defaults are settings, not pins — we measure and
  test with gpt-5.6-luna and deepseek-v4-flash, but the user's configured
  choice is what runs.
- Config lives in attn's store, not a file: the same daemon-owned storage
  the settings panel will edit later. Scope is global-only in v1 (no
  per-workspace overlay; the environment prose can name repos).
- CLI surface: a dedicated `attn automode` group for policy (show, env,
  allow, deny, denials, model) — split by nature from the separate
  harness/model-catalog surface the settings panel also needs. The daemon
  injects the resolved config into each session the way launch
  instructions already travel. Bare pi falls back to a file under the pi
  config dir.
- **The CLI proposes; only the app promotes.** The CLI is reachable by
  agents, and an agent writing its own policy is the hole auto mode
  exists to close — either the classifier rejects it (it should) or auto
  mode is meaningless. So `attn automode allow ...` never takes effect
  directly: it records a *proposed* rule and tells the caller the user
  must promote it in the attn config UI. Promotion is app-only — the
  trust boundary is human input in the app, which no CLI caller can
  fake. Belt and suspenders: automode storage and its config paths are
  born hard-denied to sessions (Claude's "protect the classifier's own
  control"), so the propose verb is the only door.
- Sequencing: **pi first, panel after** — but promotion needs a home
  before the full panel: a minimal proposed-rules section in settings
  (list + promote/discard) ships with v1; the full harness/model panel
  lands later on the same storage.
- Then the attn **settings panel for harness + model config** — per
  harness, list all discoverable models, allow custom model declarations
  (user's own risk), support custom effort/reasoning levels. Auto mode's
  classifier picker rides on it.
- Per-session state (circuit-breaker counters, cached verdicts, granted
  approvals) via `appendEntry`, rebuilt on `session_start`.
- No-UI contexts (`-p`, json mode) fail closed: deny-with-reason, never
  silently allow — the `permission-gate.ts` precedent.
- Staging: `build-bundled-plugins.sh` bundles it like `suite.js`
  (`@earendil-works/pi-coding-agent` stays external); version bump in the
  three checked places.

## Open questions

- Classifier latency/cost receipt: p50 latency + $/day at real session
  tool-call rates, measured with luna and deepseek-v4-flash before writing
  the default settings.
- How small can the v1 promotion surface be: a proposed-rules list in
  settings, or is a notification with promote/discard enough?
