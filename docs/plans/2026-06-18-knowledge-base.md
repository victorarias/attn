# Knowledge base: reframe "memory notes" into a chief-curated, PARA/OKF knowledge base

Status: in progress (single PR on `feat/kill-dreaming`; the PR1+PR2 split below was collapsed into one)
Date: 2026-06-18

## Why

The Notebook has two layers: a dated **journal** and distilled **memory notes**.
The journal got a reliable automated writer (the keeper narrates each workspace
into it). Memory notes never did — their only intended writers were "the chief,
by judgment" (unreliable) and the dreaming promote pass (removed in #343). So
`memory/` is, in practice, empty scaffolding.

We are reframing the memory layer into a **knowledge base**: a space adjacent to
the journal that the **chief of staff** maintains in character (and the user can
use for their own notes). It is structured prescriptively — **PARA** on the
directory axis, **OKF** (Open Knowledge Format) on the frontmatter axis — and the
chief edits it **directly** with native file tools, the same way the keeper
already writes the journal. It is explicitly **not** called "memory".

This is not an agent-recall index. It is the durable, human-and-chief-owned
knowledge layer of the work journal.

## Vocabulary

- **Knowledge base** — the `knowledge/` subtree of the Notebook. Replaces the
  "memory" concept and the `memory/` directory. Maintained by the chief of staff
  and the user.
- **PARA** (Tiago Forte) — the directory axis: `projects/` (bounded efforts with
  an end ≈ an attn workspace/epic), `areas/` (ongoing responsibilities /
  subsystems, no end), `resources/` (reference material), `archive/` (inactive
  items). Knowledge is nested under these.
- **OKF** (Open Knowledge Format v0.1, GoogleCloudPlatform/knowledge-catalog) —
  the frontmatter axis: vendor-neutral markdown + YAML, whose one required key is
  `type` (an open, author-chosen string). Reserved files `index.md` (per-level
  nav) and `log.md` (changelog) carry no frontmatter. PARA (directory) and OKF
  (`type`) are **orthogonal**.
- **The journal** — unchanged: `journal/<date>.md`, the dated narrative, written
  by the keeper per-workspace, the chief at altitude, and the human.

## End-state shape (prescriptive)

The whole Notebook is **one OKF bundle**, nestable as a subfolder of a larger
personal vault (e.g. `~/exo/attn`). attn is prescriptive about its skeleton:

```
<root>/                         # the OKF bundle root (~/attn-notebook[-profile])
  index.md                      # reserved: bundle guide (OKF root, attn-authored)
  log.md                        # reserved: change history
  inbox.md                      # reserved: chief inbox (send-to-chief target)
  journal/
    <date>.md                   # dated narrative (type: journal)
  knowledge/
    index.md                    # reserved: knowledge-base nav
    projects/   index.md        # bounded efforts (one folder per project/epic)
    areas/      index.md        # ongoing responsibilities / subsystems
    resources/  index.md        # reference material
    archive/    index.md        # inactive / finished items
  .attn/                        # machine state (tasks, raw tier, anchors) — never surfaced
```

- Knowledge notes are **nested under** `projects/<slug>/` and `areas/<slug>/`
  (and live flat or nested under `resources/`, `archive/`).
- Every non-reserved `.md` carries OKF frontmatter with a non-empty `type:`
  (author/chief-chosen). attn no longer enforces a closed `journal|memory` enum.
- A **project folder** links to the workspace that produced it via OKF
  `resource: attn:workspace/<id>` in its `index.md`. This is the hook the keeper
  uses to archive it on workspace close.

## Design decisions

1. **`memory` → `knowledge`** everywhere: directory, scaffold, frontmatter
   semantics, protocol field, frontend grouping, glossary, guidance. The word
   "memory" is retired from the domain.
