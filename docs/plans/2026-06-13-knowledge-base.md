# Plan: Knowledge Base (Durable Markdown Memory)

Status: discussion → pre-implementation. Date: 2026-06-13.
Integration branch: `feat/chifplace` — implementation PRs target it; it merges to `main`
only when the epic is stable (pull `main` in periodically to avoid drift). External sync
(huxton) is explicitly **out of scope**; see *External-Sync Compatibility*.

## Goal

Give an attn profile a durable, filesystem-based, opinionated **markdown knowledge
base** — journals, distilled memory, and cross-workspace decisions — that:

- outlives any single workspace (unlike `context.md`, which dies with its workspace),
- is written by the **daemon on behalf of** agents (chief-of-staff), a periodic
  consolidation pass (the "dreaming" janitor), and the user (an in-app Obsidian-style
  editor),
- is plain `.md` on disk so it stays browsable in any markdown editor and *compatible
  with* external sync (huxton being the concrete target) — but no sync integration is in
  scope; the architecture must only avoid precluding it,
- and follows attn's **daemon-owned** principle for every attn-originated mutation.

This is the *durable memory layer*. It is **not** a task tracker (a separate concern —
"what to do", not "what we know"), and it does **not** replace `context.md`.

## Scope: What It Gives, Won't Give, Could Give

### What it gives (v1, PR1–8)

- A durable, profile-wide markdown memory that **outlives workspaces**: dated journals +
  distilled memory notes + cross-workspace decisions, plain `.md` you own, openable in any
  editor and git-trackable.
- **Daemon-owned** read/write/list/move with atomic writes, hash-CAS edits, and
  `vault_changed` events — every attn-originated mutation is serialized, safe, observable.
- Agents (esp. the **chief**) read `memory/index.md` at launch and write notes/decisions via
  `attn vault …`; role-conditional guidance steers the chief to the vault over `context.md`.
- An opt-in nightly **dreaming** pass that promotes recurring, grounded facts from journals
  + `context.md` snapshots + **closed dispatches** into durable memory — with audit trail,
  dry-run, rollback. This turns raw activity into institutional memory and closes the
  closed-dispatch/mailbox-limbo gap.
- An in-app **Obsidian-style viewer/editor** in two modes (tile alongside agents, fullscreen),
  with link nav, backlinks, live updates, and a **highlight → send to chief** action.
- A directory an external sync tool can later point at — **without any sync code in attn**.

### What it won't give

- **Not a task tracker** ("what to do"). Memory ≠ tasks.
- **No cross-device sync / encryption / relay** — that's an external tool's job, out of scope.
- **No semantic search / embeddings / RAG** in v1 — navigation is tree + links + tags + cheap
  text; retrieval is filesystem-shaped.
- **No automatic pruning** of durable memory in v1 (append-only grows; bounded compaction is a
  deferred, separate, reviewable op).
- **Not real-time collaborative editing** — concurrency is hash-CAS + fsnotify reconcile, not
  character-level CRDT (an external sync tool can add that at the file layer).
- **The daemon is not the only possible writer** — external editors/sync can write; attn
  reconciles but does not lock the filesystem.
- **Dreaming won't invent memory** — strictly grounded; by design it can miss genuinely-novel
  one-off insights that never recur (recurrence is the durability signal).

### What it could give if we adjust it

- **Semantic recall:** a daemon-side embedding index so agents ask "what do we know about X?"
  instead of navigating the tree — the biggest force-multiplier, moderate cost.
- **Typed links / graph:** promote relationship kinds (supersedes / derived-from) to
  structured edges + a graph view — enables real querying.
- **Context priming (reverse bridge):** dreaming pulls `context.md` → vault today; it could
  also push relevant durable memory *into* a new workspace's `context.md` at creation.
- **Task bridge:** memory notes reference tasks by id in an external tracker — "what to do"
  beside "what we know" without merging the systems.
- **Richer dreaming:** a weekly "reflect" digest, shadow-trial scoring, or session-end (not
  just nightly) consolidation.
- **Shared/org memory:** a vault shared across profiles/teammates (needs the external sync layer).

## Decisions Locked (this discussion)

1. **Distinct durable layer, not `context.md` scaled up.** `context.md` keeps its
   SQLite-canonical, revision-CAS, per-session-checkout model for live multi-session
   coordination. The Knowledge Base (KB) is a separate, profile-wide, filesystem-canonical
   durable layer. The dreaming pass is the **bridge**: it harvests durable facts out of
   `context.md` snapshots (and journals, and closed dispatches) into KB memory.
