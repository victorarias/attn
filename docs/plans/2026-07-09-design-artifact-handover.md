# Ticket handover through Notebook files

Status: implemented and verified; ready for review.

## Why / Alignment

The handover keeps a durable plan alive after its producing session stops so the
chief can read it, delegate from it, and keep it current until the work is done.
The filesystem is the source of truth: current artifacts are ordinary Markdown
files in `tickets/<ticket-id>/`, while the ticket carries coordination, decision
context, and the durable handover receipt.

This implementation covers the complete vertical slice in one branch: atomic
multi-file handover with optional state/comment, filesystem-derived ticket reads,
ticket-detail actions, role-specific agent guidance, and isolated live-app proof.
Existing hidden attachment data remains in place and is not part of current reads.

## Decision

A delegated agent hands a durable plan to its chief with `attn ticket
handover`. The command copies one or more Markdown files into the ticket's
visible Notebook directory, optionally changes ticket state, records the
handover, notifies the chief, and returns the canonical paths.

After handover, those paths are ordinary Notebook files. Humans and agents use
their normal file tools and attn's Notebook editor to read and maintain them.
The ticket remains the coordination record around the files: it carries work
state, delegation association, discussion, and handover history.

## Architecture

- A chief delegation creates a durable ticket bound to the delegated session
  (`internal/daemon/delegate.go:436-457`,
  `internal/daemon/delegate_ticket.go:33-52`).
- Ticket events and role-owned unread cursors deliver durable activity to the
  chief (`internal/store/ticket_events.go`, `internal/daemon/ticket_read.go`,
  `internal/daemon/ticket_notify.go`).
- `ticket attach` validates and stages one or more Markdown sources, installs
  them into the ticket's visible Notebook directory, and commits the attachment
  event and optional state change as one recoverable operation
  (`internal/daemon/ticket_attach.go`, `internal/store/tickets.go`).
- Full ticket reads enumerate current files directly from
  `tickets/<ticket-id>/`; the board remains a lightweight summary
  (`internal/daemon/ticket_artifacts.go`, `internal/daemon/ticket_board.go`).
- Ticket detail supports attach, open, copy-path, rename, and delete actions,
  while `ticket show` exposes the same filesystem-derived paths
  (`app/src/components/TicketDetailPanel.tsx`, `cmd/attn/main.go`).
- Built-in and plugin-provided agents receive a shared delegated initial prompt
  and the `ATTN_WRAPPER_PATH` environment needed to invoke the CLI
  (`internal/daemon/delegate.go:401-406`, `474-523`,
  `internal/pty/manager.go:533-569`).

The implementation uses the existing ticket, event, notification, and Notebook
paths.

## Product contract

### Current artifacts come from the filesystem

Each ticket has a visible directory:

```text
<notebook>/tickets/<ticket-id>/
```

Every regular Markdown file directly inside that directory is a current ticket
artifact. For example:

```text
<notebook>/tickets/opencode-plugin-design/
  design.md
  rollout.md
  open-questions.md
```

Ticket reads enumerate this directory at read time, sort the results by file
name, and return each Notebook-relative and absolute path. Hidden files,
directories, symbolic links, and non-Markdown files are excluded from the
artifact list.

This enumeration is the single source of truth:

- creating a Markdown file makes it visible on the next read;
- editing preserves its identity and immediately changes its contents;
- renaming replaces the old path with the new path on the next read;
- deleting removes it from the next read.

The event log supplies handover and discussion history. Directory enumeration
supplies the current file index.

### Hand over one or several files

```sh
"$ATTN_WRAPPER_PATH" ticket attach \
  --file docs/plans/design.md \
  --file docs/plans/rollout.md \
  --state ready_for_review \
  --comment "The plan and its decision context are ready for the chief."
```

The command:

1. resolves the caller's bound ticket, or an explicitly supplied ticket ID;
2. validates every source before changing the ticket;
3. copies the files into `<notebook>/tickets/<ticket-id>/`;
4. records one `attach_submitted` ticket event containing the submitted file
   names and decision-context comment;
5. applies the optional state transition;
6. notifies ticket participants; and
7. returns a receipt containing the ticket ID, canonical paths, event sequence,
   and resulting state.

`--file` is repeatable. `--state` accepts the existing ticket states. `--comment`
is optional and supplies the short coordination context; the full reasoning
lives in the handed-over Markdown.

The chief may pass a ticket ID to hand over a file to another ticket, matching
the authorization model used by ticket comments and status changes.

The destination name is the source basename. A destination containing the same
bytes is accepted as an idempotent retry. A destination containing different
bytes is preserved and the command asks the caller to choose another name.

### Maintain handed-over files

The receipt tells the producer which files became canonical. Subsequent work
targets those returned paths:

```sh
# The plan changed while work remains active.
"$ATTN_WRAPPER_PATH" ticket status in_progress \
  --comment "Updated design.md with the chosen storage model."

# The state remains unchanged, or another ticket is being updated.
"$ATTN_WRAPPER_PATH" ticket comment <ticket-id> \
  -m "Renamed rollout.md to implementation.md and updated its sequence."
```

These reports let the chief react to meaningful changes while ordinary file
edits remain ordinary file edits.

### Chief consumption

The chief reads the ticket before acting on its artifacts. `ticket show` and the
ticket detail UI return the paths currently present in the directory, so a
follow-on delegation receives current paths rather than paths retained from an
earlier event.