2. **`kind` → `type`** (OKF). The frontmatter field renames; validation relaxes
   from a closed enum to "present `type` must be non-empty". **Read-compat:** the
   document accessor reads `type`, falling back to `kind`, so any stray
   externally-authored `kind:` still resolves (byte-faithful round-trip means we
   never rewrite a user's key silently). attn writes `type:`.
3. **PARA directories** replace `memory/{decisions,gotchas,domain}`. attn
   scaffolds `knowledge/{projects,areas,resources,archive}/` each with an
   `index.md`, plus `knowledge/index.md`.
4. **Agents edit knowledge + journal files directly** with native tools (Read/
   Write/Edit/Bash). The "write through the daemon, never edit files directly"
   mandate is **dropped for agents** — it was an invariant about attn's own
   components (CLI/frontend), and the keeper already writes the journal natively.
   The frontend keeps writing over WebSocket (it has no filesystem); agent edits
   surface through the existing fsnotify watcher as `origin=external`.
5. **Remove the `attn notebook` user CLI** (init/show/list/journal/memory/guide/
   tasks). The frontend uses WS, agents use native tools, so the user-facing
   subcommands have no essential consumer. The browser, send-to-chief, and task
   panel (all WS) are untouched.
6. **Keep `notebook_guide` as launcher plumbing.** The agent-launch wrapper
   (`resolveChiefNotebookRoot`) and the live-promotion doorbell still need to know
   *is-chief* + *root*; that internal socket command stays. Only the user-facing
   `attn notebook guide` subcommand is removed.
7. **Doorbell goes file-based.** `activateNotebookGuidanceLive` and the inbox
   nudge can no longer type `` `attn notebook guide` `` / `` `attn notebook show` ``.
   They point the agent at native paths instead (`read <root>/index.md`,
   `read <root>/inbox.md`). Full operating guidance keeps flowing into the system
   prompt at launch via `hooks.NotebookGuidance` (unchanged mechanism).
8. **Roleplay chief guidance.** `hooks.NotebookGuidance` is rewritten as a
   chief-of-staff roleplay prompt: you are the chief; the knowledge base is your
   space; maintain it in PARA/OKF; promote durable knowledge from finishing
   projects into areas; edit files directly. Non-deterministic but "enough".
9. **Keeper archives on close.** The keeper's removal-pass narrate prompt gains:
   after the final retrospective, if a `knowledge/projects/<slug>/` is linked to
   this closing workspace (`resource: attn:workspace/<id>`), move it to
   `knowledge/archive/<slug>/`. Mechanical, reversible tidy-up that fits the
   keeper persona; the chief retains higher-judgment promotion/curation.

## Stack (base `feat/kill-dreaming`)

> **Collapsed:** PR1 and PR2 below were merged into a single PR (`feat/remove-notebook-cli`)
> so the agent guidance never ships an interim state pointing at the old `/memory`
> paths. The PR1/PR2 breakdown is kept as the record of what that combined PR contains.
> PR3 (keeper archiving + remaining docs) stays a separate follow-up.

### PR1 — `feat/knowledge-base`: data-model reframe (mechanical, all layers)
Rename `memory`→`knowledge`, `memory/{decisions,gotchas,domain}`→PARA, `kind`→
`type` (OKF, open + non-empty, read both), across:
- `internal/notebook/` — `DirMemory`→`DirKnowledge`; `memorySubdirs`→PARA;
  scaffold dirs + per-level `index.md`; `indexTemplate`/`memoryIndexTemplate`;
  `ValidKind`→open-`type` validation; `Document.Kind()`→`Type()` (read `type`||`kind`);
  `newJournalDoc` writes `type: journal`; `KindMemory` removed, `KindJournal`
  kept as the journal `type` value; tests (~44 path + ~24 frontmatter fixtures).
- `internal/protocol/` — `NotebookEntry.kind`→`type`; regen (`rm -rf tsp-output`
  first); `ProtocolVersion` 111→112; frontend `PROTOCOL_VERSION` mirror.
- `internal/daemon/` — `notebookEntriesToProtocol` (`e.Kind`→`e.Type`); any
  `memory` path literals; tests.
- `app/src/` — `NotebookEntry.kind`→`type`; `groupEntries`/`GROUP_ORDER`/
  `PREFERRED_FIRST` regroup by PARA top-level under `knowledge/`; `data-kind`→
  `data-type`; labels (`Durable memory`→knowledge); command-palette copy; tests.
- Docs: glossary + `docs/notebook-processes.md` path/term updates; this plan doc.
- Protocol bump (112). CLI + guidance keep their current form but point at
  `/knowledge/...` paths (the CLI takes explicit paths, so it still functions).

### PR2 — `feat/remove-notebook-cli`: remove the `attn notebook` user CLI + file-based doorbell
- `cmd/attn/main.go` — delete `case "notebook"`, `runNotebook*`, help, parsers,
  `printNotebookTasks`, `formatTaskTime` (orphaned). Keep `resolveChiefNotebookRoot`
  (launcher) and its `notebookGuideClient`.
- `internal/client/client.go` — delete `NotebookInit/Read/List/AppendJournal/
  Write/Tasks` (CLI-only). **Keep `NotebookGuide`** (launcher).
- `internal/daemon/` — delete the orphaned unix-socket handlers + dispatch cases
  (`handleNotebookInit/List/Read/Write/AppendJournal`, `handleNotebookTaskList`)
  and any handler with no remaining caller. Keep all `*WSResult` handlers
  (frontend) and `handleNotebookGuide` (launcher).
- `internal/protocol/` — remove CLI-exclusive messages (`NotebookInitMessage`/
  `NotebookInitResult`, `NotebookAppendJournalMessage`) + Cmd constants +
  dispatch. Keep shared messages used by WS (List/Read/Write/Backlinks/
  SendToChief/TaskList/TaskRetry) and `NotebookGuide`. `ProtocolVersion` 112→113.
- Doorbell: rewrite `notebookActivationPrompt` and `chiefInboxNudgePrompt` to
  native-file pointers; update `notebook_test.go` doorbell assertions and
  `main_test.go`.
- `internal/hooks/hooks.go` — rewrite `NotebookGuidance` (the launch injection)
  here, not in PR3: removing the CLI means the guidance can no longer tell the
  chief to run `attn notebook …`, so the roleplay rewrite (knowledge base, PARA,
  OKF `type`, edit-directly with native tools, promote→areas, drop "write through
  daemon") is the coherent completion of the removal. Rewrite `hooks_test.go`
  accordingly. The narration-prompt archiving and skill/glossary docs stay in PR3.

### PR3 — `feat/chief-roleplay-guidance`: keeper archiving + docs
(The `hooks.NotebookGuidance` roleplay rewrite moved into PR2 — it is the coherent
completion of removing the CLI, and doing it once avoids rewriting the same
function twice across the stack. The `attn_skill/references/notebook.md` rewrite
also moved into PR2: the rewritten guidance routes the chief to "load the attn
skill's notebook reference," so that reference cannot keep pointing at the removed
CLI. `chief-of-staff.md` has no notebook-CLI references and stays in PR3.)
- `internal/daemon/notebook_narration_prompts.go` — add the removal-pass archive
  instruction to `narrateWorkspacePromptBrief` (move the workspace's linked
  project folder to `archive/`).
- Skill refs (`internal/agent/attn_skill/references/notebook.md`,
  `chief-of-staff.md`), glossary, CHANGELOG.

## Verification (per PR)
- `go build ./... && go vet ./...`; scoped `go test` for touched packages;
  `go test ./internal/daemon -race -run <scoped>` (avoid the TestGitStatusScheduler
  data race); `make generate-types` + `tsc`/vitest for protocol/frontend PRs.
- An adversarial multi-lens review workflow over each PR's diff before merge
  (correctness, missed references, protocol-version completeness, design fidelity).
- Owner-machine `make install` after each protocol bump (daemon reconnect; needs
  keychain — cannot run in sandbox).

## Open decisions (Victor's call)
- **Live-promotion guidance parity.** *Implemented as the default:* PR2's doorbell
  points a freshly-promoted chief at `<root>/index.md` (structure) while the full
  roleplay guidance arrives in the system prompt only at launch. This is a small
  regression from the old behavior, where the live doorbell ran `attn notebook
  guide` and so pulled the *full* guidance text. If you want live-promotion parity
  back, the daemon can write `hooks.NotebookGuidance` into a reserved file at
  promotion and point the doorbell there — say the word and I'll add it.
- **`index.md` ownership.** Proposed: attn authors the bundle/knowledge `index.md`
  files as the prescriptive guide; the user's content lives in PARA subdirs.
