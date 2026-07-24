# Notebook Schema

This is the canonical specification for attn Notebook knowledge-base files. The chief of staff, the keeper, the notebook UI, and human editors all follow this contract. If you're writing or editing knowledge notes — by hand, through the chief, or through the in-app editor — this is the reference.

Vocabulary — *the keeper*, *the chief of staff*, *dispatch*, *the raw tier*, *PARA*, *OKF* — is defined in [glossary.md](glossary.md).

---

**Contents:** [Concepts](#concepts) · [Frontmatter](#frontmatter) · [Markdown Body](#markdown-body) · [Section Conventions by Type](#section-conventions-by-type) · [Linking](#linking) · [File Naming](#file-naming) · [Lifecycle and Placement](#lifecycle-and-placement) · [Reserved Files](#reserved-files) · [Parsing Rules](#parsing-rules) · [Examples](#examples)

---

## Concepts

The **Notebook** is a profile-wide, filesystem-canonical markdown bundle. Defaults to `~/attn-notebook` for the default profile and `~/attn-notebook-<profile>` for named profiles (see [docs/profiles.md](../profiles.md)). It has two products:

- **The journal** (`journal/<date>.md`) — a dated narrative of what was done, maintained by the keeper and the chief. Not covered by this schema (journals are append-only prose with `type: journal` frontmatter).
- **The knowledge base** (`knowledge/`) — distilled, timeless knowledge maintained by the chief of staff and the user. **This schema covers knowledge-base notes.**

A **Note** is a markdown file under `knowledge/` with YAML frontmatter. Notes are organized along two orthogonal axes:

1. **Directory axis (PARA)** — the physical location: `projects/`, `areas/`, `resources/`, `archive/`. This is organizational, not semantic.
2. **Frontmatter axis (OKF)** — the `type` field declares what kind of knowledge the note carries. This is semantic, not organizational.

```
knowledge/
├── index.md                    # Knowledge root (reserved, no frontmatter)
├── projects/                   # Bounded efforts with an end
│   ├── index.md                # Directory index (reserved)
│   └── auth-rewrite/           # One folder per project/epic
│       ├── index.md            # Project root (type: project)
│       └── token-storage.md    # A note within the project
├── areas/                      # Ongoing responsibilities, no end
│   ├── index.md
│   ├── auth.md                 # Area note
│   └── observability.md
├── resources/                  # Reference material, consumed as-is
│   └── index.md
└── archive/                    # Finished or inactive
    └── index.md
```

**Path = identity.** A note's notebook-relative path (`knowledge/areas/auth.md`) is its stable address, used in links and source references. Moving a note means moving the file and updating all links that reference it.

---

## Frontmatter

### Required Fields

```yaml
---
type: note
---
```

Every non-reserved knowledge note must carry a `type`. Reserved files (`index.md`, `log.md`, `inbox.md`) carry no frontmatter.

### `type`

An open, author-chosen string. The store does not enforce a closed set — the vocabulary is intentionally extensible. Conventional values:

| Value | Meaning | When to use | Typical body sections |
|-------|---------|-------------|----------------------|
| `note` | General knowledge note | Default. Use when no other type fits. | Freeform |
| `project` | Project root | The `index.md` of a `projects/<slug>/` folder | Freeform |
| `area` | Area of responsibility | Ongoing domain broader than one component — if the note groups several systems or cuts across services, prefer `area` | Freeform, optionally Contracts |
| `system` | System/service description | A running component you could draw a box around on an architecture diagram, with its own contracts and behaviors | Technical Details, Contracts, Behaviors |
| `decision` | Architectural decision record | A decision that shapes future work — captures the context, alternatives, and chosen path | Context, Alternatives, Decision |
| `runbook` | Operational procedure | How to do X when Y happens — procedural, step-oriented | Procedural H2 sections |
| `operating-principle` | Team operating principle | A durable way-of-working or behavioral norm | Title and introduction only |
| `reference` | Reference material | Specifications, API docs, external resources | Freeform |

For backward compatibility, the store also reads a legacy `kind` field as a fallback when `type` is absent. New notes must always use `type`.

### Optional Fields

Fields fall into two tiers based on implementation status:

**Fully surfaced** — accessor in Document, Entry field, protocol representation, UI:

| Field | Type | Meaning |
|-------|------|---------|
| `summary` | string | One-line description (50-100 chars). Displayed in the notebook browser's file tree. Distinct from the body introduction (see [Markdown Body](#markdown-body)). |
| `updated` | date string | Last meaningful update (ISO 8601 date). The store also tracks file mtime as fallback. |

**Convention-only** — round-trip preserved via the raw-frontmatter mechanism, but not parsed, indexed, or exposed through List/protocol/UI. Tool authors must read individual documents and inspect the `Frontmatter` map directly:

| Field | Type | Meaning |
|-------|------|---------|
| `sources` | list of strings | Grounding references. Chief-authored notes must carry at least one. User-authored notes may carry sources at the user's discretion. See [Sources](#sources). |
| `resource` | string | A resource URI linking this note to an attn entity (e.g. `attn:workspace/<id>`). |
| `title` | string | Informational / editor convenience. **NOT the canonical title** — the canonical title is always the first `# ` heading in the body (per `Document.Title()`). Existing notes may carry it for external-tool compatibility (e.g., Obsidian). |
| `created` | date string | Original creation date (ISO 8601). Distinct from `updated`. Informational only. |
| `paths` | list of strings | Code-location prefixes this note covers, relative to the repository root (e.g. `internal/classifier/`, `app/src/hooks/useDaemonSocket.ts`). Enables future automation to flag stale notes when relevant code changes. |

Unknown frontmatter keys are preserved on round-trip. Fields written by Obsidian, external sync tools, or the user survive an attn rewrite untouched.

#### Future Fields (not yet implemented)

These fields are documented as conventions for future implementation. They survive on disk via round-trip preservation but have **no accessor, no Entry field, no protocol representation, and no code that reads them**.

| Field | Type | Intended meaning |
|-------|------|-----------------|
| `parent` | string | Filename of this note's logical parent within the same directory. Would create a semantic tree over flat files — hierarchy in frontmatter, not in the filesystem. Changing `parent` would move a note in the hierarchy without renaming the file. |
| `state` | string | Lifecycle state: `active`, `planned`, `in-progress`, `deprecated`. By convention, omitted state is treated as `active`. |

Implementation requires changes to: `document.go` (accessor), `notebook.go` (Entry struct), `store.go` (List), `protocol/schema/main.tsp` (NotebookEntry), and the frontend tree renderer.

### Sources

Grounding is a hard rule for chief-authored knowledge. A chief-maintained note should carry resolvable sources, not paraphrase alone. User-authored notes may carry sources at the user's discretion.

| Format | Example | Meaning |
|--------|---------|---------|
| Journal anchor | `/journal/2026-06-20.md#auth-migration` | Points to a specific section in a journal entry |
| Dispatch reference | `dispatch:abc123` | A chief-of-staff dispatch outcome |
| URL | `https://github.com/user/attn/pull/350` | An external document, PR, or resource |
| Note reference | `/knowledge/areas/auth.md` | Another knowledge note |

---

## Markdown Body

Every note starts with a **title** (H1 heading) and an optional **introduction** (prose between the title and the first H2). These are the only universal elements. Everything after the introduction is opt-in H2 sections — authors are free to use any sections that fit the content type.

### Title and Introduction

```markdown
# Auth Token Storage

The auth service stores refresh tokens in an encrypted SQLite database on the
device, rotated on each use. Access tokens are held in memory only and expire
after 30 minutes.
```

The H1 heading is the note's display title (`Document.Title()` reads this; returns `""` when absent, and callers fall back to the filename as display name). The introduction is 1-3 paragraphs describing the **observable behavior or known fact** — what is true, not how the code implements it.

The frontmatter `summary` is a one-line description for listing views (50-100 chars). The body introduction provides fuller orientation. Both should exist and be consistent, but the introduction may be longer.

### Opt-In Body Sections

Use these when they fit. Not every note needs any of them.

#### Technical Details

```markdown
## Technical Details

Tokens are stored in `~/.config/app/tokens.db` using SQLite's SEE encryption
extension. The `TokenStore` class in `internal/auth/store.go` handles rotation.
```

Implementation specifics: code paths, config files, service endpoints, data formats. Keeps the introduction clean while giving engineers technical context. **Use when:** the note describes a system or subsystem with implementation worth documenting.

#### Contracts

```markdown
## Contracts

| From | To | Description |
|---|---|---|
| auth-service | token-db | Encrypted refresh token via SQLite write, rotated on each use |
| auth-service | client-app | Access token (JWT, 30-min TTL) returned from `POST /auth/refresh` |
| client-app | auth-service | Encrypted refresh token in request body of `POST /auth/refresh` |
```

Contracts define system boundaries — what one component expects from another.

**Contract quality rules:**

- **Package-level or component-level only.** From and To should be packages, subsystems, or external systems — not individual types or functions within a package.
- **Self-contained rows.** Each row must be readable in isolation. Never write "Same as above."
- **Name the mechanism.** Each Description must state what is exchanged AND how: the integration type (function call, SQL query, websocket event, HTTP endpoint, etc.) and enough detail to locate the call site.
- **Consistent naming.** Use canonical names across all notes.

**Use when:** the note describes a system or subsystem with boundaries worth mapping.

#### Behaviors

```markdown
## Behaviors

- [ ] Token rotation happens on every refresh call
- [x] Expired access tokens return 401 (verified: `go test ./internal/auth/...`)
- [ ] Concurrent refresh calls are serialized (single-flight)
- [ ] Missing token DB triggers first-run setup flow
```

A checklist of expected observable behaviors. Each line is a checkbox with a short description.

**Behavior rules:**

- Describe what **should happen**, not how the code does it.
- Each behavior should be independently testable.
- **Verification notes are required when checking a behavior** — include the method: test name, dispatch ID, or manual observation in parentheses.
- An unchecked behavior is a known gap — not a failure, but something that has not been verified.
- **When modifying code that could invalidate a checked behavior, uncheck it and remove the verification note.** The behavior reverts to unverified. Do not add dates — the `updated` frontmatter field tracks recency.

**Use when:** the note describes a system with behaviors worth enumerating and tracking.

---

## Section Conventions by Type

Not prescriptive — these are common patterns. Authors may deviate when the content calls for it.

| Type | Common body sections |
|------|---------------------|
| `system` | Introduction → Technical Details → Contracts → Behaviors |
| `area` | Introduction → optionally Contracts (cross-system boundaries) |
| `runbook` | Introduction → procedural H2 sections (Steps, Triage, References) |
| `decision` | Introduction → Context → Alternatives → Decision |
| `operating-principle` | Introduction only (title and prose) |
| `note`, `reference`, `project` | Freeform — whatever fits |

---

## Linking

Root-absolute markdown links, not wikilinks:

```markdown
[auth area](/knowledge/areas/auth.md)
[journal entry](/journal/2026-06-20.md)
```

Within the same directory, relative links are acceptable:

```markdown
[token storage](auth-tokens.md)
```

Keep relationship kind in prose, not in link syntax:

```markdown
This decision supersedes [the old token format](/knowledge/archive/old-tokens.md).
```

---

## File Naming

- **Convention:** kebab-case, lowercase `a-z`, `0-9`, hyphens, always `.md`.
- **Code enforcement** (`CleanPath`): `.md` extension required, no dotfiles/dotdirs, no empty segments, no parent-directory escapes.
- Filenames must be unique within their directory.
- Renaming a file requires updating all links and any `parent` references that point to it.

---

## Lifecycle and Placement

### When to create, update, or archive

1. **Orient first.** Before creating a new note, check if one for this subsystem/topic already exists. Prefer updating over creating.
2. **Update** existing notes when new grounded information changes the understanding. Update the `updated` field and append new entries to `sources`.
3. **The keeper auto-archives** project folders when the linked workspace closes (the one whose `index.md` carries `resource: attn:workspace/<id>`).
4. **The chief may promote** durable project knowledge into `areas/` when it becomes an ongoing responsibility.
5. **Prefer archive over deletion.** Move finished or deprecated notes to `archive/` rather than removing them.

### PARA placement heuristic

| Directory | Rule of thumb | Examples |
|-----------|--------------|---------|
| `projects/` | Time-bounded with a clear "done." One folder per project or epic. | `auth-rewrite/`, `mobile-release-9.2/` |
| `areas/` | Ongoing domain you would re-read and revise next quarter. Subsystem descriptions, runbooks, operating principles. | `auth.md`, `observability.md`, `ais-goalie.md` |
| `resources/` | Static reference material consumed but rarely revised by the author. | API specs, vendor docs, compliance references |
| `archive/` | Finished or deprecated. Kept as a record. | Completed projects, superseded decisions |

**When in doubt:** will you actively maintain it (`areas/`) or consume it as-is (`resources/`)?

---

## Reserved Files

These files have special meaning and carry no OKF frontmatter:

| File | Purpose |
|------|---------|
| `index.md` | Bundle root / directory index. Present at the notebook root and in each directory. |
| `log.md` | Change history (newest first). |
| `inbox.md` | Chief-of-staff inbox. Created on first "send to chief." |
| `knowledge/index.md` | Knowledge-base root. Documents the PARA layout and grounding rules. |
| `knowledge/{projects,areas,resources,archive}/index.md` | PARA directory indexes. |

---

## Parsing Rules

For tools and agents that need to parse knowledge notes programmatically:

1. **Frontmatter**: Everything between the first `---` and the second `---`. Parse as YAML. Unknown keys are preserved on round-trip.
2. **Title**: First `# ` heading in the body (`Document.Title()`). Returns empty string when absent; callers (the store, the UI) fall back to the filename as the display name. Subsequent `# ` headings are body content, not titles.
3. **Introduction**: All content between the title and the first `## ` heading. Strip the H1.
4. **Sections**: Split on `## ` headings. Each section is identified by its heading text. Unknown H2 sections are preserved and ignored by parsers.
5. **Contracts table**: Under `## Contracts`, find the markdown table. Parse rows by `|`, skip header and separator rows. Columns: From, To, Description.
6. **Behaviors checklist**: Under `## Behaviors`, each line matching `- [ ] ` or `- [x] ` is a behavior. Text in trailing `(parentheses)` is the verification note.
7. **Links**: Root-absolute links matching `/knowledge/...` or `/journal/...` are internal references. Relative links are resolved within the note's directory.

---

## Examples

### A minimal note (no structured body sections)

```markdown
---
type: operating-principle
summary: Decisions live with the person closest to the work.
sources:
  - /journal/2026-06-18.md#decision-ownership
---

# Decision ownership lives at the edge

When a task is delegated, the receiving agent owns the implementation decisions
within the brief's scope. Escalate to the chief only when the decision would
change the brief's scope, affect other workspaces, or carry irreversible
consequences. The goal is speed with accountability, not consensus.
```

### A full system note (all structured body sections)

```markdown
---
type: system
summary: The session classifier determines when an AI coding session has finished its work.
updated: 2026-06-24
sources:
  - /journal/2026-06-15.md#classifier-rewrite
  - dispatch:d-789
  - https://github.com/user/attn/pull/350
paths:
  - internal/classifier/
---

# Session Classifier

The classifier runs when a session stops producing output. It reads the
terminal's last screen and the session's recent transcript to determine
whether the session is waiting for user input, has completed its task, or
needs approval. The result drives the session's state transition and the
sidebar badge.

## Technical Details

The classifier is implemented in `internal/classifier/`. It uses a fast
model (Haiku) with a structured prompt that receives the last N lines of
terminal output and the most recent assistant message from the transcript.
The prompt is tuned to distinguish "done" from "waiting for input" — the
hardest boundary case.

The timestamp protection pattern is critical: capture the timestamp before
classification starts and use `UpdateStateWithTimestamp()` so stale results
cannot overwrite fresher runtime state.

## Contracts

| From | To | Description |
|---|---|---|
| classifier | daemon | Classification result via function return: one of `idle`, `waiting_input`, `pending_approval` with confidence score |
| daemon | frontend | Session state update via `session_update` websocket event, drives sidebar badge |
| daemon | store | `UpdateStateWithTimestamp()` SQL call — stale classifier results rejected if timestamp is older |

## Behaviors

- [x] Sessions that print "done" or similar are classified as `idle` (verified: `classifier_test.go TestDoneClassification`)
- [x] Sessions waiting at a prompt are classified as `waiting_input` (verified: `classifier_test.go TestWaitingInput`)
- [ ] Approval prompts (Y/n) are classified as `pending_approval`
- [ ] Long-running sessions (5+ min) that finish get `needs_review_after_long_run` flag
- [ ] Stale classification results never overwrite fresher state (timestamp guard)
```