The stable ticket association lets the chief:

- open the complete plan in attn or any Markdown editor;
- discuss decisions on the ticket;
- pass canonical paths to follow-on agents;
- keep the plan current throughout implementation; and
- recover the same state after a restart.

## Delivery semantics

A successful attachment means all submitted files are present at the returned
paths, the `attach_submitted` event is durable, the requested state is
durable, and the receipt identifies all three. Notification delivery may be
repeated; ticket reads and the attachment receipt remain authoritative.

The command uses a deterministic fingerprint of the ticket ID, destination
names, file contents, requested state, and comment. The event stores that key.
A retry with the same fingerprint and matching destination contents returns the
existing receipt. This gives the operation idempotent recovery while the
directory remains the artifact index.

Filesystem writes are staged before becoming visible. The daemon holds the
ticket operation lock while it installs the staged files and commits the event
and optional state change. If an operation stops before returning success, the
caller retries the same command. Matching staged or installed files are reused;
the durable fingerprint prevents a second event after the database commit.

## Failure behavior

| Case | Observable result and recovery |
|---|---|
| A source is missing or is not Markdown | Validation fails before the ticket changes. |
| A destination has different contents | Existing content is preserved; the caller chooses another filename. |
| A copy fails | Staged output is removed and the ticket remains unchanged. |
| The daemon stops after installing files but before recording the event | The command has not succeeded; retry reuses matching files and completes the handover. |
| The response is lost after commit | Retry finds the fingerprint and returns the existing receipt. |
| The chief is inactive or restarts | The role-owned unread event remains discoverable, and the ticket directory supplies the current files. |
| A file is edited, renamed, or deleted | The next ticket read reflects the directory immediately; the editing agent reports the meaningful change on the ticket. |
| The producing worktree is removed | The canonical Notebook copies remain available. |
| Two agents edit one file concurrently | Normal filesystem editor behavior applies; the ticket discussion coordinates ownership. |

## UI

Ticket detail presents an **Artifacts** section backed by the same directory
enumeration as `ticket show`. It supports:

- opening a Markdown artifact in the Notebook editor;
- copying its canonical path;
- handing over several files with an optional resulting state and comment;
- renaming an artifact; and
- deleting an artifact with confirmation.

The existing Notebook editor supplies content editing. Each completed UI action
refreshes the ticket read so the displayed list reflects the directory.

## Agentic instructions

Correct handover behavior uses both guaranteed role guidance and the attn skill.
The guaranteed layer carries the rule an agent must act on; the skill supplies
command examples and detailed operation guidance.

| Role | Guaranteed guidance | Skill guidance |
|---|---|---|
| Chief of staff | Read the ticket's current artifact paths before follow-on work, pass those paths to delegated agents, and expect meaningful changes to be reported on the ticket. | How to inspect handover receipts, open plans, and include canonical paths in delegation briefs. |
| Chief-tracked delegated agent | Hand over a durable plan with `ticket attach`; after success, treat the returned Notebook paths as canonical and report meaningful edits, renames, or deletions through ticket status or comments. | Multi-file examples, optional state/comment usage, collision handling, and retry behavior. |
| Plugin-provided delegated agent | The same tracked-agent rule arrives in the universal delegated initial prompt. | Plugins may expose the bundled references when supported. |
| Ordinary agent | Existing ticket-awareness guidance remains sufficient. | Ticket handover guidance is loaded when the agent is working with a tracked ticket. |

The implementation updates:

- `ChiefGuidance` in `internal/hooks/hooks.go` with the chief rule;
- `delegatedTicketPrompt` in `internal/daemon/delegate.go` with the tracked-agent
  rule;
- `internal/agent/attn_skill/references/delegated-agent.md` with producer
  mechanics;
- `internal/agent/attn_skill/references/delegation.md` with chief mechanics; and
- `internal/agent/attn_skill/references/tickets.md` with the full command and
  recovery contract.

Claude receives its guaranteed guidance through the appended system prompt;
Codex receives the same semantics through developer instructions. Plugin agents
receive the tracked-agent rule through the initial delegation prompt, making
the core behavior available across supported runtimes.

## Cross-runtime behavior

Codex, Claude, and plugin-provided interactive agents use the same handover,
status, comment, and ticket-read protocol. Runtime-specific ticket delivery
continues to use each runtime's existing capability: Claude may self-monitor,
while Codex and plugin runtimes respond to nudges and perform one-shot inbox
reads.

## One-PR implementation

- [x] Replace the current file-handoff command and protocol with `ticket attach`,
   including repeatable files, optional state/comment, a structured receipt,
   and the `attach_submitted` event.
- [x] Write new handovers to `tickets/<ticket-id>/` and make directory enumeration
   the ticket-read source. Existing attachment data remains untouched.
- [x] Add deterministic handover fingerprints, staging, locking, rollback, and
   retry recovery.
- [x] Update ticket detail to open, hand over, rename, and delete artifacts through
   Notebook file operations.
- [x] Apply the role-specific prompt and skill guidance.
- [x] Cover store, daemon, CLI, protocol, UI, prompt, and embedded-skill behavior;
   update the user-facing changelog.
- [x] Verify in a non-production profile with a multi-file handover and state
   change, chief read, direct edit, rename, deletion, and an idempotent retry
   returning the original receipt.
