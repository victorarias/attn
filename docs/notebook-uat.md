# Notebook Keeper / Knowledge Base — manual UAT runbook

End-to-end, UAT-style verification of the whole Notebook epic (#347–#356 + #358/#359)
against the **dev install** so the live prod app is never disturbed. Code-grounded by
[notebook-processes.md](notebook-processes.md) and [glossary.md](glossary.md).

Each scenario is tagged:

- **[DET]** deterministic, fast, no model spend — run these first.
- **[LLM]** spawns a real headless Claude/Codex agent → **real token spend**. That is the
  point of UAT for narration/compaction, but run them knowingly.
- **[TIME]** time-gated (cron); shortened with a setting so it fires within a minute.

> A scenario "passes" only when the **observable artifact** (file on disk, daemon-log
> line, app UI, DB row) matches. Never trust "it should have run" — look.

## Verification status (last driven 2026-06-20, dev install, protocol 115 / schema 52)

This runbook was driven end-to-end headlessly (CLI over the unix socket, a Node WS driver
issuing real frontend commands, direct SQLite reads, and filesystem inspection). Full evidence
and methodology: [notebook-uat-loop-log.md](notebook-uat-loop-log.md).

**Verified with real artifacts:** A1–A6 (scaffold, guidance, dispatch capture A3 incl.
exactly-once across a daemon restart, snapshot floor, task engine, settings/migration/protocol);
B1/B2 grounded session-end summarize→narrate; B3 removal retrospective + best-effort archive
(confirmed on disk); B4 manual compaction + rollback, **and** the auto-trigger enqueue-on-12KiB
(unit-tested); **Codex as keeper** (real `codex` gpt-5.5 narrate, grounded, no clobber); the
cross-day / one-per-day / relaxed-vs-strict-gate / path-traversal / cross-workspace guards
(unit-tested); C1 daily cron; D1 authoring.

**GUI-only (not headlessly e2e-exercisable):** the old-daemon/new-app protocol-mismatch reload
(frontend gate); the SettingsModal rendering; the D2 hash-CAS edit-conflict modal. The daemon-side
contracts behind these (reports protocol 115; persists keeper config; bounded send-to-chief) are
verified.

**Known limitations — do NOT claim as guaranteed:**
- **Secrets are not scrubbed deterministically.** "Entries never include secrets" is overstated:
  protection is a single best-effort prompt line, not a regex/scrubber before the durable write.
  A canary proved literal example tokens land verbatim in the (externally-synced) journal; a
  realistic credential was omitted only because the agent happened to refuse that task. Treat the
  claim as best-effort, and harden with a deterministic token-shaped-string scrubber.
- **The narration success gate is content-blind.** A `done` narrate verifies the journal block
  *changed*, not that it is *grounded*; narration trusts the carried transcript path without
  re-resolving it. With the agent-session env leak fixed (see the log) this no longer bites, but a
  future transcript-resolution regression would again pass `done` on a vacuous entry.

---

## 0. Environment setup (do once)

### 0.1 Fresh dev build at the tip — **mandatory**
Rebuild from the current branch so we test **protocol 115 / migration 52** and the post-collapse
wiring. Needs the macOS keychain, so run **outside the sandbox**:

```sh
make dev            # builds + installs ~/Applications/attn-dev.app, (re)starts dev daemon on :29849
```

> The janitor→keeper rename is **migration 52** and is `tableExists`-guarded, so it is a clean
> rename on a DB still carrying `workspace_context_janitor_backups` and a safe no-op on a DB
> already renamed. After `make dev` the dev daemon serves **protocol 115** and reports schema
> **52** (`SELECT MAX(version) FROM schema_migrations`). Pre-collapse drafts said 114/51.

### 0.2 Point your shell / CLI at the dev profile
- Interactive **fish** shell: `./attn profile-env --fish dev | source`
- One-off commands (and anything run through the agent's POSIX shell): prefix every call
  with `ATTN_PROFILE=dev`, e.g. `ATTN_PROFILE=dev ./attn list`.

Confirm you're talking to dev (port 29849, data `~/.attn-dev`, notebook `~/attn-notebook-dev`):

```sh
ATTN_PROFILE=dev ./attn profile
ATTN_PROFILE=dev ./attn profile resolve --json | grep -E 'port|data_dir|socket'
```

### 0.3 Paths cheat-sheet
| What | Path |
| --- | --- |
| Notebook root (dev) | `~/attn-notebook-dev` |
| Journal | `~/attn-notebook-dev/journal/<YYYY-MM-DD>.md` |
| Knowledge base | `~/attn-notebook-dev/knowledge/{projects,areas,resources,archive}/` |
| Raw tier | `~/attn-notebook-dev/.attn/raw/{sessions,context-snapshots}/` |
| Task records | `~/attn-notebook-dev/.attn/tasks/` (one file per task; readable id in the JSON body) |
| Cron anchor | `~/attn-notebook-dev/.attn/narrate/state.json` |
| Settings + keeper backups | `~/.attn-dev/attn.db` (`settings`, `workspace_keeper_compact_backups`) |
| Daemon log | `~/.attn-dev/daemon.log` |

### 0.4 Observation helpers
```sh
# Tail keeper/narration activity (run in a side terminal during LLM scenarios)
tail -f ~/.attn-dev/daemon.log | grep -E 'keeper compact:|summarize_session:|narrate_workspace:|daily narrate:'

# Read a setting / list all notebook settings
sqlite3 ~/.attn-dev/attn.db "SELECT key,value FROM settings WHERE key LIKE 'notebook.%' OR key='workspace_keeper_compact'"

# Tree of the notebook root (after scenarios)
find ~/attn-notebook-dev -maxdepth 3 -not -path '*/.git/*' | sort
```

### 0.5 Clean-slate reset (optional, between full passes)
```sh
ATTN_PROFILE=dev ./attn daemon stop
rm -rf ~/attn-notebook-dev          # wipes journal, knowledge, raw tier, tasks, cron anchor
# (leave ~/.attn-dev/attn.db unless you also want to re-test migration 52 from scratch)
ATTN_PROFILE=dev ./attn daemon ensure
```

---

## Group A — Deterministic foundation (fast, no model spend)

### A1 — Cold-start scaffold (PARA/OKF skeleton) **[DET]**
**Proves:** a fresh notebook root is scaffolded with the prescriptive PARA/OKF skeleton.

**Pre:** `~/attn-notebook-dev` absent (see 0.5) and dev daemon running.

> **UAT-verified trigger:** the scaffold is **not** created at daemon start, nor by
> browsing/listing the notebook (`notebook_list` returns `success` but writes nothing).
> `ensureNotebookScaffold` runs only from `handleNotebookGuide` and
> `activateChiefGuidanceLive` — i.e. **when a session is promoted to chief of staff**
> (or a chief is launched). So scaffold by promoting a session to chief (see A2), then:
```sh
find ~/attn-notebook-dev -maxdepth 2 -not -path '*/.attn/*' | sort
```

**Expected:**
- Dirs: `journal/`, `knowledge/{projects,areas,resources,archive}/`, `.attn/`.
- Reserved files: `index.md`, `log.md` (no frontmatter), `knowledge/index.md`, and an
  `index.md` inside each of the four PARA dirs.
- `index.md` / `knowledge/index.md` describe PARA + the **grounding rule** (`sources:` required).
- `inbox.md` is **absent** (created lazily on first send-to-chief, not scaffolded).

**Pass:** all dirs + reserved files present; PARA dirs each carry their own `index.md`.

---

### A2 — Chief-of-staff guidance is native-file (no CLI) **[DET]**
**Proves:** the removed `attn notebook` CLI is gone from guidance; the chief is oriented to
edit files directly, and the live-promotion doorbell points at native paths.

**Steps:**
1. Launch a session in the dev app, then **promote it to chief of staff** from the UI.
2. Watch the chief's terminal for the live-promotion **doorbell**.

**Expected:**
- Doorbell text points at native files (e.g. *read `~/attn-notebook-dev/index.md`*),
  **not** `` `attn notebook guide` `` / `` `attn notebook show` ``.
- The injected guidance frames the chief as owner of the **knowledge base** (PARA/OKF,
  `type:` frontmatter, promote projects→areas, link with root-absolute markdown), and never
  references `attn notebook …`.

**Pass:** zero `attn notebook` references in the doorbell/guidance; native-path pointers present.

---

### A3 — Dispatch capture → raw tier, exactly once **[DET]** — *retired*

> **Retired (tickets epic, slice 7).** The chief-of-staff dispatch namespace and its
> raw-tier "delivery ledger" (`.attn/raw/dispatches/`) were removed when delegated work
> moved to the ticket model. There is no dispatch capture to verify; delegated work is now
> tracked on its ticket (see the ticket board and `attn ticket status`). The original
> scenario below is kept only as a historical record of what A3 verified at the time.

**Proved (historical):** a chief-of-staff dispatch's terminal outcome was captured
deterministically to the raw tier (not the curated journal), exactly once.

---

### A4 — Context-snapshot floor at workspace removal **[DET]**
**Proves:** the daemon synchronously snapshots `context.md` to the raw tier **before** the
workspace row is deleted, so the editorial overlay is never lost. (This is the data-safety
floor; the LLM retrospective it enables is **B3**.)

**Steps:**
1. In a workspace, give the shared context some canonical content:
   ```sh
   ATTN_PROFILE=dev ./attn workspace context show     # prints the editable file path
   # edit that file to add a recognizable line, then:
   ATTN_PROFILE=dev ./attn workspace context update
   ATTN_PROFILE=dev ./attn workspace context status    # note the canonical revision N
   ```
2. **Remove the workspace** from the dev app (or clear its sessions).
3. Inspect:
```sh
cat ~/attn-notebook-dev/.attn/raw/context-snapshots/<wsID>.md
```

**Expected:**
- A `<wsID>.md` snapshot containing the canonical context, ending with
  `source: workspace-context:<wsID>@<N>`.
- A crafted/unsafe workspace id can never appear here — the writer rejects anything that is
  not a single safe path segment (no `/`, `..`, dotfiles, control chars).

**Pass:** snapshot file exists with the content + `@<revision>` footer, written at removal time.

---

### A5 — Task surface: list, state, retry, persistence, crash recovery **[DET]**
**Proves:** the durable task engine is observable and recoverable.

**Steps:**
1. Generate a task (any session stop enqueues `summarize_session`; or run A4/B-series).
2. **App:** ⌘K → Browse the Notebook → expand the **Tasks** panel. Confirm rows show
   *kind, subject, state, attempts, next-run, last error*, and that failed/dead rows show
   **Retry**.
3. **Disk:** `ls ~/attn-notebook-dev/.attn/tasks/` (one file per task) and
   `cat` one — JSON has `id` (`kind:subject`), `state`, `attempts`, `next_attempt_at`.
4. **Crash recovery:** while a task is `running`, hard-stop the daemon
   (`ATTN_PROFILE=dev ./attn daemon stop`), then `… daemon ensure`. The orphaned `running`
   task is reset to `queued` at startup and re-runs.
5. **Retry:** click Retry on a failed/dead row → it moves to `queued` and re-executes; the
   panel refreshes live (the `tasks_changed` broadcast).

**Expected states:** `queued → running → {done | failed}`; `failed → queued` (backoff) or
`→ dead` (attempts exhausted); `failed/dead → queued` on Retry.

**Pass:** panel reflects real state live; orphan reset on restart; Retry re-queues.

---

### A6 — Settings, migration 52, protocol 115 gate **[DET]**
**Proves:** keeper config persists/enables/disables; the janitor→keeper migration landed; the
daemon speaks protocol 115.

> **Numbering note (post-collapse onto `origin/main`):** the janitor→keeper rename is
> **migration 52** (main owns 49–51, incl. the workflow-engine tables), and it is
> `tableExists`-guarded: a clean rename on a DB still carrying the janitor table, a safe no-op
> on a DB already renamed. Protocol is **115**. Older drafts of this runbook said 51/114 — that
> was the pre-collapse stack.

**Steps:**
1. **Keeper compaction config (app):** Settings → Workspace-context keeper → pick Agent +
   Model → **Save**. Then verify, and test **Disable**:
   ```sh
   sqlite3 ~/.attn-dev/attn.db "SELECT value FROM settings WHERE key='workspace_keeper_compact'"
   # → {"agent":"claude","model":"…"}   (Disable button writes "")
   ```
2. **Migration 52:**
   ```sh
   sqlite3 ~/.attn-dev/attn.db "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%janitor%'"  # → empty
   sqlite3 ~/.attn-dev/attn.db "SELECT name FROM sqlite_master WHERE type='table' AND name='workspace_keeper_compact_backups'"  # → present
   sqlite3 ~/.attn-dev/attn.db "SELECT MAX(version) FROM schema_migrations"  # → 52
   ```
3. **Protocol 115:** the dev app (rebuilt in 0.1) connects cleanly; an *old* daemon would
   force a reconnect-version mismatch (frontend gate, `useDaemonSocket.ts`). Sanity:
   `grep -n "ProtocolVersion" internal/protocol/constants.go` → `"115"`; the live daemon's
   `initial_state.protocol_version` also reports `115`.

**Pass:** config round-trips + disables; no `janitor` table, keeper backups table present,
schema at 52; app connects at protocol 115.

---

## Group B — Agentic narration & compaction (real model spend)

> These spawn **real headless agents**. Keep `tail -f … daemon.log | grep …` (0.4) running.
> Default tiers: summarize = Claude **Haiku**, narrate = Claude **Sonnet**. Narration is
> always on (no enable step). Debounce is **2 min** after a session stop.

### B1 — Per-session digest (`summarize_session`) **[LLM]**
**Proves:** a finished session is digested to the raw tier by the cheap-tier agent.

**Steps:**
1. Launch a session **in a workspace** (dev profile), have the agent do a little real work
   (a few commands / a small edit), then let it **stop** (turn ends).
2. Wait ~2 min (debounce), watching the log for `summarize_session: session=… digest=…`.
3. Inspect:
```sh
cat ~/attn-notebook-dev/.attn/raw/sessions/<wsID>/<sessionID>.md
```

**Expected:** a digest file under the **owning workspace** bucket (a session with no workspace
lands in `sessions/_solo/`). Success gate is *file-is-ledger*: the file exists and changed.

**Pass:** digest file written under the correct `<wsID>/` bucket; log line present.

---

### B2 — Workspace journal narration (`narrate_workspace`) **[LLM]**
**Proves:** the strong-tier keeper composes the curated, dated journal entry for a live
workspace from the raw tier.

**Steps:** continue from B1 (digest exists, workspace still live). Within the debounce the
keeper also enqueues `narrate_workspace`. Watch for `narrate_workspace: workspace=… removal=false journal=…`, then:
```sh
cat ~/attn-notebook-dev/journal/$(date +%Y-%m-%d).md
```

**Expected:**
- One block per workspace per day, delimited by a full-line `<!-- attn:wsnarr:<wsID> -->`
  marker, narrating decisions/fights/what-shipped grounded in the digests — **not** raw
  machine text.
- The narrate agent reads only the **raw tier**, never the curated journal, so the journal
  stays editorial.

**Pass:** a grounded journal entry with the correct `wsnarr` marker; journal stays curated.

---

### B3 — Removal retrospective + project archive-on-close **[LLM]**
**Proves:** removing a workspace fires the **immediate** (ZeroDebounce) final retrospective
(daemon-guaranteed), and the keeper is **instructed** to file the linked project folder under
`archive/` (best-effort agent behavior — see the scope note below).

> **Scope (be honest in the verdict):** the daemon **guarantees** the removal snapshot
> (`.attn/raw/context-snapshots/<id>.md`) and the removal-retrospective journal block. The folder
> relocation is performed by the **agent**, driven by the prompt (a literal whole-line
> `grep -lFx 'resource: attn:workspace/<id>'` over `projects/*/index.md`, then `mv` to `archive/`
> with a dated suffix on collision; ambiguity or no match → do nothing). The daemon cannot verify
> the move. Verified once on disk: the linked `projects/<f>/` was moved to `archive/<f>/` with its
> backlink intact. Do not describe this as "automatically archived" — it is best-effort.

**Pre (to exercise archiving):** create a project folder linked to the workspace you'll remove:
```sh
mkdir -p ~/attn-notebook-dev/knowledge/projects/uat-demo
printf -- '---\ntype: project\nresource: attn:workspace/<wsID>\nsources:\n  - journal/%s.md\n---\n# UAT demo\n' "$(date +%Y-%m-%d)" \
  > ~/attn-notebook-dev/knowledge/projects/uat-demo/index.md
```

**Steps:** **remove that workspace** in the app. Watch for `narrate_workspace: … removal=true`.

**Expected:**
- A final retrospective block for the workspace is written/refreshed in today's journal
  immediately (it overrides any pending debounce).
- `knowledge/projects/uat-demo/` is **moved** to `knowledge/archive/uat-demo/` (whole-line
  `resource:` match). Anything already in `areas/` is untouched; nothing is deleted.

**Pass:** removal-pass journal entry present; project folder relocated to `archive/`.

---

### B4 — Keeper compaction (`compact_context`) + rollback **[LLM]**
**Proves:** oversized canonical context is compacted by the agent under the commit fence,
backed up, and reversible.

**Pre:** keeper configured (A6 step 1, not disabled).

**Steps:**
1. Push canonical context **past 12 KiB**:
   ```sh
   ATTN_PROFILE=dev ./attn workspace context show   # edit the file to >12 KiB of content
   ATTN_PROFILE=dev ./attn workspace context update
   ```
2. Run it **now** (skip the 10-min debounce):
   ```sh
   ATTN_PROFILE=dev ./attn workspace context compact
   ```
3. Observe:
   ```sh
   ATTN_PROFILE=dev ./attn workspace context status   # canonical content shrank, revision bumped
   sqlite3 ~/.attn-dev/attn.db \
     "SELECT workspace_id,source_revision,result_revision,agent,model FROM workspace_keeper_compact_backups ORDER BY created_at DESC LIMIT 3"
   ```
   Log: `keeper compact: workspace=… changed=true …`; canonical row's `updated_by_session_id=attn-keeper`.
4. **Rollback:** `ATTN_PROFILE=dev ./attn workspace context rollback` → pre-compaction content restored.

**Pass:** context compacted + validated + applied as `attn-keeper`; backup row written;
rollback restores the snapshot.

---

## Group C — Daily-cron narrate backstop

### C1 — Nightly per-workspace narrate, activity-gated **[TIME][LLM]**
**Proves:** an active long-lived workspace gets a daily entry even with no session stop, and
idle workspaces are skipped.

**Steps:**
1. Shorten the cron to every minute (GetSetting reads the DB directly, so no restart needed):
   ```sh
   sqlite3 ~/.attn-dev/attn.db "INSERT OR REPLACE INTO settings(key,value) VALUES('notebook.cron.frequency','* * * * *')"
   ```
2. Mark a **live** workspace active without ending a session — make a content-changing context
   write:
   ```sh
   ATTN_PROFILE=dev ./attn workspace context show   # edit + change content
   ATTN_PROFILE=dev ./attn workspace context update
   ```
3. Watch the cron:
   - First tick **records the anchor and does not fire** (no startup narrate):
     `cat ~/attn-notebook-dev/.attn/narrate/state.json` → `scheduled_from` set.
   - Next tick (≤1 min later) is due → drains the activity set → enqueues a daily narrate
     (`daily_pass=1`, relaxed success gate). Watch `daily narrate:` / `narrate_workspace:`.
4. Confirm a journal entry for the active workspace appears; an **untouched** workspace gets
   none (activity gate).
5. **Restore** the schedule:
   ```sh
   sqlite3 ~/.attn-dev/attn.db "INSERT OR REPLACE INTO settings(key,value) VALUES('notebook.cron.frequency','0 3 * * *')"
   ```

**Pass:** anchor recorded then advanced; active workspace narrated via the daily pass; idle
workspaces skipped; schedule restored.

---

## Group D — Knowledge base authoring & browser (chief / human surfaces)

### D1 — Author a grounded knowledge note **[DET]**
**Proves:** the knowledge base accepts open-`type` OKF notes nested under PARA and surfaces
them, and grounding is the stated rule.

**Steps:** as the chief (native tools) or by hand, create:
```sh
mkdir -p ~/attn-notebook-dev/knowledge/areas/terminal-rendering
printf -- '---\ntype: note\nsources:\n  - journal/%s.md\n---\n# Emoji cluster rendering\nRIS disables DEC 2027 …\n' "$(date +%Y-%m-%d)" \
  > ~/attn-notebook-dev/knowledge/areas/terminal-rendering/emoji-clusters.md
```
Open ⌘K → Browse the Notebook.

**Expected:** the note renders, grouped by its PARA top-level (`areas`), open `type:`
preserved byte-faithfully; the store does **not** reject the custom `type`.

**Pass:** note appears under the correct PARA group with frontmatter intact.

---

### D2 — Browser read/edit, external-edit watcher, send-to-chief **[DET]**
**Proves:** the in-app browser stays the live read/edit surface (the WS path the CLI removal
did not touch).

**Steps:**
1. **Read/edit:** open a note, **Edit**, save → hash-CAS write; edit the same file on disk
   meanwhile to force a **conflict** → Reload-from-disk / Overwrite choices appear.
2. **External-edit watcher:** edit any note on disk (`echo` a line into it) → the open browser
   refreshes live (`origin=external`); delete the open note on disk → the view clears.
3. **Send-to-chief:** highlight text in a note → **Send to chief** → the selection (blockquoted,
   with a backlink) is appended to `~/attn-notebook-dev/inbox.md`; a live idle chief gets a
   bounded doorbell nudge; with no chief, it still lands in `inbox.md`.

**Pass:** edit conflict reconciles without clobber; external edits/deletes reflect live;
selection reaches `inbox.md` (+ nudge when a chief is live).

---

## Teardown
```sh
# restore any test settings you changed (cron frequency in C1; keeper config if you disabled it)
sqlite3 ~/.attn-dev/attn.db "SELECT key,value FROM settings WHERE key LIKE 'notebook.%' OR key='workspace_keeper_compact'"
# optional: wipe the dev notebook (0.5). When done with the epic, reinstall prod: make
```

---

## Coverage map (scenario → PR)
| Scenario | Exercises | Primary PR(s) |
| --- | --- | --- |
| A1 | PARA/OKF scaffold | #348 |
| A2 | chief native-file guidance / doorbell | #356 |
| A3 | dispatch capture → raw tier | #347/#351 |
| A4 | context-snapshot floor | #351 |
| A5 | durable task engine + recovery + panel | #347, #355 |
| A6 | keeper rename/migration 52, settings, protocol 115 | #350, #355, #359 |
| B1 | summarize_session digest | #352 |
| B2 | narrate_workspace journal entry | #352 |
| B3 | removal retrospective + archive-on-close | #352, #354, #356, #358 (startup-reap wiring) |
| B4 | compact_context + rollback | #351, #349 (native-tools) |
| C1 | daily-cron narrate backstop | #353 |
| D1 | knowledge-base authoring (open type/PARA) | #348, #355 |
| D2 | browser read/edit/watcher/send-to-chief | #355 |
