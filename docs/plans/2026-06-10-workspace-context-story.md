# Plan: Workspace context as an area map

## Intent

Make context describe the workspace as an area of attention, not as one task or
goal. A new agent should understand what belongs together, the current shared
picture, the important threads when any exist, how the area evolved, and what
matters now.

## Product Fit

- A workspace is a user-curated spatial and attention area containing session
  panes and persistent tiles. Its contents may span directories, worktrees, and
  eventually endpoints.
- Sessions and tiles are views or participants inside the workspace. Context
  must not mirror the layout: one session may cross several threads, several
  sessions may share a thread, and a tile may matter without representing work.
- The singular `Goal` model is therefore wrong. Live contexts already contain
  multiple outcomes, inquiries, responsibilities, and reference material.
- Context should be an orientation map for that area. It is not a task tracker,
  session registry, dispatch ledger, or transcript.

## Interaction Model

```text
workspace
  -> Area: stable identity and boundary
  -> Current Picture: authoritative shared understanding now
  -> Threads: optional semantic slices of the area
  -> Timeline: a few sourced turning points
  -> Decisions and Constraints: durable area or thread boundaries
```

Agent workflow:

1. Read `Area` and `Current Picture` before acting.
2. Use a relevant thread when one helps organize the work. Do not create a
   thread merely because a session, pane, or task exists.
3. Update whichever area facts or threads materially changed.
4. Add a timeline entry only when a sourced turning point changed shared
   understanding, relationships, direction, or boundaries.
5. Remove content only when the agent's current work directly proves it stale or
   superseded. Attn's janitor handles occasional broad compaction.

## Format

```md
# Workspace Context

## Area
What body of work, inquiry, or responsibility belongs here; why it is grouped;
and what is outside the boundary.

## Current Picture
The area-wide facts, relationships, dependencies, and tensions true now,
whether or not Threads exist. Use prose or bullets, whichever communicates the
area more clearly.

## Threads

### <semantic name>
- Intent: <outcome, inquiry, responsibility, or reference role>
- Now: <current understanding>
- Open edge: <next action or unresolved question, when useful>
- Related: <non-obvious artifacts or surfaces, when useful>

## Timeline
- <YYYY-MM-DD>: <turning point> → <how it changed the current area>.
  Source: <PR, commit, ticket, document, or explicit user decision>.

## Decisions
- [area|<thread name>] <choice> — <brief rationale>. Source: <evidence>.

## Constraints
- [area|<thread name>] <boundary that still applies>.
```

Rules:

- `Area` and `Current Picture` are required. Omit any other empty section.
  Settled, reference-only, and tile-only workspaces may have zero Threads and no
  open edges.
- `Current Picture` and each thread's `Now` are authoritative over the timeline.
- Threads use semantic names, not IDs. They may represent outcomes, inquiries,
  responsibilities, or reference roles. They do not map one-to-one to sessions.
- `Open edge` and `Related` are optional. Do not add workflow state, inferred
  ownership, executor identity, or routine progress.
- Keep only a few timeline entries that explain the area. Exclude routine task
  completion and session activity.
- Every timeline entry needs a source. Use an exact date or named phase only
  when the source establishes it, and order entries only by an established
  sequence. Omit transitions whose relative position cannot be supported.
- Use causal language only when the source or a recorded decision establishes
  the consequence. Never use migration or context-edit time as event time.
- Unknown or disputed claims remain as an `Open edge`; they do not belong in
  `Current Picture` or `Timeline`.

## One-Time Cutover

- Replace the existing guidance and bundled skill with this contract.
- Do not support or document `Goal`, `Progress`, or `Handoff` as alternate
  headings.
- Do not add a format marker or compatibility layer. This is currently a
  single-user installation with a small, known set of contexts.
- Treat the format as agent guidance, not a publish-time schema. Focused tests
  protect the injected instructions; normal updates remain plain Markdown.
- Migrate the existing canonical contexts once rather than erasing them because
  they contain useful state.
- Apply the cutover offline: stop attn, back up the database, migrate or clear
  the canonical contexts, and delete the old checkouts.
- Restart attn and active context-capable agents so the new launch guidance is
  injected. Daemon recovery alone does not update an existing agent's prompt.

## Context Janitor

Broad cleanup belongs to attn, not every agent. Agents should update durable
meaning as work changes; they should not repeatedly rewrite the whole document
for size or style.

```text
successful canonical publish
  -> context is above 12 KiB
    -> reset a ten-minute debounce
      -> capture canonical content + revision
        -> configured agent + model run headlessly
          -> agent reads and replaces context through janitor-only tools
            -> validate basic area-map shape
              -> revision compare-and-swap publish
                -> leave existing checkouts stale
```

Configuration:

- Add one atomic `workspace_context_janitor` setting:

  ```json
  {"agent":"codex","model":"<agent-specific model>"}
  ```

  Empty disables the janitor.
- The agent selector lists only installed drivers that implement a headless task
  capability. Reuse that agent's existing executable setting.
- Offer agent-specific recommended model presets, plus a Custom option that
  reveals a free-text model override. Keep the preset list local and explicit;
  do not build model discovery yet.
- Validate the agent/model pair together so changing agent cannot accidentally
  retain a model chosen for another provider.
- Do not silently fall back to another agent or model. A bad configuration
  reports an error and leaves context unchanged.
- Keep one global configuration rather than per-workspace policy.

Execution:

- Add an optional `HeadlessTaskProvider` capability to agent drivers rather than
  reusing interactive `BuildCommand` or the Claude-specific review-loop path.
- Keep its request narrow: resolved executable, model, prompt, timeout, and an
  attn-owned janitor tool endpoint. Return only completion status and generic
  failure diagnostics; discard child output so context cannot enter daemon
  logs.