2. **User-facing, external-sync-compatible, profile-derived location.** Configurable
   `vault.root`; the **default is derived from the active profile** — default profile →
   `~/attn-vault/`, named profile `foo` → an isolated default (e.g. `~/attn-vault-foo/`) — so
   a dev/test profile never writes the real vault. Lives *outside* `~/.attn[-profile]/`, fully
   functional standalone (local + git), a plain directory any external sync tool can later
   point at; no sync integration ships in this plan. Parallel agents test the KB in isolation
   purely by using distinct `ATTN_PROFILE` values (existing isolation: data dir + socket +
   hash-derived port). Parallel *packaged-app* testing stays single-tenant (separate epic).
3. **Cron + opt-in session-end staging.** Daily cron runs the expensive LLM promotion
   (cost-bounded, batches a day). An opt-in, debounced post-session-close nudge cheaply
   *stages* candidates for the next nightly run. Disabled by default.
4. **v1 consolidates journals + `context.md` snapshots + closed dispatches.** Dispatch
   consolidation is in v1 (not deferred) to close the "mailbox/closed-dispatch in limbo"
   gap. This couples the harvest to the chief-of-staff dispatch schema — accepted.
5. **The chief-of-staff writes to the KB too**, not only the dreaming pass and the user.
   All such writes go through the daemon.
6. **An in-app Obsidian-style markdown view/edit UI** is part of the feature.
7. **Daemon-owned everywhere.** Every attn-originated KB mutation (agent, dreaming, UI)
   is a daemon-owned transaction with atomic writes and a `vault_changed` broadcast. The
   frontend and agents never write vault files directly.
8. **The chief prefers the vault over `context.md`.** The chief is profile-wide, so
   per-workspace `context.md` is the wrong primary surface for it. A chief session gets
   `VaultGuidance` *instead of* the workspace-context checkout guidance; it can still read
   a specific workspace's context manually when it steps in, but the vault is its home.
9. **Live activation = PTY trigger → CLI guidance pull.** Enabling KB mode on an
   already-running session injects a *bounded* PTY prompt ("run `attn vault guide`"); the
   agent runs that daemon-owned CLI and pulls the real guidance from stdout. The PTY
   carries only the doorbell; content stays daemon-owned and versioned.

## Architecture Map

```text
WRITERS (attn-originated, all daemon-owned)
  chief-of-staff agent ─┐
  in-app markdown UI ───┤   attn vault <verb>  /  ws command
  dreaming janitor ─────┘            │
                                     ▼
                      ┌──────────────────────────────┐
                      │  daemon: vault service (auth) │  single in-process writer
                      │  - read / list / write / move │  atomic temp+rename
                      │  - hash-CAS on edit           │  validate kind/frontmatter
                      │  - fsnotify reconcile         │  emit vault_changed
                      └──────────────┬────────────────┘
                                     ▼
                         <vault.root>/*.md  (filesystem-canonical)
                                     ▲
EXTERNAL WRITERS (reconciled, not owned) ──┤  (out of scope to build; must not break)
  external markdown sync (e.g. huxton) ────┤  daemon detects via fsnotify,
  real Obsidian / editor (optional) ───────┘  reloads, re-emits vault_changed
```

## On-Disk Format

OKF's useful core (markdown + YAML frontmatter + relative links forming an untyped
graph; one required field; reserved `index.md`/`log.md`; path-is-identity; permissive
consumers; preserve-unknown-keys). Drop OKF's data-catalog baggage (`type: BigQuery
Table`, `resource:` cloud URIs, export pipelines). OKF is v0.1 (published 2026-06-12) —
a compatible baseline we superset, not a spec we conform to.

```text
<vault.root>/
  index.md                 # bundle root; declares okf_version + kind taxonomy (no frontmatter, like OKF)
  log.md                   # global change history, date-grouped, newest first
  journal/
    2026-06-13.md          # append-only, dated; the raw system of record
  memory/                  # durable distilled notes (high-signal layer)
    index.md
    decisions/             # cross-workspace + dispatch decisions that outlived a PR
    gotchas/               # repeated surprises
    domain/                # glossary / business rules
  workspaces/
    <workspace-slug>/index.md   # optional durable per-workspace digests
  .attn/                   # machine state — dotdir, ignored by huxton's scanner
    dreams/candidates.json
    dreams/checkpoints.json   # last-run cursor per source
    dreams/runs/YYYY-MM-DD.md  # dated run reports (audit trail)
    dreams/locks/
