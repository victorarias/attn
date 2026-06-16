# Notebook Narration & the Durable Task Engine

Status: Proposed — 2026-06-14 (revised) — branch `feat/chifplace`

> **Revision note.** This doc supersedes the earlier per-workspace-file / `## Workspace Log` + `## Story` / `AppendSectionEntryOnce` / `ReplaceSectionByMarker` / Go-transcript-reader / daemon-writes-the-journal design. That machinery is **retired**. The new architecture is: **one curated daily journal** (`journal/<date>.md`) written **only by the keeper and humans**; a separate **raw tier** under `.attn/raw/` that holds machine inputs the keeper consumes; **fully agentic** summarize/narrate tasks that read transcripts and write files with their **own native file tools** (no Go transcript parser, no attn MCP for these); and a **single daemon journal-adjacent write** — the synchronous `context.md` snapshot at workspace removal. The verified codebase citations from the prior revision are reused below; the design they support has changed.

> **Domain language.** This doc uses the canonical vocabulary defined in [docs/glossary.md](../glossary.md) — **the keeper** (one automated persona with two duties: keep `context.md` tidy via `compact_context`, and narrate each workspace's work into the journal via `summarize_session` + `narrate_workspace`), **the chief of staff** (cross-workspace, keeper-aware journaling), and **the journal** (the durable, curated, cross-workspace log). There is no separate "janitor," "narrator," or "summarizer" persona — only the keeper performing its duties. The task-kind strings `compact_context` / `summarize_session` / `narrate_workspace` / `harvest_dream` are runner mechanisms and stay verbatim.

## TL;DR

- **One curated daily journal.** The durable human work-journal is `journal/<date>.md` (recall + perf-review). It is written **only by the keeper and by humans** (and the chief of staff at its own cross-workspace altitude) and must stay **curated** — nothing machine-raw ever lands in it. There are no per-workspace Log/Story files.
- **A separate raw tier feeds the keeper.** Machine inputs (per-session digests, dispatch outcomes, the `context.md` removal snapshot) live under `.attn/raw/`, physically unreachable through the user-facing notebook APIs (`CleanPath` rejects dotdir segments). The keeper consumes the raw tier; it never appears in the curated journal.
- **All narrative work is agentic.** `summarize_session` and `narrate_workspace` are **headless Claude/Codex agents with native file tools** (Read/Write/Grep/Bash), driven by a mechanics+expectations prompt. There is **no daemon-orchestrated single-shot LLM call** and **no Go transcript parser** — the agents read `~/.claude` and `~/.codex` transcripts natively.
- **Cheap-vs-strong model tiers.** `summarize_session` runs a cheap tier and writes a per-session digest to the raw tier. `narrate_workspace` runs a stronger tier and writes the curated journal. Model tier is a per-task-kind knob on the runner.
- **Concurrency = the staleness guard.** Native writes are safe because the Claude/Codex write tools require read-before-write and reject a write if the file changed since it was read (CAS at the tool layer). Idempotency is agent judgment + that guard — **not** daemon markers.
- **The daemon's only journal-adjacent write** is a **synchronous snapshot of `context.md` into the raw tier at every workspace-removal path, before the row DELETE**. `RemoveWorkspace` runs `DELETE FROM workspace_contexts`, which erases `context.md` synchronously; an async agent cannot win that race. This is the deterministic data-safety floor. The daemon never writes the journal.
- **Cross-day output rules.** Across days the keeper can only **append** (it cannot edit a prior day's file); **same-day** it can rewrite its own entry. Predictable greppable markdown headers let the keeper find prior entries; guidance is read-back-until-you-have-enough-context. The removal-day entry is the **full retrospective**.
- **Durable task runner + keeper-tidy (compaction) migration + status surface + dreaming fold-in.** A file-backed task runner (`internal/tasks`) runs the agentic tasks (retry/backoff/coalesce, per-kind model tier). The workspace-context compaction (the keeper's tidy duty, today's `workspace_context_janitor` code) migrates onto it (`compact_context`). A protocol-bumped status surface adds task observability + manual retry. Dreaming folds onto the runner. End state: one runner, zero special-case schedulers.
- **J-series repurposing.** Merged PR-J1/J2 currently write raw dispatch outcomes **into** `journal/<date>.md`. Under this model dispatch capture is **redirected to the raw tier** (`.attn/raw/dispatches/`) and the curated journal stops receiving raw dispatch blocks. The J2 "nudge every agent to journal" injection is reverted (capability kept, not pushed).

## Table of Contents

1. [Motivation & Capture Model](#1-motivation--capture-model)
2. [Curated Journal vs Raw Tier (paths & file model)](#2-curated-journal-vs-raw-tier-paths--file-model)
3. [The Two Agentic Tasks: Spawn Model, Model Tiers, Concurrency](#3-the-two-agentic-tasks-spawn-model-model-tiers-concurrency)
4. [Triggers & the Daemon's Single Write](#4-triggers--the-daemons-single-write)
5. [The Durable Task Engine & Compaction Migration](#5-the-durable-task-engine--compaction-migration)
6. [Status Surface, Dreaming Fold-In, J-series Repurposing](#6-status-surface-dreaming-fold-in-j-series-repurposing)
7. [PR Phasing, Testing Bar & Open Questions](#7-pr-phasing-testing-bar--open-questions)
8. [Prompts (load-bearing)](#8-prompts-load-bearing)

---

## 1. Motivation & Capture Model

### Vision: the Notebook is a durable human work-journal

The attn Notebook is the user's lasting record of what they decided, fought through, built, broke, and learned — kept for **recall and perf-review**, not for an agent's working memory. Its most valuable entries are **singular**: the one architecture call that paid off, the root cause that took a day to find, the dead-end that taught something. Importance is **not recurrence**. The keeper does most of the writing because journaling well is time-consuming and humans underlog, but the audience is always a human returning weeks later.

This inverts the usual agent-recall model and has one hard consequence: **capture must not depend on agent discipline.**

### The empirical driver: agents underlog

This is not a hypothesis. The owner ran three live workspaces under the existing nudge-the-agent approach (the `JournalingDirective` in `internal/hooks/hooks.go:81`, injected at launch for every non-chief agent) and observed **near-zero** agent journaling. Telling agents harder to journal does not work. That kills the "nudge every agent" path (the J2 approach, reverted in [§6](#j-series-repurposing--retirements)) and forces the design toward **automated capture** that produces a good journal even when the agent writes nothing.

> The `attn notebook journal append` CLI stays **available** (the agent→notebook path is still valid when an agent *does* want to write); we just stop *pushing* it. The `JournalingDirective` nudge text itself is **removed** — once the launch injection is dropped it has no remaining caller — so nothing leftover keeps nudging.

### Single durable surface: context.md is an ephemeral editorial overlay

There is exactly **one durable surface — the Notebook (`journal/<date>.md`).** Everything else is working state.

In particular, the per-workspace shared context (`context.md`, managed by `internal/store/workspace_context.go` and the keeper's tidy duty — today the `workspace_context_janitor` code) is the **agent editorial overlay** and is **not durable**:

- it is **erased on workspace removal** — `store.RemoveWorkspace` issues `DELETE FROM workspace_contexts` (`internal/store/workspace.go:47`), and
- it is **compacted/pruned** by the keeper's tidy duty on a size threshold (12 KiB / 10-min debounce / 5-min timeout, the `defaultWorkspaceContextJanitor*` / `defaultKeeperCompact*` constants).

So `context.md` is an ephemeral, curated, high-signal view of what currently matters in a workspace. It is not the journal and was never meant to outlive the work. These write paths feed the one durable Notebook (the writer table mirrors [docs/glossary.md § The journal](../glossary.md)):

| Writer | Mechanism | Status |
|--------|-----------|--------|
| keeper → journal | the keeper's narrate duty (this plan) — per-workspace narrative | **the heart of this plan** |
| chief → journal | cross-workspace, chief-of-staff-altitude journaling | existing; keeper-aware (no per-workspace play-by-play) |
| human → journal | direct edits | always |
| agent → journal | `attn notebook journal append` CLI | still available, no longer pushed (J2 reverted) |

### Capture-before-curation

Capture and curation are separated so **data safety never depends on an LLM**:

- **Capture (deterministic, daemon).** At every workspace-removal boundary, the daemon synchronously snapshots `context.md` into the raw tier before the row delete. Per-session transcripts already survive removal under `~/.claude` / `~/.codex`; dispatch outcomes are captured deterministically into the raw tier. None of this needs an agent.
- **Curation (agentic, best-effort).** The keeper reads the raw tier and writes the curated journal. It may fail repeatedly (LLM down, retries exhausted) without losing any captured material — the raw tier is the safe holding pen.

The keeper is best-effort precisely **because** the raw tier is the durable ledger. A failed narrate leaves all inputs intact for the next attempt.

### Two-layer view + epistemic tiering (the intelligence)

Every capture decision flows from two layers of a workspace's history:

- **Ground truth = the session transcript.** Complete, immutable, reliable — but flat and noisy. Transcripts live under `~/.claude` and `~/.codex` and **survive workspace removal**, so transcript-based work can run async even after the workspace is gone. The pipeline reads transcripts **natively** (the agent's own file tools); attn no longer ships a Go transcript reader for this.
- **Editorial overlay = `context.md`.** Sparse, high-signal, the agent's own statement of what mattered and why — but ephemeral and unverified. It marks **salience** (where to point attention); it is **not** truth.

The agents apply one **epistemic tiering** when reconciling these (the load-bearing intelligence — see the [Prompts](#8-prompts-load-bearing)):

1. **TOOL results = mechanical ground truth.** The actual stdout/exit code of a build/test/lint/git/file op is what really happened. Prefer it above everything.
2. **USER turns = intent & authority.** What the user asked, accepted, corrected, or rejected outranks the agent's own self-assessed "done!".
3. **AGENT prose = a claim, never a fact.** "I fixed it / all tests pass" is an assertion; it becomes fact only when a tool result or user acceptance backs it.

When tiers conflict, authority is **tool result ≈ user acceptance > agent prose**, and the divergence itself is the richest material: **claimed-but-abandoned** dead-ends and **did-but-never-mentioned** wins are exactly what a perf-review reader wants. Because the tiering lives in the **prompts**, not in Go code, there is no structured-reader prerequisite — the agents enumerate user turns and tool_use/tool_result blocks themselves from the raw JSONL.

---

## 2. Curated Journal vs Raw Tier (paths & file model)

Two distinct surfaces. Do not conflate them.

### The curated journal (durable, user-facing)

```
journal/<date>.md       # e.g. journal/2026-06-14.md
```

- **Written only by the keeper and by humans** (and the chief at its own cross-workspace altitude). The daemon never writes it.
- Lives under `DirJournal` (`internal/notebook/layout.go:22`), the existing reserved journal dir, `CleanPath`-visible (`layout.go:50-73`) and externally syncable — it is the user's record.
- Each workspace's entry in a day's file is a self-contained block delimited by a hidden HTML-comment marker so the keeper can find and rewrite exactly its own entry across passes:

```markdown
## <workspace title> — 2026-06-14
<!-- attn:wsnarr:<wsID> -->

<curated narrative prose — decisions, fights, shipped work, dead-ends, unresolved>

source: workspace:<wsID>
```

  The marker namespace `attn:wsnarr:` parallels the shipped dispatch-journal idiom `<!-- attn:dispatch:<id> -->` (`internal/daemon/notebook_dispatch_journal.go:34`) and the `source: <kind>:<id>` footer is lifted directly from `renderDispatchJournalEntry` (`notebook_dispatch_journal.go:193`). One entry per workspace per day; greppable across days.

- **Cross-day discipline (enforced in prompt + code review):** the keeper may only write **today's** file; it may **append** a new dated entry across days and **rewrite its own** same-day entry; it may **never** edit a prior day's file. Prior days are immutable history. The removal-day pass writes the **full retrospective**.

### The raw tier (machine-internal, never user-facing)

```
<notebook.root>/.attn/raw/
  sessions/<sessionID>.md      # per-session digest, written by summarize_session (native Write)
  dispatches/<dispatchID>.md   # dispatch outcome, daemon-written, marker-keyed exactly-once
  context-snapshots/<wsID>.md  # context.md snapshot at removal, daemon-written (the data-safety floor)
```

- **Location: a reserved machine dir under `.attn/`**, sibling to `.attn/tasks/` (the runner) and `.attn/dreams/`. This is deliberately **unreachable** through the user-facing notebook Write/Read/List/Link APIs: `CleanPath` rejects any path whose segment starts with `.` (`layout.go:68`), so the raw tier can never be accidentally grepped or included into the curated journal. The watcher also skips `.attn/` entirely (`internal/notebook/watcher.go:173`, `:275`), so raw writes emit **no** external-edit broadcasts — correct, since the raw tier is daemon/agent-internal.
- **Shape: per-session files, not a running file.** `summarize_session` is naturally keyed by `sessionID` → distinct writers, no concurrent-append contention; the task id `summarize:<sessionID>` maps 1:1 to one file; a crashed/retried summarize harmlessly overwrites its own file. A single running file would serialize all sessions and break the per-session task→file mapping.
- **Format: markdown** (not JSONL) because the consumer is the keeper reading prose, matching the journal idiom. Each file carries a greppable `source: <kind>:<id>` footer so the keeper can ground and a reader can trace.

#### Per-kind raw files

- **Session digests — `.attn/raw/sessions/<sessionID>.md`.** Written by the `summarize_session` agent via native `Write`. Header carries `source: session:<sessionID>` + the transcript path.
- **Dispatch outcomes — `.attn/raw/dispatches/<dispatchID>.md`.** Daemon-written, deterministic, marker-keyed exactly-once. Reuse `renderDispatchJournalEntry`'s rendered block + `neutralizeJournalMarkers` (`notebook_dispatch_journal.go:121`, `:203`); just **redirect the destination** from `journal/<date>.md` to this raw file (one file per dispatch; the file's existence + marker is the ledger). See [§6 dispatch handling](#dispatch-handling-redirect-not-keeper-harvest).
- **Context.md snapshot at removal — `.attn/raw/context-snapshots/<wsID>.md`.** Daemon-written, synchronous, the data-safety floor (see [§4](#the-daemons-single-write-synchronous-contextmd-snapshot-at-removal)). The keeper reads it as the editorial overlay for that workspace's removal-day retrospective.

#### Directory creation & write mechanics

- `EnsureScaffold` creates only `journal/` + `memory/` (`internal/notebook/layout.go:81-94`); it does **not** make `.attn/...`. So the **daemon `MkdirAll`s the raw subdirs at enqueue time** (mirroring how the runner `MkdirAll`s `.attn/tasks/`), so the agent's native `Write` never fails on a missing parent.
- **No new notebook.Store primitive for the raw tier.** Agents write session digests with their **native** file tools (direct I/O, bypassing `notebook.Store` entirely). The daemon's dispatch-redirect and context-snapshot writes use a small atomic temp+rename writer keyed by the marker for exactly-once — **not** `notebook.Store` (the raw tier is outside `CleanPath`, so Store APIs cannot address it anyway). The earlier revision's `AppendSectionEntryOnce` / `ReplaceSectionByMarker` / `WorkspaceEntryPath` / slug helper are **retired** — there is no per-workspace curated file and no section-split journal file to need them.

---

## 3. The Two Agentic Tasks: Spawn Model, Model Tiers, Concurrency

Both narrative tasks are **headless agents with native file tools**, driven by a mechanics+expectations prompt. They are not daemon single-shot LLM calls.

### What is reused vs genuinely new (grounding correction)

attn already runs headless Claude/Codex agents to completion: `HeadlessTaskProvider` / `HeadlessTaskRequest` (`internal/agent/driver.go:269-284`), `runHeadlessCommand` (`internal/agent/headless.go:31-54`, `exec.CommandContext` + `cmd.Run()` blocking until exit/deadline, 8 KiB bounded output, `classifyHeadlessFailure` mapping stderr → diagnostics), and the workspace-context compaction executor (the keeper's tidy duty) as the reference consumer (`internal/daemon/workspace_context_janitor.go:371-439` → `workspace_keeper.go`).

**But `RunHeadlessTask` is NOT reusable unchanged for summarize/narrate.** Both providers **hard-code the compaction executor's MCP tool allow-list**:

- Claude: `tools := toolPrefix + "read_context," + toolPrefix + "replace_context"` then `--tools`/`--allowedTools tools` (`internal/agent/claude.go:143-154`).
- Codex: `enabled_tools=["read_context","replace_context"]` via `-c mcp_servers.<name>.enabled_tools=...` (`internal/agent/codex.go:144`), with `--sandbox read-only` (`codex.go:122`) — which **cannot write** a journal.

summarize/narrate need **native filesystem tools** (Read/Write/Edit/Grep/Bash), not the compaction executor's MCP pair, and narrate must **write**. So generalizing the headless spawn to carry the tool surface is **the one genuinely new build** in this epic and is load-bearing. Concretely:

- **Extend `HeadlessTaskRequest`** (`driver.go:269-277`) with a `NativeTools bool` mode flag and an optional `AllowedTools []string`. The new shape is `{ Executable, Model, Prompt, WorkDir, NativeTools, AllowedTools, MCPServer* (optional) }`. The existing MCP fields stay for the compaction executor.
- **Claude native-tools mode (`claude.go`).** Drop `--strict-mcp-config` / `--mcp-config` / the pinned `--tools`; pass `--allowedTools` with the **native** names (`Read,Write,Edit,Grep,Glob,Bash`); keep `--permission-mode dontAsk` (or bypass) so the agent can read transcripts under `$HOME` and write the raw-tier / journal files without prompts. Keep `--print`, the isolation args, `--model`, `--output-format json`. **The CAS-on-write safety is Claude's native Write/Edit read-before-write guard** — preserved by using native tools, not an attn MCP.
- **Codex native-tools mode (`codex.go`).** The compaction executor's `--sandbox read-only` + disabled shell is wrong for narrate (it must write). Add a native-write variant: `--sandbox workspace-write` scoped to the notebook root + transcript dirs (or the full-access equivalent), re-enable the file tools, keep `--ephemeral` / `--ignore-user-config` / `--strict-config` / `-m model`. Codex's apply-patch/write tooling provides the read-before-write CAS.
- **Sandbox/permission widening (the careful surface).** Native-tools mode must grant **read** access to `~/.claude` and `~/.codex` (transcripts) and **write** access to `<notebook.root>`. The compaction executor's tempdir-only model does not cover this; it is a real new per-provider surface to get right and test.

Everything else is reused verbatim: `runHeadlessCommand` (exit detection, bounded output, diagnostics), `headlessEnvironment`'s provider-scoped auth allow-list (`headless.go`; extend the prefix list only if a model needs a var outside `ANTHROPIC_*`/`CLAUDE_CODE_USE_*`/`OPENAI_*`/`CODEX_*`), the runner's `context.WithTimeout` wrapper (the compaction executor's 5-min pattern), and the executor-reads-the-result-file completion shape.

### Spawn details

- **CWD.** `request.WorkDir → cmd.Dir` (`headless.go:39-40`), unchanged. Use a per-task scratch tempdir (like the compaction executor) and pass **absolute paths** (transcript path, raw-tier file, journal file, journal dir) in the **prompt**, so the agent's native tools operate on absolute paths regardless of cwd.
- **Prompt.** `request.Prompt` positional arg, unchanged (`claude.go:157` / `codex.go:146`). It is the mechanics+expectations brief ([§8](#8-prompts-load-bearing)) with the absolute input/output paths embedded.
- **Completion + success evidence.** `runHeadlessCommand` blocks until exit/deadline; the runner maps non-nil error → `failed` (backoff/retry), nil → `done`. For these native-tools tasks **the file is the ledger**: after `RunHeadlessTask` returns nil, the executor verifies the agent actually wrote the target file (digest exists for summarize; the day's journal contains the `attn:wsnarr:<wsID>` marker for narrate). This mirrors the compaction executor reading its candidate file (`workspace_context_janitor.go:425-433` → `workspace_keeper.go`), adapted to native-tools output.

### Provider selection & model tiers (per-kind, reusing the compaction pattern)

Each task kind has its own settings key holding the **same JSON shape the compaction config already validates**: `{"agent":"claude"|"codex","model":"<model-id>"}`, parsed exactly like `parseKeeperCompactConfig` (formerly `parseWorkspaceContextJanitorConfig`, `internal/daemon/ws_settings.go:47-77`): `agentdriver.Get(agent)` → assert `HeadlessTaskProvider` → `HeadlessTaskAvailability()` → `ResolveExecutable(GetSetting(canonicalExecutableSettingKey(agent)))` → `exec.LookPath`. **Validate at enqueue/config time**, not mid-run, so a misconfigured agent fails fast into `failed`→`dead` with a surfaced `last_error` rather than hanging.

| Setting key | Kind | Tier | Default (unset) |
|---|---|---|---|
| `SettingNotebookSummarizeSession` | `summarize_session` | **cheap** | Claude Haiku (or Sonnet low/no-reasoning); Codex `5.4-mini-medium` / `5.5-low` |
| `SettingNotebookNarrateWorkspace` | `narrate_workspace` | **strong** | Claude strong (Sonnet/Opus normal reasoning); Codex `5.x` medium/high |
| `SettingKeeperCompact` (was `SettingWorkspaceContextJanitor`) | `compact_context` | unchanged | today's compaction default |
| `SettingNotebookHarvestDream` (later) | `harvest_dream` | n/a (deterministic) | — |

- **Cheap for summarize** because it is per-session, high-frequency, and only produces raw input the keeper re-reads in its narrate duty. **Strong for narrate** because it writes the curated journal — the load-bearing product surface where quality is the point.
- **No reasoning/temperature knob beyond the model id.** Reasoning tier is encoded in the chosen model id (e.g. a `-low` variant), avoiding net-new config schema. Add a reasoning field only if a concrete model needs it.
- **Auth.** The existing `headlessEnvironment` allow-list already passes `ANTHROPIC_*` / `CLAUDE_CODE_USE_*` / `OPENAI_*` / `CODEX_*`; summarize/narrate reuse it with no new env plumbing.

### Concurrency: the staleness guard, not daemon markers

Native writes are safe because **the Claude/Codex write tools require read-before-write and reject a write if the file changed on disk since it was read** (CAS at the tool layer). Idempotency is therefore **agent judgment + the staleness guard**, not a daemon marker:

- **Session digests** are per-`sessionID` files → naturally distinct writers; a retried summarize re-reads and replaces its own file.
- **The curated journal** is shared (multiple workspaces' keepers + the human may write the same day's file). The prompt instructs the keeper: on a stale-write rejection, **re-read, re-locate its `attn:wsnarr:<wsID>` marker, preserve every other workspace's entry and all human content verbatim, then write again** — operating only inside its own marker block. The daemon does **not** serialize or mark these writes; the tool-layer CAS + the prompt's reconcile loop are the whole concurrency story.

---

## 4. Triggers & the Daemon's Single Write

Capture is event-driven off two daemon boundaries plus a daily cron. No trigger depends on agent discipline.

### Trigger model (three triggers)

**1. Session-end** — `handleStop` (`internal/daemon/daemon.go:2009`) receives `msg.ID` + `msg.TranscriptPath`. It:
- **resolves `wsID` synchronously from the persisted row** (`d.store.Get(msg.ID).WorkspaceID`), **not** the in-memory registry — the same session exit can drive `dissociateSessionFromWorkspace`, which tears the registry entry down. `StopMessage` carries no `WorkspaceID`, so this lookup must happen in `handleStop` before any async work and before the dissociate race.
- enqueues `summarize:<sessionID>` (**cheap**, carries `msg.TranscriptPath`). This is a **standing** input (underlogging makes the transcript the reliable source), is purely transcript-mechanical at stop, and **survives workspace loss** because its input is under `~/.claude`/`~/.codex`. It does **not** wait for `needs_review_after_long_run` resolution.
- enqueues a **coalesced** `narrate_workspace:<wsID>` (**strong**) only if the session still resolves to a workspace. For a workspace being torn down, the authoritative final narrate is the removal-boundary one (below).

**2. Daily cron** — a single cron enqueuer (the DR6 enqueuer, [§6](#dreaming-fold-in)) enqueues a per-active-workspace `narrate_workspace:<wsID>` each day, **gated on at-least-one session-end-or-context-write that day** so idle workspaces don't burn passes. This covers the **never-removed long-lived workspace**: same-day rewrite, cross-day append. *(The exact "active day" gate is an impl detail — see Open Q.)*

**3. Removal boundary** — after the keeper-tidy cancel, before `RemoveWorkspace`'s DELETE: the daemon does the **synchronous `context.md` snapshot** (below), then enqueues a **zero-debounce** final `narrate_workspace:<wsID>` (the full retrospective), which survives the workspace-row deletion via the independent `.attn/tasks/` record.

The keeper's **tidy duty** stays compaction-only (`compact_context`), orthogonal to its narrate duty except that removal `Cancel`s it (blocks-until-exit, commit-fence) before the snapshot read. The transcript-as-ground-truth reframe is what lets the tidy duty stay pure compaction: anything it later prunes from the overlay was already recovered at session-end by `summarize_session` over the immutable transcript — the same keeper preserving the story before it prunes the overlay.

### The daemon's single write: synchronous context.md snapshot at removal

`context.md` is **erased whenever a workspace row is removed** — `store.RemoveWorkspace` runs `DELETE FROM workspace_contexts` (`internal/store/workspace.go:47`). An async agent cannot win that race, so the snapshot must be **synchronous, before the delete**, at **every** removal path. The three real teardown sites (all `internal/daemon/workspace.go`):

1. **Explicit unregister** — `handleUnregisterWorkspace`: `cancelKeeperCompact(id)` (formerly `cancelWorkspaceContextJanitor`, `workspace.go:399`) → **insert snapshot** → `RemoveWorkspace(id)` (`workspace.go:400`).
2. **Natural last-session departure (the common path)** — `dissociateSessionFromWorkspace`: `cancelKeeperCompact(workspaceID)` (`workspace.go:546`) → **insert snapshot** → `RemoveWorkspace(workspaceID)` (`workspace.go:547`).
3. **Startup reconciliation** — `loadWorkspacesFromStore`: reaps an orphaned workspace with `RemoveWorkspace(ws.ID)` (`workspace.go:432`). No keeper-tidy cancel precedes it; call `cancelKeeperCompact(ws.ID)` for uniformity, then **insert snapshot**, then the reap.

A shared helper runs at all three:

```go
// shared, synchronous, best-effort, swallow-error
func (d *Daemon) snapshotWorkspaceContextOnRemove(id string, title string)
```

It:
1. Reads the canonical overlay via `d.store.GetWorkspaceContext(id)` (content + `Revision`; empty/revision-0 → no-op).
2. Resolves the notebook root; unconfigured → silent no-op.
3. `MkdirAll`s `.attn/raw/context-snapshots/`, runs the verbatim body through `neutralizeJournalMarkers` (daemon-layer; raw-tier content must not be able to forge a journal marker), and writes `<wsID>.md` — where `<wsID>` is the workspace's stable `id` (the same string passed to `RemoveWorkspace`) — with a `source: workspace-context:<id>@<revision>` footer via the small atomic temp+rename writer. The file's existence + that `source:` footer is the exactly-once ledger (a replayed removal path is a harmless overwrite of identical content; this file carries **no** HTML-comment dedup marker — the 1:1 `<wsID>.md` keying makes one unnecessary).
4. Logs and swallows every failure — capture must never block or fail a teardown (same contract as `journalDispatchOutcome`).

> **Snapshot destination — raw tier.** Per the locked design, the snapshot lands in the **raw tier** (`.attn/raw/context-snapshots/<wsID>.md`), where the keeper reads it as the removal-day editorial overlay. *(Open for owner: the prior locked text also discussed a human-visible curated destination; the current decision is raw-first, keeper-curated. See Open Q.)*

**Ordering invariants (all three sites):** snapshot **after** any `cancelKeeperCompact(id)` (the commit-fence guarantees no in-flight keeper-tidy write to context after that point) and **before** `RemoveWorkspace(id)`. Take the title from the `unregister` return snapshot (sites 1–2) or the store row (`ws.Title`, site 3).

After the snapshot, enqueue the **zero-debounce** final `narrate_workspace:<wsID>`. Because the snapshot + the surviving transcripts are durable, a narrate that runs post-removal reconstructs the retrospective with no live workspace state.

---

## 5. The Durable Task Engine & Compaction Migration

### `internal/tasks` — a general, file-backed, durable runner

A new daemon-level package providing a **general** runner the narration pipeline, the migrated compaction (the keeper's tidy duty), and dreaming all lease. Not notebook-coupled. It **borrows the dreaming persistence/lock/recovery idioms verbatim** (`internal/notebook/dreams_state.go`) and adds a queue, attempt/backoff, per-task status, coalescing enqueue, per-kind model tier, and a nailed-down cancellable contract.

**Why file-backed, not SQLite.** No existing task table to reuse, and the **burned-migration gotcha is live**: source `migrations` in `internal/store/sqlite.go` ends at `{48, ...}` but `SELECT MAX(version) FROM schema_migrations` returns **49** on both prod and dev DBs (`~/.attn/attn.db`, `~/.attn-dev/attn.db`), so a `{49, ...}` table is silently skipped. Decision: **one atomic-JSON file per task** under `<notebook.root>/.attn/tasks/<id>.json`; the "queue" is `os.ReadDir` filtered by `state`. (Re-verify `MAX(version)` on real DBs if anyone proposes a table.)

**Persistence & recovery (ported from dreaming).** `MkdirAll <notebook.root>/.attn/tasks/` on init (and the `.attn/raw/*` subdirs); atomic temp+rename per record; orphan-`running`→`queued` reset on start; single worker goroutine (no lock file needed — the worker serializes); done-channel ticker shutdown. **Runner disabled when no notebook root resolves** (the compaction consumer then degrades to an inline in-process fallback so compaction still happens).

**Task record.** `id` (derived from kind+subject, e.g. `narrate_workspace:<wsID>`, `summarize_session:<sessionID>`, `compact_context:<wsID>`, `harvest_dream:<root>`), `kind` (executor selector), `subject`, `state` (`queued|running|failed|done|dead`), `attempts`, `next_attempt_at` (also the coalesce debounce anchor), `last_error`, `created_at`/`updated_at`. No `payload`/`dedupe_marker` — every kind derives what it needs from `subject`, and idempotency lives in the **target file**, not the record.

**State machine.** `queued→running→done`; `running→failed→queued` (auto when `now ≥ next_attempt_at` and `attempts < max`) `→dead` (at `attempts ≥ max ~5`). Backoff: capped exponential, base ~1m, cap ~1h. Manual retry forces `failed|dead→queued` with `next_attempt_at = now`.

**Coalescing.** Subject-derived id → re-enqueue overwrites the same record. Default **pushes `next_attempt_at` forward** by the kind's debounce window; the enqueuer can request **zero-debounce** (`next_attempt_at = now`). This expresses the keeper-tidy debounce (`compact_context:<wsID>`), collapses N session-ends into one `narrate_workspace:<wsID>`, and lets the **removal-boundary final narrate** override any pending debounce so it runs after the synchronous snapshot.

**Cancellable contract (the part the keeper's tidy duty forces).** `Cancel(id)` signals the executor's context and **does not return until the task goroutine has exited** (proven under `-race`). The **commit-fence** is executor-owned: the executor honors cancellation up to a local "committing" latch, then ignores cancellation through its atomic write, then clears the latch. A delete either cancels a not-yet-committing run cleanly or waits for an already-committing run to finish — never tears the store write. This is ported verbatim from `cancelKeeperCompact` (formerly `cancelWorkspaceContextJanitor`, `workspace_context_janitor.go:310-328` → `workspace_keeper.go`, fence at `:318`, `<-done` wait at `:325-327`).

**Idempotency per kind.** Native-tools narrate/summarize: the agent re-reads and the staleness guard + the target-file ledger make a retry safe (a digest overwrites itself; a narrate re-locates its marker block). `compact_context`: safe via the store's optimistic-revision guard, though a crash-mid-compaction re-spends an LLM pass (acceptable — compaction is rare/debounced).

**Executor registration (the seam PR-3 leans on).** The runner exposes a per-kind executor registry, modeled on the existing `keeperCompactExecutor` function-pointer type (formerly `workspaceContextJanitorExecutor`, `workspace_context_janitor.go:41` → `workspace_keeper.go`): roughly `type ExecutorFunc func(ctx context.Context, task *Task) error` and `func (r *Runner) Register(kind string, fn ExecutorFunc) error`. The runner owns the `context.WithTimeout` wrapper and the cancellable/commit-fence contract; the executor body owns the work and sets the committing latch before its durable write. PR-2 proves this seam by registering the migrated keeper-tidy executor (`compact_context`) and exercising it through the runner's enqueue/cancel/state API; PR-3 then registers `summarize_session` and `narrate_workspace` against the same registry.

**Explicit exclusions** (keep it ~1k-line reviewable): no priorities, no DAG, no cron generality in the core (DR6 adds one cron enqueuer), no worker pool, no SQLite, no per-task lock file, no heartbeat beyond `context.WithTimeout`.

### Compaction (tidy-duty) migration (the validation consumer)

The workspace-context compaction (the keeper's tidy duty) moves onto the runner as `compact_context`. It already separates scheduling from execution (`keeperCompactExecutor`, formerly `workspaceContextJanitorExecutor`, is a function-pointer type at `workspace_context_janitor.go:41` → `workspace_keeper.go`, held as a `Daemon` field at `daemon.go:190`), so the migration is: replace the `time.AfterFunc` scheduling + hand-rolled single-flight/cancel guards with runner enqueue + executor registration, **keep `executeKeeperCompact` (formerly `executeWorkspaceContextJanitor`) body verbatim**, keep `validateKeeperCompactCandidate` and `ApplyKeeperCompactResult` untouched. Removal now `Cancel("compact_context:<wsID>")` via the runner.

This proves the runner against the **most concurrency-sensitive surface** (commit-fence cancel, 5-min timeout, atomic backup) before narration leans on it, and closes two real compaction bugs **for free**: **Gap A** (lost debounce across restart — pending compaction now survives) and **Gap B** (no retry on a failed compaction — now auto-requeues with backoff). The bespoke scheduling/guard fields on `Daemon` (`daemon.go:182-194`) are deleted; threshold/debounce/timeout knobs feed the enqueue/executor config.

---

## 6. Status Surface, Dreaming Fold-In, J-series Repurposing

### Status surface (protocol bump)

A unified observability + manual-retry surface, modeled on the existing `dreamStatus` read path and the `notebook_*` WS request/result idiom — but over the **WebSocket** (the tasks panel is a frontend surface).

- **`notebook_task_list` (read, WS request/result).** Cheap `os.ReadDir` of `.attn/tasks/` decoding each record, newest-first; mirrors `dreamStatus()`'s "cheap summary, no side effects". Plus a unix-CLI handler so `attn notebook tasks` works from the shell.
- **`notebook_task_retry` (action — UX option B, the chosen one).** Payload `{ cmd, task_id }`; daemon calls the runner's retry (`failed|dead→queued`, `next_attempt_at = now`), then broadcasts `EventNotebookTasksChanged`. A manual retry merely flips state and is reflected by the broadcast, so AGENTS.md Critical Pattern #2 (no optimistic fire-and-forget for fallible mid-action ops) does not require the heavier request/result Promise here — **lighter command + live refresh**. The retry UX is a lighter command + live panel refresh.
- **`EventNotebookTasksChanged` (live broadcast)**, modeled on `broadcastNotebookChanged`, fired from the runner on every lifecycle transition; the frontend re-fetches `notebook_task_list` on receipt.

**Protocol versioning (mandatory — Critical Pattern #1):** edit `internal/protocol/schema/main.tsp` (add `NotebookTask` / `NotebookTaskListResult` / the WS messages; model `kind`/`state` as **`string`, not TypeSpec `enum`**, to dodge the quicktype identical-enum-merge gotcha that drops a TS export); `make generate-types`; update `internal/protocol/constants.go` by hand (commands/events/decode cases); **bump `ProtocolVersion` `109`→`110`** (`internal/protocol/constants.go:13`); run the `tsc`-check after generate; `make install` (outside the sandbox). The frontend host is `app/src/components/NotebookBrowser.tsx` (a collapsible Tasks section), threaded through the App.tsx four-site two-component pattern; `sendNotebookTaskList`/`sendNotebookTaskRetry` copy the existing `sendNotebook*` hooks.

### Dreaming fold-in

Dreaming folds onto the runner as a `harvest_dream` task kind + a **single cron enqueuer** (which also drives the daily per-workspace narrate from [§4](#trigger-model-three-triggers)). Delete the bespoke scheduler (`startDreamingScheduler` / `dreamSchedulerTick` / `runDreamHarvest` in `internal/daemon/notebook_dreaming_scheduler.go`); keep `harvestDreamCandidates` / `dreamHarvestUnion` (`notebook_dreaming.go:40`, `:53`) as the executor body with their union idempotency. Reuse `DreamRunState` persistence (reframe its schedule fields onto the `harvest_dream:<root>` task record or keep a slimmed struct — verify `fillDreamSchedule`'s consumers first, `notebook_dreaming.go:311-316`). End state: **one runner, zero special-case schedulers.**

> **Harvest source supersession.** `harvestInto` (`notebook_dreaming.go:72-127`) scans three sources: journals (`:78`), workspace-context Decisions/Constraints via `extractContextSignals` (`:103-113`), and closed dispatches (`:115-123`). The narration pipeline supersedes **source 2** (the `context.md` re-read for durable output — otherwise two systems consume `context.md`); delete it + `extractContextSignals` after verification. Sources 1 (journals) and 3 (closed dispatches) are **not** superseded — keep them.

### Dispatch handling: redirect, not keeper-harvest

**Decision: redirect the deterministic dispatch capture to the raw tier; retire the dispatch-to-curated-journal write. Do NOT have the keeper harvest from the chief transcript.**

The recommended leaning (keeper-harvest from the chief transcript) is **infeasible** as stated and is overridden on grounding: dispatch reports are persisted **only** to the SQLite `chief_of_staff_dispatches` table (`UpdateChiefOfStaffDispatchReportEnvelope`, `internal/store/chief_of_staff_dispatches.go:255`) and to the journal (`journalDispatchOutcome`, `chief_of_staff_dispatch.go:277`); they are **never** written to the chief session's transcript. So "harvest dispatch outcomes from the chief transcript" reads a transcript that does not contain them. Retiring the deterministic capture before a transcript-source exists would lose dispatch outcomes during the gap.

What we do instead:
1. **Keep** the deterministic, exactly-once dispatch capture (`renderDispatchJournalEntry` + per-dispatch marker + `neutralizeJournalMarkers` + the report-path / session-gone / restart triple-trigger) — it is reliable and already correct.
2. **Redirect** its destination from `AppendJournalEntryOnce` into `journal/<date>.md` (`notebook_dispatch_journal.go:73`) to `.attn/raw/dispatches/<dispatchID>.md` (one file per dispatch; existence + marker = the exactly-once ledger). This removes machine-raw entries from the curated journal. Mechanically: `journalDispatchOutcome` stops calling `notebook.Store.AppendJournalEntryOnce` and instead writes the **already-rendered, already-neutralized** block (`renderDispatchJournalEntry` + `neutralizeJournalMarkers`, unchanged) to the raw file via the **same atomic temp+rename writer the context snapshot uses** ([§4](#the-daemons-single-write-synchronous-contextmd-snapshot-at-removal)). One writer, one per-dispatch file: the file's existence + its `attn:dispatch:<id>` marker is the ledger, so the prior in-file `AppendJournalEntryOnce` marker-scan is no longer needed for dispatches (a replayed trigger is a harmless identical overwrite). `AppendJournalEntryOnce` itself is **kept, not deleted** — its other callers and the existing `journal/<date>.md` J1/J2 history stay valid; only the dispatch path stops using it. The two deterministic daemon writes — context snapshot and dispatch redirect — share one atomic writer and one `neutralizeJournalMarkers` step so they are built, reviewed, and tested together. They cannot collide: each is keyed to a distinct per-item raw file (`context-snapshots/<wsID>.md` vs `dispatches/<dispatchID>.md`), the snapshot's ledger is file-existence + its `source: workspace-context:<id>@<revision>` footer while the dispatch's is file-existence + its `attn:dispatch:<id>` marker, and both run their bodies through `neutralizeJournalMarkers` so no free-text field in either can forge a marker.
3. The **keeper consumes** dispatch outcomes from the **raw tier files only** (`.attn/raw/dispatches/<dispatchID>.md`, absolute dir passed in the prompt), **not** the live store rows. Rationale: the authoritative final narrate runs **post-removal**, after the workspace row (and any live dispatch query path tied to it) is gone, so narration must depend only on durable on-disk inputs. The deterministic capture (step 1) guarantees a terminal dispatch's outcome is in the raw tier before narration needs it, so `ListChiefOfStaffDispatches` is **not** read by the keeper. The keeper weaves terminal dispatch outcomes into the narrative when they belong to that workspace's sessions; the prompt ([§8B](#b-narrate_workspace-strong-tier)) already lists `RAW_DISPATCHES_DIR` as a read input.

**Migration safety:** existing `journal/<date>.md` files already contain J1/J2 dispatch blocks. Those are durable history and stay (read-only). The redirect changes only where **new** dispatch outcomes land. No backfill.

### J-series repurposing & retirements

- **J1 dispatch-to-journal → raw tier** (above): redirect, keep the deterministic capture, do not delete old journal blocks.
- **J2 nudge revert.** With underlogging proven, revert the broadened `JournalingDirective` injection from the launch/fallback paths — drop the nudge at the **3 sites**: the `+ "\n\n" + hooks.JournalingDirective()` suffix at `internal/agent/claude.go:92`, `internal/hooks/codex.go:77`, and `internal/hooks/hooks.go:67`. Once the injection is gone the `JournalingDirective` function has **no remaining caller**, so **remove it** (the `attn notebook journal append` CLI stays — the agent→journal path is still available, just no longer pushed). The chief still gets the fuller `NotebookGuidance` (`hooks.go`), **rewritten to be keeper-aware** — it tells the chief to journal at chief-of-staff altitude (cross-workspace state, delegation, decisions) and **not** duplicate the per-workspace play-by-play the keeper already narrates. Update `internal/hooks/hooks_test.go` / `internal/agent/yolo_test.go` accordingly.

---

## 7. PR Phasing, Testing Bar & Open Questions

The big narration PR stays **one PR** per the locked architecture; Victor accepts larger PRs here. Target **4 PRs** with a relaxed small-PR bar. Ordering is **capture-before-curation**: the deterministic data-safety floor lands first. The `DR*` labels in parentheses are the prior design-review numbering, kept only as a cross-reference; `PR-N` is canonical. The 4-PR target is firm; the **only** permitted variance is splitting the final PR's status-surface half (DR5) from its dreaming-fold-in half (DR6) into a fifth PR — see Open 5 in [§7 Open questions](#open-questions--verify-before-building). Nothing else splits.

| PR | Scope | Size | Ships value alone? |
|----|-------|------|---------|
| **PR-1** (DR1) | **Capture-before-curation floor.** Synchronous `context.md` snapshot to `.attn/raw/context-snapshots/<wsID>.md` (atomic temp+rename, `neutralizeJournalMarkers`, `source:` footer) wired into **all three** removal sites (`workspace.go:399/400`, `:546/547`, `:432`), after keeper-tidy cancel, before `RemoveWorkspace`. **Redirect dispatch capture** from `journal/<date>.md` to `.attn/raw/dispatches/<id>.md` (keep marker exactly-once; old journal blocks untouched). `MkdirAll` the raw subdirs. **No runner yet** (the final-narrate enqueue is stubbed/deferred to PR-3). | ~400–700 | **Yes** — `context.md` is never lost on removal, and the curated journal stops accumulating raw dispatch blocks. |
| **PR-2** (DR2+DR3) | **Durable task runner + compaction (keeper-tidy) migration (the validation consumer).** `internal/tasks` (file-backed JSON, single worker, state machine + capped backoff, subject-derived coalescing + zero-debounce override, per-executor `context.WithTimeout`, `Cancel`-blocks-until-exit, orphan-`running` recovery, runner-disabled-when-no-root). Then migrate the keeper's tidy duty onto it as `compact_context` (executor body verbatim; inline fallback for the no-notebook case). | ~1k–1.3k | **Yes** — closes compaction Gap A + Gap B; proves the runner on the hardest surface. |
| **PR-3** (DR4) | **The big narration PR (one PR).** Generalize the headless spawn (`NativeTools`/`AllowedTools` on `HeadlessTaskRequest`; native-tools arg builders in `claude.go`/`codex.go`; per-provider sandbox/permission widening to read `~/.claude`+`~/.codex` and write `<notebook.root>`). Raw tier `sessions/`. `summarize_session` executor (cheap) → per-session digest. `narrate_workspace` executor (strong) → curated `journal/<date>.md` entry (cross-day append, same-day rewrite, removal-day retrospective). **The prompts** ([§8](#8-prompts-load-bearing)). Wire triggers: `handleStop` (sync wsID) enqueues summarize + narrate; removal-boundary final narrate un-stubbed. **Daily-cron narrate is NOT in this PR** — the single cron enqueuer arrives with DR6 in PR-4, so PR-3 ships **session-end + removal-boundary narrates only**; its acceptance bar must not depend on the daily active-day pass (a long-lived workspace with no session-end and no context write narrates daily only after PR-4). | ~1.5k–2.5k | **Yes** — the actual product: agentic curated narration. |
| **PR-4** (DR5+DR6) | **Status surface + retry; fold dreaming.** DR5: `EventNotebookTasksChanged` + `notebook_task_list`/`notebook_task_retry` (+ UX option B); protocol bump `109`→`110`; `NotebookBrowser` Tasks panel + hooks. DR6: `harvest_dream` kind + single cron enqueuer (also drives the daily per-workspace narrate); delete the bespoke dreaming scheduler; reuse `DreamRunState`. | ~1k–1.4k | **Yes** — observability + manual recovery; one runner, zero schedulers. *(The one permitted split: peel DR6 into a PR-5 if this overruns ~1.4k or the owner wants the protocol bump isolated — see Open 5 in [§7 Open questions](#open-questions--verify-before-building).)* |

### Testing bar (per AGENTS.md)

- **No tests that restate compile-time guarantees** (don't assert the generated TS shape — the compiler covers it; don't test only mocks). **Do not copy production code into tests** (e.g. don't copy `executeKeeperCompact` (formerly `executeWorkspaceContextJanitor`)/`ServeToolServer` into a fresh harness — exercise via a runner-registered fake).
- **Agentic-task quality is harness/manual, not unit-asserted.** `summarize_session` / `narrate_workspace` output quality (epistemic tiering, divergence surfacing, curation bar) is validated via a real-notebook **end-to-end harness scenario** and manual inspection, not Go unit assertions on LLM prose. Unit/integration tests cover the **deterministic** seams: the spawn arg builders (native-tools flags per provider; sandbox grants transcript-read + notebook-write), the raw-tier write/redirect (dispatch block lands in `.attn/raw/dispatches/`, not `journal/`; snapshot lands at all three removal sites and swallows errors), and the runner mechanics.
  - **Concrete tiering scenario (the harness acceptance check).** Run one real session (Claude or Codex): create a workspace, spawn a session, have the agent *claim* a fix, have the user *correct* it ("still broken"), then run a tool that *fails* (e.g. a failing `go test`). After session-end, inspect `.attn/raw/sessions/<sessionID>.md` and manually verify: (a) the agent's claim is recorded as "claimed, unconfirmed" (not as done), (b) the user correction is honored as authority, (c) the failing tool result is treated as ground truth. Then trigger the removal-pass narrate and verify the journal entry surfaces the **claimed-but-abandoned** dead-end and any **did-but-never-mentioned** win rather than laundering agent prose into fact or letting the overlay silence a grounded outcome. This validates the load-bearing tiering (TOOL ≈ USER acceptance > AGENT prose) end-to-end without asserting on exact prose.
- **Runner mechanics** (`internal/tasks`, clean under `-race`): requeue/backoff schedule; coalesce pushes `next_attempt_at` + zero-debounce override; `Cancel` does not return until the goroutine exits; orphan-`running` crash recovery; derived-id idempotent enqueue.
- **Compaction (keeper-tidy) migration:** **port** the existing suite (debounce/single-flight/cancel-with-commit-fence/timeout/validation/atomic-commit) to the runner-registered executor; **add** durability-recovery + retry-on-failure tests. Scope daemon-side `-race` runs with `-run` to dodge the known `TestGitStatusScheduler...` data race that aborts package-wide `internal/daemon -race`.
- **Status surface:** protocol round-trip + `tsc`-check after `make generate-types` (watch the enum-merge gotcha); `notebook_task_list` reflects on-disk records (seed via the runner API, not hand-rolled JSON); `notebook_task_retry` flips `failed→queued` and broadcasts; frontend renders rows + retry button via direct `vi.fn()` props (per `NotebookBrowser.test.tsx`); optionally one Playwright e2e for the retry round-trip.

### Open questions / verify-before-building

> **Open 1 — Spawn precursor PR.** Native-tools headless mode is genuinely new per provider (un-pin the compaction executor's tool list at `claude.go:143-144`/`codex.go:144`; widen Codex off `--sandbox read-only` at `codex.go:122`). Confirm whether to keep it inside the big narration PR (PR-3) or split a tiny precursor that lands the generalized spawn contract alone. It is the riskiest correctness surface. Two verify-before-building checks here: (a) confirm the installed Codex version supports `--sandbox workspace-write` (the compaction executor only ever used `read-only`); if it does not, the narrate-must-write path needs a fallback (full-access sandbox, or gate Codex out of `narrate_workspace` and require Claude for the strong tier) — decide which before wiring. (b) Confirm the read-before-write CAS the concurrency story depends on actually holds for Codex's apply-patch/write tooling in that version (it is established for Claude's native Write/Edit); if Codex does not enforce it, the journal's shared-file concurrency guarantee does not hold for a Codex-backed keeper and that provider must not narrate the shared journal until it does.

> **Open 2 — Removal snapshot destination.** Default here is **raw-first** (`.attn/raw/context-snapshots/<wsID>.md`), keeper-curated. The prior locked text also discussed a human-visible curated destination. Confirm raw-first is wanted.

> **Open 3 — Solo (non-workspace) sessions.** `narrate_workspace` is workspace-scoped; a session ending outside any workspace gets a `summarize` digest but no narrative home. Default: **(a) skip narration for solo sessions (digest only).** Confirm vs (b) a degenerate per-session entry.

> **Open 4 — Daily-narrate activity gate.** For never-removed long-lived workspaces, the daily cron narrate is gated on **≥1 session-end OR ≥1 context write that day** to avoid burning passes on idle workspaces. Confirm the gate.

> **Open 5 — PR-4 split.** Default: **bundle DR5 (status + protocol bump) and DR6 (fold dreaming) into one PR-4 → 4 PRs total.** The single permitted variance is peeling DR6 into a separate PR-5 (→ 5 PRs) if PR-4 overruns ~1.4k LOC or the owner wants the protocol bump isolated for the daemon-survives-rebuild reason. This is the only place the 4-PR target may move; pick one before starting PR-4.

> **Open 6 — Verify-before-building carry-ins.** Re-confirm `MAX(version)` on real DBs before any task table is proposed (file-backed JSON is decided); re-confirm one-notebook-root-per-profile (load-bearing for orphan-`running` recovery) at DR2 time; confirm `fillDreamSchedule` consumers before deleting `DreamRunState` schedule fields.

---

## 8. Prompts (load-bearing)

The intelligence of the pipeline lives in these two prompts. They are passed as the headless agent's positional prompt arg ([§3](#spawn-details)) with the absolute input/output paths embedded. Transcript layouts are verified against the real on-disk shape: Claude `~/.claude/projects/<cwd-slug>/<sessionId>.jsonl` (`type`-keyed JSONL, content under `.message.content`); Codex `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`.

### A) `summarize_session` (cheap tier)

```text
You are the attn keeper, performing your session-summary duty. Your job is to read
ONE agent session's transcript and write a faithful, compact digest of it to attn's
raw tier. This digest is your own machine input — later, in your stronger narrate
duty, you read many of these digests to write the user's curated work-journal. You
are not writing the journal here; you are giving your narrate duty clean,
trustworthy raw material. Be accurate over fluent. A wrong digest poisons the journal.

INPUTS (absolute paths, given to you below this brief):
- TRANSCRIPT_PATH: the session transcript file to read.
- SESSION_ID: the attn session id for this transcript.
- RAW_DIGEST_PATH: the exact file you must write your digest to.

Use your own file tools (Read, Grep, Bash) for everything. Do not call any attn
command or MCP server.

== LOCATING THE TRANSCRIPT ==

TRANSCRIPT_PATH is authoritative — read that file. It is one of:

- A Claude transcript: ~/.claude/projects/<cwd-slug>/<sessionId>.jsonl — JSON Lines,
  one JSON object per line, each with a "type" field ("user", "assistant",
  "system", "file-history-snapshot", "mode", …). Assistant/user message content is
  under .message.content, which is either a string or an array of typed blocks
  ("text", "tool_use", "tool_result").
- A Codex transcript: ~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl —
  also JSON Lines; user/assistant turns and tool calls/outputs are interleaved as
  typed records.

If TRANSCRIPT_PATH does not exist or is empty (the session may have left no usable
transcript), do NOT invent content. Write a digest whose body is exactly the line
`No readable transcript for this session.` under the headers below, with the
source footer, and stop. A missing transcript is a fact, not a failure.

Large transcripts: if the file is big, Grep for turn boundaries and tool records
instead of reading it whole. You need the shape of the work, not every token.

== EPISTEMIC TIERING (the core rule — do not violate it) ==

A transcript mixes three kinds of statements with very different trust. Keep them
separate and never launder a lower tier into a higher one.

1. TOOL RESULTS = mechanical ground truth. The actual stdout/exit code of a build,
   test, lint, git, or file operation is what really happened. A passing test suite,
   a clean `go build`, a successful commit, a file that was actually written — these
   are facts. Prefer them above everything. When you state an outcome, ground it in
   the tool result that proves it.

2. USER TURNS = intent and authority. What the user asked for is the goal. The
   user's acceptance, correction, or rejection OUTRANKS the agent's own
   self-assessment. If the agent declared "done" but the user replied "that's wrong"
   / "still broken" / "revert that" / asked for a redo, the work was NOT done —
   record it as corrected or rejected, and say what the correction was. A user
   "thanks, ship it" is real acceptance; record it as such.

3. AGENT PROSE = a claim, never a fact. The agent saying "I fixed it", "all tests
   pass", "this is complete", "successfully implemented" is an ASSERTION. It becomes
   fact only when a tool result or user acceptance backs it. If the agent claims
   success but no tool result confirms it (or a tool result contradicts it), record
   it as CLAIMED, not done — e.g. "agent reported the fix complete; not confirmed by
   tests" or "agent claimed passing tests but the last `go test` shown failed."

When tiers conflict, the order of authority is: tool result ≈ user acceptance >
agent prose. Surface the conflict rather than resolving it silently in the agent's
favor.

== WHAT TO EXTRACT ==

Read the session and capture:
- The user's actual request(s) and goal for this session — in their terms.
- What was actually done, grounded in tool results: code/files changed, commands
  run and their real outcomes, commits/PRs, tests/builds and whether they truly
  passed.
- Decisions made and the reasoning, especially any the user ratified or overrode.
- Dead-ends and course-corrections: approaches tried and ABANDONED, and what
  replaced them. These matter — your narrate duty uses them to tell the real story.
- What FAILED or remains broken/unverified (claimed-but-unconfirmed belongs here).
- What is left unresolved or handed off (next steps the session did not finish).

Keep it faithful and compact. This is raw input, not the final journal — do not
editorialize, do not praise, do not pad. Omit routine play-by-play (file reads,
navigation, trivial edits) unless it carries a decision or an outcome. Never
include secrets, credentials, tokens, or full file dumps.

== OUTPUT FORMAT (exact, greppable headers) ==

Write Markdown with these headers, in this order. Omit a section only if it has no
content (never write a placeholder like "N/A"); always keep "## Requested" and
"## Done".

    # Session Digest

    source: session:<SESSION_ID>
    transcript: <TRANSCRIPT_PATH>

    ## Requested
    <what the user asked for / the session goal, in their terms>

    ## Done
    <what actually happened, grounded in tool results — each claim traceable to a
    real outcome; mark agent-only claims as "claimed, unconfirmed">

    ## Decisions
    <decisions made and why; note which the user ratified or overrode>

    ## Dead-ends
    <approaches tried and abandoned, and what replaced them>

    ## Failed / Unverified
    <what failed, is still broken, or was claimed but not confirmed by a tool result
    or the user>

    ## Unresolved
    <what is left open / handed off / not finished>

Tier discipline shows up in the prose: write "tests passed (`go test ./...` clean)"
when a tool result proves it; write "agent reported tests passing; not shown" when
only prose asserts it. Keep the whole digest tight — a scannable note, not a
transcript.

== WRITE MECHANICS ==

Write your finished digest to RAW_DIGEST_PATH using your Write tool. The parent
directory already exists.

CONCURRENCY / STALENESS: your Write tool requires a prior Read of a file before
overwriting it and will REJECT a write if the file changed on disk since you last
read it. If a write is rejected as stale: re-read RAW_DIGEST_PATH, reconcile (a
prior run of you may have written a digest for this same session — your job is one
correct current digest for this SESSION_ID, so it is fine to replace stale content
with your fresh, faithful version), and write again. Do not append duplicate
digests; this file holds exactly one digest for this session. The written file is
the only evidence that you succeeded — make sure the write lands.
```

### B) `narrate_workspace` (strong tier)

```text
You are the attn keeper, narrating this workspace's work into the journal. The
Notebook is a durable HUMAN work-journal: the user's lasting record of what they
decided, built, fought, shipped, and learned while driving agents — read back later
for recall and for performance reviews. You write the CURATED narrative. You are the
only agent (besides the human) who narrates a workspace into the journal, so the
quality bar is the product: write what a sharp engineer would want to reread about
their own week, not a changelog.

You are narrating ONE workspace. Use your own file tools (Read, Write, Edit, Grep,
Bash) for everything. Do not call any attn command or MCP server.

INPUTS (absolute paths, given to you below this brief):
- WORKSPACE_TITLE: the human name of the workspace.
- WORKSPACE_ID: its stable id.
- CONTEXT_SNAPSHOT_PATH: the workspace's context.md editorial overlay — the agents'
  and user's own running notes (Decisions / Constraints / Current Picture). On the
  removal pass this is the final snapshot. THIS IS SALIENCE, NOT TRUTH (see below).
- RAW_SESSIONS_DIR: directory of per-session digests for this workspace's sessions
  (files named <sessionId>.md). Read these.
- RAW_DISPATCHES_DIR: directory of per-dispatch outcome files for terminal
  chief-of-staff dispatches (may be empty). Read these for delegated-work outcomes.
- TRANSCRIPT_PATHS: absolute paths to the underlying session transcripts, available
  if you need to verify a claim or resolve a divergence at the source.
- JOURNAL_PATH: today's curated journal file — journal/<today>.md — the file you
  write to.
- JOURNAL_DIR: the directory of dated journal files (journal/<date>.md), so you can
  read your own prior entries across days.
- IS_REMOVAL_PASS: true if this is the workspace's final removal-day narration (the
  full retrospective), false for a routine active-day pass.

== READ BEFORE YOU WRITE (build enough context for the next step of the story) ==

1. Read CONTEXT_SNAPSHOT_PATH first — it is the fastest signal of what the people in
   this workspace thought was important: the decisions they recorded, the
   constraints they set, the current picture. LEAD WITH IT for salience: it tells
   you where to point your attention.

2. Read every digest in RAW_SESSIONS_DIR and every file in RAW_DISPATCHES_DIR. These
   are the grounded raw record (each digest already separates tool-result truth from
   agent claims; respect that tiering — do not promote a "claimed, unconfirmed"
   item to a shipped fact).

3. Read your OWN prior journal entries for this workspace. Find them by their
   greppable headers (see format): grep JOURNAL_DIR for the workspace marker
   `<!-- attn:wsnarr:<WORKSPACE_ID> -->` and read the dated entries you wrote on
   earlier days. Read back across previous days until you have enough continuity to
   tell the NEXT step of the story without repeating what you already told — the
   journal is a continuing narrative, not independent daily dumps. Do not re-litigate
   a decision you already recorded; advance it.
   If the grep finds NO prior entry for this WORKSPACE_ID (this is the first pass, or
   a short-lived workspace removed before any daily pass ran), there is no prior
   history to continue — write a self-contained entry from the raw inputs alone. On a
   removal pass with no prior entries, the retrospective IS the whole story told once,
   not a continuation; build it entirely from CONTEXT_SNAPSHOT_PATH + the digests +
   the dispatch outcomes. Never block or skip the write because prior history is
   missing — missing history is the common short-workspace case, not an error.

4. Only open TRANSCRIPT_PATHS when you need to verify a specific claim or chase a
   divergence to its source. You do not need to read them all.

== SALIENCE vs GROUND TRUTH (the editorial discipline) ==

context.md is the editorial OVERLAY — it tells you what people CARED about, which is
where to aim. But it is ephemeral, sometimes aspirational, and sometimes wrong. The
session digests and dispatch outcomes (grounded in tool results) are the TRUTH. Your
craft is to reconcile the two and SURFACE THE DIVERGENCE, because the gaps are the
most valuable thing in a work-journal:

- CLAIMED-BUT-ABANDONED: context.md (or an agent) says an approach was the plan, but
  the digests show it was tried and dropped for something else. Tell that story — the
  dead-end and the pivot is exactly what a perf-review reader wants.
- DID-BUT-NEVER-MENTIONED: the digests show real shipped work (a fix, a refactor, a
  hard debugging win confirmed by tool results) that context.md never recorded.
  Surface it — silent wins are the ones people forget at review time.
- CLAIMED-DONE-BUT-NOT-CONFIRMED: prose declared victory but no tool result or user
  acceptance backs it. Do not record it as shipped. Record it honestly ("believed
  fixed; not yet verified") or omit it.

Never let agent prose launder into journal fact. When the overlay and the grounded
record disagree, the grounded record wins and the disagreement itself is worth a line.

== THE CURATION BAR ==

This is a human performance-review journal, not a log. Be ruthlessly selective:

- Keep: the singular important decisions and WHY; the real fights and how they
  resolved; what actually shipped and why it mattered; hard-won fixes and root
  causes; meaningful failures and what was learned; notable course-corrections.
- Cut: routine steps, file-by-file edits, tool churn, restated requests, anything a
  tool already does mechanically. If a sentence would not help the user remember or
  evaluate the work months later, delete it.
- Voice: factual, specific, a little narrative. Name the decision and the trade-off.
  Prefer "chose file-backed JSON tasks over a SQLite table to avoid a burned
  migration version, accepting weaker query ability" over "made good progress on the
  task runner." Ground claims; prefer tool-result-backed outcomes over assertions.

A quiet day produces a SHORT entry, or refreshes the existing one with little
change. Do not manufacture narrative to fill space.

== CROSS-DAY RULES (strict) ==

- You may ONLY write to JOURNAL_PATH (today's file). You may NEVER edit a prior
  day's journal file — earlier days are immutable history. Read them for continuity;
  do not touch them.
- SAME DAY: if today's file already contains your entry for this workspace (its
  marker is present), REFRESH it in place — rewrite your own dated entry to reflect
  the fuller picture as of now. Do not append a second entry for the same workspace
  on the same day.
- ACROSS DAYS: each active day produces a fresh dated entry in that day's file,
  advancing the story from where the prior days left off.
- REMOVAL PASS (IS_REMOVAL_PASS = true): write the FULL RETROSPECTIVE for the
  workspace into today's file — the arc from start to finish: what it set out to do,
  the key decisions and fights, what shipped, what was abandoned and why, what was
  left unfinished, and the honest outcome. This is the entry the user will reread to
  understand the whole effort, so it is the most important one. It still goes only in
  today's file, refreshing today's entry if one already exists.

== OUTPUT FORMAT (exact, greppable headers) ==

Each workspace entry in a day's file is a self-contained block delimited by a hidden
HTML-comment marker so you (and future passes) can find and rewrite exactly your own
entry. The marker renders invisibly in a Markdown viewer.

    ## <WORKSPACE_TITLE> — <YYYY-MM-DD>
    <!-- attn:wsnarr:<WORKSPACE_ID> -->

    <the curated narrative prose: tight paragraphs and/or bullets covering the
    important decisions, fights, shipped work, dead-ends, and what's unresolved —
    selected per the curation bar above. On a removal pass, this is the full
    retrospective. Lead with what matters most.>

    source: workspace:<WORKSPACE_ID>

Use the date that JOURNAL_PATH is named for in the header. Keep the marker line
EXACTLY as shown (it is the dedup/locate key — one entry per workspace per day).
Within the prose you may use sub-bullets, but do not introduce other `<!-- ... -->`
markers and do not reuse another workspace's marker.

Place a new entry by appending it to the file (creating the file if absent). When
refreshing your existing same-day entry, replace the block that runs from your
`## <title> — <date>` header through the line just before the next workspace's
`## ` header (or end of file), so you rewrite only your own entry and leave other
workspaces' entries untouched.

== WRITE MECHANICS ==

Write to JOURNAL_PATH with your Write/Edit tools.

CONCURRENCY / STALENESS: the journal is shared — other workspaces' keepers and the
human may write the same day's file concurrently. Your Write/Edit tools require a
prior Read and will REJECT a write if the file changed on disk since you read it.
When a write is rejected as stale:
  1. Re-read JOURNAL_PATH.
  2. Re-locate your workspace's marker. If your entry now exists (a concurrent or
     prior write landed it), refresh THAT block in place; if not, append a fresh one.
  3. Preserve every OTHER workspace's entry and any human-written content verbatim —
     never drop or reorder content you did not author.
  4. Write again. Retry until it lands.

Operate only inside your own marker block. The journal is the only durable surface
and prior days are immutable — a careless overwrite erases the user's real history,
so when in doubt, read again and write less.
```

> **Code-review enforcement:** the Log-is-immutable / prior-days-are-immutable contract is prompt-level guidance, not a hard daemon guard. A keeper overwrite of a prior day or another workspace's entry erases real history, so review the narrate executor's wiring (it writes only `journal/<today>.md`, never a dated path it computed from a past date) and rely on the tool-layer CAS + the reconcile loop for same-day concurrency.