- Expose exactly two run-scoped tools: `read_context` returns the captured
  canonical document; `replace_context` stores the complete proposed
  replacement. The agent gets no shell, filesystem, web, memory, repository, or
  other MCP access.
- Codex uses `codex exec` with the selected model, an ephemeral session, ignored
  user configuration and rules, and only the janitor tool endpoint.
- Claude uses bare print mode when explicit API-key or cloud-provider
  authentication is available. Otherwise it loads no user, project, or local
  setting sources so normal OAuth, keychain, or organization-managed
  authentication can run while their customizations remain disabled. Both
  paths disable auto-memory and session persistence, enforce strict MCP
  configuration, and expose only the janitor tools.
- Implement built-in Codex and Claude adapters first. Plugin agents remain
  ineligible until their driver protocol supports the same scoped headless task.
- Run without an attn session, PTY, transcript watcher, user/project hooks,
  resume state, or workspace checkout. Organization-managed authentication
  hooks may run in Claude's managed-auth path. This is a daemon-owned
  background invocation.
- The tool server owns the candidate. Do not parse Markdown from the agent's
  final prose response.
- Record the configured agent, model, and resolved executable used for each run.

Trigger policy:

- Measure canonical UTF-8 bytes. Schedule compaction above 12 KiB, roughly twice
  the largest current context.
- Check after agent-authored publishes. Do not schedule from the janitor's own
  publish, so it cannot immediately loop.
- Reset a ten-minute timer whenever that workspace's context is published again.
  Session state and modified checkouts do not gate the run.
- Run at most one janitor agent invocation across the daemon.
- Do not persist scheduler state, scan on startup, or retry automatically. A
  later agent-authored publish can schedule another attempt.
- Expose a `workspace context compact` CLI command for manual use below the
  threshold. Defer the UI action.

Janitor contract:

- Work only from the supplied `context.md`. Do not read transcripts,
  repositories, or other workspace files.
- Preserve `Area`, current truths, unresolved open edges, decisions,
  constraints, source links, and useful turning points.
- Shorten prose, deduplicate, merge overlapping threads, and remove stale or
  superseded material only when the document itself establishes that.
- Add no new facts, dates, causality, ownership, or conclusions.
- If uncertain, preserve the content. A no-op is valid.

Safety:

- Require one `# Workspace Context`, non-empty `Area`, and non-empty
  `Current Picture` before publishing the result.
- Treat an identical result as a no-op and reject a result larger than its
  source.
- Apply only if the workspace still exists and the canonical revision still
  equals the captured revision.
- Store only the latest successful pre-compaction content per workspace for
  direct rollback.
- Attribute the update with the reserved updater id `attn-janitor`; render that
  id as `Attn Janitor` in the navigator instead of adding a new actor protocol.
- Leave every existing checkout stale after apply. Checkout files are
  agent-owned working copies and may have writers holding their current inode;
  the normal refresh/conflict workflow preserves both clean and modified local
  state without asynchronous replacement.
- Cancel the run on daemon shutdown or workspace deletion. Failure only logs and
  leaves context untouched.

Persistence:

```text
WorkspaceContextJanitorBackup {
  workspace_id
  source_revision
  source_content
  result_revision
  agent
  model
  created_at
}
```

## Implementation Steps

- [x] Update `WorkspaceContextGuidance` and the bundled workspace-context skill
      with the area map, thread semantics, timeline rules, and one-time migration
      workflow. Tell agents attn owns broad compaction.
- [x] Replace guidance and skill tests that assert the old singular-goal model.
- [x] Rewrite the current non-empty contexts one at a time, without manufacturing
      threads or history, then apply them in one backed-up offline migration and
      clear old checkouts.
- [x] Test fresh agents in this workspace and the multi-threaded `thunk`
      workspace. They should identify the area, current picture, relevant
      threads, important story, and open edges without reconstructing transcripts.
- [x] Test a settled or tile-only workspace with zero Threads and no open edge.
- [x] Add the atomic janitor configuration and expose only built-in agents with
      a `HeadlessTaskProvider`.
- [x] Add the run-scoped `read_context` and `replace_context` tools, then
      implement Codex and Claude non-interactive adapters with timeout and
      cancellation.
- [x] Schedule a simple post-publish size check and debounce, with one daemon-wide
      invocation at a time and no persistent scheduler state.
- [x] Add revision-CAS publish, basic shape/size validation, latest rollback
      snapshot, reserved updater rendering, stale-checkout handling, and the
      manual CLI command.
- [x] Test successful compaction, no-op, growth rejection, stale revision,
      invalid output, bad configuration, timeout, cancellation, and rollback.

## Separate Follow-Ups

- Notify or refresh active agents when another session publishes context.
- Decide whether context survives after the final workspace leaf closes.
- Promote Threads to structured entities only if independent UI or atomic
  updates later require it.

## Decisions

- Model the workspace as an area of attention, not a single goal or collection
  of tasks.
- Use optional semantic `Threads` without workflow state, IDs, or ownership.
  The term covers related outcomes, inquiries, responsibilities, and reference
  material without implying one workspace-wide goal.
- Make a one-time migration with no marker or compatibility layer.
- Keep the timeline area-level and causal, not an event or completion ledger.
- Let attn compact large contexts centrally and infrequently without a review
  gate. Add review later only if real compactions prove it necessary.
- Make the janitor a configurable headless agent invocation. Agent and model are
  separate concepts stored atomically; unsupported or invalid configurations
  fail without fallback.
- Give the janitor an agent loop through two context-specific tools, not broad
  workspace access and not a one-shot text completion.
- Keep causal clarity, freshness, and lifetime as separate product properties.