```

Frontmatter — exactly one required field (OKF discipline):

```yaml
kind: memory            # journal | memory
```

Recommended/optional (OKF's set + two deliberate extensions):

```yaml
title: PR CI skips on dirty merge
summary: Zero checks usually means a merge conflict with main, not slow CI.
tags: [ci, github, gotcha]
created: 2026-06-01T09:00:00Z    # extension: OKF has one timestamp; we split
updated: 2026-06-13T14:30:00Z
sources:                          # extension: grounded provenance, first-class
  - /journal/2026-06-01.md#pr-312
  - dispatch:dsp_abc123
  - https://github.com/.../pull/312
```

- **Links:** root-absolute markdown — `[x](/memory/decisions/foo.md)` — **not**
  `[[wikilinks]]`. huxton does not resolve wikilinks; markdown links are zero-infra,
  GitHub-rendered, survive moves, and the in-app UI resolves them itself. Relationship
  *kind* (supersedes / relates-to / derived-from) lives in prose, not link syntax.
- **`sources:` is the anti-hallucination spine.** Every durable `memory` note must carry
  resolvable sources (journal anchors, `dispatch:<id>`, or URLs). Enforced mechanically
  in the promote phase; the summarizing model may not author memory from paraphrase alone.
- **Permissive reader:** never reject on unknown `kind`, extra keys, broken links, or
  missing index — the realistic state of agent-authored files. Preserve unknown keys on
  round-trip so huxton/Obsidian/user fields survive.

## Write Authority & Concurrency

The KB is **filesystem-canonical** (so Obsidian + huxton stay first-class), and the
daemon is the **authority for attn-originated writes** plus the **reconciler for
external writes**. This is the honest reconciliation of "daemon-owned everywhere" with
"plain files on disk that other tools also touch."

- **In-attn writes go through the daemon vault service**, never the Tauri fs API or a
  raw agent file write. The service:
  - serializes writes (single in-process writer; per-path mutex),
  - writes atomically (temp + rename, mirroring huxton's `writeFileAtomic` and the
    existing checkout writer),
  - validates `kind`/frontmatter and (for `memory`) `sources:` resolvability,
  - emits a `vault_changed` event (async request/result pattern, AGENTS.md §2).
- **Edits use hash-CAS** (lighter than `context.md`'s SQLite revision CAS, since the FS
  is canonical): the caller passes the base content hash it read; the daemon writes only
  if the on-disk hash still matches, else returns a conflict the UI/agent reconciles.
  Journals are **append-only** (appends rarely conflict; serialize and append).
- **External writes are reconciled, not owned.** The daemon watches `vault.root` with
  fsnotify; on an external change (huxton materialize, real Obsidian) it reloads and
  re-emits `vault_changed`. attn cannot stop external tools from writing — so it treats
  them as a legitimate concurrent writer and never assumes it holds the only copy.
- Startup recovery: scan for orphaned `.attn/dreams/locks/` and incomplete runs and
  clear them (the workspace-context janitor's lock lifecycle is the template).

## Data Model / Interfaces

```text
Settings (existing store.GetSetting/SetSetting — no new config plumbing):
  vault.root                       # absolute path; default ~/attn-vault, profile-namespaced
  vault.dreaming.enabled           # bool, default false
  vault.dreaming.frequency         # cron, default "0 3 * * *"
  vault.dreaming.timezone
  vault.dreaming.model             # optional override

Daemon vault service (new internal/vault + daemon handlers):
  List(prefix) -> []VaultEntry{path, kind, title, summary, updated, size}
  Read(path)   -> {content, hash}
  Write(path, content, baseHash?) -> {hash}  // baseHash omitted = create; present = CAS edit
  AppendJournal(date, entry)      -> {path, hash}
  Move(from, to) / Delete(path)
  // all daemon-owned, atomic, emit vault_changed

Protocol (TypeSpec → generate-types → constants.go → bump ProtocolVersion):
  commands: vault_list, vault_read, vault_write, vault_append_journal, vault_move, vault_delete,
            vault_dream_status, vault_dream_run (--dry-run/--apply)
  events:   vault_changed { paths[], origin: agent|dreaming|ui|external }
  results:  vault_write_result { ok, hash, conflict?, current_hash? }

Store surface (minimal — FS is canonical):
  No CAS table. Optional vault_dreaming_runs row OR just JSON under .attn/dreams/.
  Prefer files-on-disk for dreaming state (self-describing, git-diffable).
  Migration: if any table is needed, check MAX(version) in REAL prod+dev DBs first
  (burned-migration-versions); do not trust a number from static grep.

CLI (agents reach the daemon over the unix socket, like `attn workspace context update`):
  attn vault journal append --text ...
  attn vault memory write --path memory/decisions/foo.md [--base-hash H]
  attn vault show <path>   /   attn vault list [prefix]
  attn vault guide         # daemon-owned: prints KB operating guidance (pulled, not PTY-typed)
  attn vault dream status  /   attn vault dream --dry-run|--apply
```

## Dreaming / Consolidation

Modeled on OpenClaw's `memory-core` (cron, append-only durable store, multi-signal
promotion, rehydrate-before-write), simplified and adapted. **The exact OpenClaw weights
(0.24/0.30/0.15/0.15/0.10/0.06) and phase names are a single-vendor source — treat as a
tunable prior, not constants.**

- **Runner:** the **janitor** — headless, daemon-owned, via `RunHeadlessTask` (no TTY, no
  session, scoped MCP). NOT the chief. The chief is an interactive consumer/curator.
- **Trigger:** daily cron (default `0 3 * * *`, timezone-aware) for the expensive pass;
  opt-in debounced post-session-close nudge that only *stages* (cheap, no LLM). Off by
  default. Reuse the janitor single-flight mutex + debounce pattern.
- **Two phases** (resist OpenClaw's full Light/REM/Deep until earned):
  1. **Harvest (no LLM):** scan since-cursor inputs → dedup → stage `candidates.json`.
     Inputs: dated `journal/*.md`; canonical `context.md` snapshots (read via store);
     **closed dispatches** (`ListChiefOfStaffDispatches` → `LatestReport` +
     `StructuredReport` incl. resolved `Request`; "closed" = target session gone).
  2. **Promote (LLM, gated):** rank candidates; for those clearing gates, **append**
     `kind: memory` notes (or merge), each with resolvable `sources:`. Dispatch-derived
     decisions land in `memory/decisions/`. Write a dated run report under
     `.attn/dreams/runs/` and a one-line `log.md` entry.
- **Promotion gates:** weighted score **AND** hard gates — `minOccurrences` across
  `minDistinctContexts` (recurrence is the best durability signal); plus recency-decay
  and relevance. Weights configurable, not hardcoded constants.
- **Safety rails (non-negotiable):**
  - append-only durable writes (never rewrite/delete memory in the same pass),
  - **rehydrate-before-write** (re-read the source at promotion; skip if gone),
  - **grounded-only** (reject candidates whose `sources:` don't resolve),
  - single-writer lock + startup orphan recovery,
  - reversible + auditable (dated run reports, `--dry-run`/`--apply`, git history),
  - token-bounded snippets (~160 tok) with a `sources:` pointer back to full detail,
  - bounded-growth compaction is a **separate, reviewable, reversible** op — explicitly
    NOT folded into the nightly promote pass (defer; flagged now).

## Janitor vs Chief-of-Staff

| | Chief-of-staff | Janitor |
|---|---|---|
| Nature | interactive session, profile singleton (`profile_roles`) | headless daemon task (`RunHeadlessTask`) |
| Trigger | user/agent activity, delegation | cron + debounced nudge |
| KB role | **writes** decisions/notes (via daemon) + **reads** `memory/index.md` at launch + can manually trigger/curate a dream | **produces** durable memory via the dreaming pass |
| State | dispatch records, reports, mailbox | consolidation lock + run reports; no relationship state |

The chief writes through the daemon vault service (`attn vault memory write ...`),
taught via `VaultGuidance` + the attn-skill reference. Producer (janitor) and
curator/contributor (chief) are complementary, not the same actor.

## Chief Guidance & Live Activation

The chief's durable home is the vault, not per-workspace `context.md`.

- **Role-conditional guidance.** When a session holds the `chief_of_staff` role, suppress
  the workspace-context checkout guidance and inject `VaultGuidance` instead (read
  `memory/index.md`, prefer the vault, write decisions via `attn vault memory write`). The
  chief may still `attn workspace context show` for a specific workspace it steps into, but
  that is opt-in, not pushed. Non-chief working agents keep `context.md` as today.
- **`attn vault guide` is the single source of guidance content.** Daemon-owned CLI that
  prints the current KB operating rules (kinds, linking, grounding, write verbs). Both the
  at-launch path (`VaultGuidance` via `--append-system-prompt`/hooks) and the live path
  resolve to the same content, so guidance is versioned in one place.
- **Two delivery paths:**
  - *At launch* — normal hook / `--append-system-prompt` injection (no PTY).
  - *Live (already-running session)* — daemon types a bounded trigger prompt + Enter into
    the session PTY instructing it to run `attn vault guide`, reusing the existing wake
    mechanism (`ptyBackend.Input` + `dispatchWakePrompt` pattern). The agent pulls the real
    guidance from the CLI.
- **Safety (reconciling the chief-of-staff PTY boundary).** The chief-of-staff plan forbids
  typing *arbitrary content* into a live PTY as a coordination primitive. This is the safe
  exception: the PTY carries only a fixed, bounded doorbell ("run this command"); the agent
  *pulls* content from a deterministic daemon-owned CLI. Prefer injecting only into
  idle/waiting sessions (like the wake button), or make live activation user-triggered —
  never type into an agent mid-task.

## Markdown UI (Obsidian-style)

A new frontend surface (Tauri/React) over the daemon WS — **daemon-mediated, never
direct fs** (no Tauri `fs` calls; every read/write goes through `vault_*` ws commands).

**Copy principle:** the daemon is an internal detail and must be invisible in the UI —
surface *outcomes* ("Saved", "added to chief's inbox", "reloaded — file changed on disk"),
never plumbing ("synced via daemon"). The tile is space-constrained and **responsive to its
own size** (ResizeObserver, not window media queries): wide → tree+editor+context; medium →
tree+editor; narrow → file-picker+editor; short → trimmed chrome.

Core:
- file-tree navigation of `vault.root`; markdown render (view) + edit (CodeMirror-style);
- root-absolute link resolution + backlink panel (graph computed daemon-side);
- live updates via `vault_changed` (including external edits); hash-CAS save with conflict reconcile.

**Two display modes** (preserve the open file across a toggle):
- **Tile** — the KB renders as a pane *inside a workspace layout, alongside agent terminals*,
  so you read/curate memory while agents work. Compact chrome (collapsible tree/backlinks).
  Integrates as a pane type in the existing workspace pane/layout system.
- **Fullscreen** — the KB takes the whole window as a wide three-pane Obsidian layout.

**Highlight → send to chief** (context action): select text in the note (view or edit) →
context menu → "Send to chief" (also "New memory note from selection", "Copy link"). The
selection is handed to the **daemon**, which delivers it to the chief; the UI never messages
the chief directly. Recommended delivery: append to a chief inbox note in the vault + an
optional live PTY nudge (see Open Decisions), reusing existing primitives rather than a
bespoke channel.

Ships as its own track: read-only viewer (both modes) first, then editor + send-to-chief.
Uses the async request/result pattern and protocol versioning.

## External-Sync Compatibility (Architecture Only)

No sync integration ships here. The only job is to ensure the architecture does not
*preclude* pointing an external markdown sync tool (huxton is the concrete target) at
`vault.root` later. The invariants that keep that door open:

- **Plain files, paths-as-identity:** `.md` only, no dotfiles-as-notes, ≤2 MiB per file
  (journals rotate daily to stay small), POSIX relative paths as identity.
- **Machine state under `.attn/`** so a dotfile-skipping sync scanner never touches it.
- **Root-absolute markdown links, no `[[wikilinks]]`** — assume no external resolver.
- **Filesystem-canonical + fsnotify reconcile** so an external materializer writing files is
  just another concurrent writer the daemon already tolerates (see *Write Authority*).
- **No attn → sync-tool dependency:** attn never imports or calls any sync tool; the KB is
  fully usable with none present. Sync, if added later, is purely additive and out of band.

## Boundaries

- The daemon is the sole authority for attn-originated KB writes, including rollback;
  external writers are reconciled via fsnotify, not owned.
- The KB is filesystem-canonical; SQLite is not the source of truth for KB content.
- Memory ≠ tasks. The KB is not a task tracker.
- Dreaming only appends durable memory; journals are immutable; durable notes are grounded.
- Dispatch consolidation reads chief-of-staff records; it does not mutate dispatch state.
- Live PTY activation carries only a bounded trigger; guidance content is pulled from a
  daemon-owned CLI, never typed into the PTY. Never inject into an agent mid-task.
- "Send to chief" is a daemon-owned action: the UI hands the selection to the daemon, which
  delivers it and is the only writer of any resulting note. The UI never messages the chief
  directly.

## Implementation Steps (phased, small-PR-friendly)

- [ ] **PR1 — Format + parser (pure, ~400).** `internal/vault`: layout constants,
      frontmatter parse/serialize (preserve-unknown-keys), `kind` validation, permissive
      reader, link parsing. `attn vault init`. Unit tests (round-trip + permissiveness).
- [ ] **PR2 — Daemon vault service + read/list (~500).** Daemon-owned service, `vault.root`
      setting, fsnotify reconcile, `vault_changed` event, atomic writer, hash-CAS write,
      `vault_list`/`vault_read`/`vault_write` protocol + CLI. Bump ProtocolVersion.
- [ ] **PR3 — Write paths + guidance + activation (~650).** `attn vault journal append`,
      `attn vault memory write`; `attn vault guide` (single source of guidance content);
      `hooks.VaultGuidance(indexPath)` (Claude `--append-system-prompt`, Codex
      `developer_instructions`); attn-skill `references/vault.md`. **Role-conditional:** chief
      sessions get `VaultGuidance` instead of workspace-context guidance. **Live activation:**
      daemon types a bounded "run `attn vault guide`" trigger into an idle/waiting session's
      PTY (reuse the wake mechanism). **Chief can now write.** Inject only `memory/index.md`
      path + nav rule (token budget).
- [ ] **PR4 — Read-only markdown UI, both modes (~700).** File tree + render + link nav +
      live `vault_changed`, daemon-mediated; **tile** (workspace pane) + **fullscreen** modes.
      (Editor in PR5.)
- [ ] **PR5 — Editable UI + send-to-chief (~700).** CodeMirror edit, hash-CAS save, conflict
      reconcile, backlink panel; **highlight → send to chief** context menu + daemon delivery
      path (selection → daemon → chief inbox note + optional PTY nudge).
- [ ] **PR6 — Dreaming harvest, no LLM (~600).** Cron scheduler + single-flight + locks;
      harvest journals + `context.md` snapshots + closed dispatches → `candidates.json`;
      `--dry-run`; startup orphan-lock recovery.
- [ ] **PR7 — Dreaming promote via headless MCP (~700).** `attn _vault-dreaming-mcp`
      (vault-scoped read/append tools); gates (occurrence + distinct-context + score);
      rehydrate-before-write + `sources:` grounding enforcement; `--apply`;
      `vault.dreaming.enabled`; `log.md` append; decisions → `memory/decisions/`.
- [ ] **PR8 — Chief integration + status (~400).** Chief reads `memory/index.md` at launch;
      `attn vault dream status`; dashboard surface (last-dream, promoted counts).
- [ ] **Follow-ups:** separate reviewable memory-compaction op for bounded growth; dev/prod
      vault separation doc; optional reflect/digest phase; semantic-recall index. (Out of
      scope here: wiring any external sync tool to `vault.root`.)

The UI track (PR4–5) and the dreaming track (PR6–7) both depend only on PR2–3 and can
proceed in parallel. PRs 1–3 deliver a real, browsable, agent-written journal+memory
vault before any LLM/UI complexity.

## Open Decisions

- **Naming.** Working name "Knowledge Base / vault". "Vault" overloads huxton's term and
  "workspace" is taken; confirm the user-facing name (Vault? Knowledge Base? Notebook?).
- **External-Obsidian policy.** In-app UI is the blessed daemon-owned editor. Do we
  *support* direct real-Obsidian edits (reconciled best-effort) or only document them as
  at-your-own-risk? Recommendation: reconcile, document the boundary.
- **Linking convention** — confirm root-absolute markdown links over `[[wikilinks]]`
  (recommended; huxton-clean).
- **Grounding strictness** — confirm the hard "no ungrounded durable memory" rule
  (recommended; multi-source `sources:` allows legitimate synthesis).
- **"Send to chief" delivery channel** — recommend routing the selection through the vault
  (append to a chief inbox note) + an optional live PTY nudge, reusing existing primitives
  rather than a bespoke channel; this also revives the dormant user→chief path. Confirm.

## Flagged Uncertainties

- OpenClaw dreaming weights/phase names: single vendor doc, not independently verified.
- OKF is v0.1 (2026-06-12), no ecosystem track record — borrow, don't conform strictly.
- External sync (huxton) is out of scope; only the compatibility invariants above are in scope.
- Next migration version: verify `MAX(version)` against real prod+dev DBs before numbering
  (burned-migration-versions); static grep was inconclusive.
- Workspace-context janitor "may never trigger if edited every 9 min": plausible from the
  debounce design — the dreaming pass is cron-anchored precisely to avoid inheriting that
  starvation failure mode.
