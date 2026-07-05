# Plan: Present — chunk 1, the spine + window (thin end-to-end)

## Why / Alignment

First chunk of [docs/vision/present.md](../vision/present.md), agreed with
Victor 2026-07-04: one vertical slice proving the whole loop — an agent runs
`attn present`, a banner notice lands in the main window, clicking it opens the
presentation in **its own OS window** (second display capable), Victor reads a
level-0/1 change presentation (explicit frame, reading order, diffs), comments
inline, hands the round back; the agent gets a doorbell and reads structured
feedback via CLI. Feedback loop is **in** this chunk (the loop is the product).
Guidance stays at level 0–1 (order + per-file notes; no line annotations, no
mermaid). The old ⌘⇧E diff panel is untouched until the vision's demolition
rock. Grounded: Tauri multi-window is viable (mechanism A below); the daemon
already supports multiple WS clients cleanly.

## Data Model / Interfaces

Manifest v0 (`.present.yml` or any path; parsed by `internal/present`):

```yaml
version: 1
kind: changes            # only "changes" in chunk 1; "doc" is a later chunk
title: Nudge countdown fixes
frame:
  repo: /abs/worktree    # CLI defaults from cwd but always records absolute
  base: origin/main      # refs, resolved AND PINNED to SHAs at trigger time
  head: HEAD
summary: >               # optional markdown, rendered simply
files:                   # optional = level 0; present = level 1 reading order
  - path: internal/daemon/nudge.go
    note: optional one-paragraph note rendered above the diff
skip:
  - app/src/types/generated.ts   # rendered last, dimmed
```

Store (new tables, next free migration version — check `MAX(version)` in real
DBs first, per the burned-versions gotcha):

```sql
presentations:         id, session_id, ticket_id NULL, title, kind,
                       repo_path, status(open|closed), created_at
presentation_rounds:   id, presentation_id, seq, manifest_yaml,
                       base_sha, head_sha, created_at, submitted_at NULL
presentation_comments: id, round_id, filepath, line_start, line_end,
                       side(new|old), content, author(user), created_at
```

Protocol (pattern #1: main.tsp → `rm -rf tsp-output` → `make generate-types` →
constants → ProtocolVersion bump; tsc-check for the quicktype enum-merge trap):

```text
unix socket (agent CLI):
  present_open     {manifest_path | inline manifest, session from ATTN_SESSION_ID}
                   -> creates presentation + round 1 (or round N+1 via --presentation)
  present_feedback {presentation_id, round?} -> structured markdown of comments
websocket (app):
  evt  presentation_added / presentation_updated   (row + latest round summary)
  cmd  get_presentations, get_presentation_round   (manifest struct + pinned SHAs)
  cmd  present_submit_round {round_id, comments[], handback: bool} -> *_result
  diff content: get_file_diff extended with pinned base_sha/head_sha
                (FileDiff today diffs base..worktree; add explicit head ref)
```

## Architecture Map

```text
agent (delegated session, worktree)
  -> attn present --manifest .present.yml        [cmd/attn, parse+pin via internal/present]
    -> unix socket present_open -> daemon: store insert + broadcast presentation_added
main window (App.tsx)
  -> banner notice (nudge-strip pattern) "▶ <title> — <session>"
    -> click -> invoke open_presentation_window(id)          [new Rust cmd]
present window (label "present", WebviewWindowBuilder,
                url index.html?window=present&presentation=<id>)
  -> main.tsx branches on ?window=present -> <PresentRoot/>  [slim root, NOT App.tsx]
    -> own useDaemonSocket (client_hello workspace_sessions; no browser-host token)
    -> get_presentation_round -> ordered file list (files -> others -> skip dimmed)
    -> per file: get_file_diff at pinned SHAs -> DiffView (@pierre/diffs)
    -> j/k nav, gutter comment -> local drafts -> submit dialog
    -> present_submit_round {comments, handback}
daemon on submit
  -> store comments; ticket-bound: ticket activity + existing nudge machinery
     else: typeDoorbell(session, fixed "run attn present feedback <id>" prompt)
agent -> attn present feedback <id> -> quoted, file:line-structured markdown

Tests:
  internal/present golden manifests (valid + every rejection)
  daemon handler tests beside ws_review/ticket patterns; store migration test
  PresentRoot unit tests w/ createMockDaemon pattern
  packaged: real-app scenario (fixture repo: present -> banner -> window ->
            comment -> submit -> feedback CLI round-trips) — single-tenant
```

## Boundaries

- `internal/present` owns manifest parse/validate/pinning. Pure file+git →
  struct; no store, no daemon imports.
- Daemon translates and stores; it never resolves file content at ingest — the
  reader fetches diffs live at the round's pinned SHAs (pin = refs, drift =
  banner; no content snapshots).
- `PresentRoot` never imports `App.tsx` — no deep-link listeners, no
  UI-automation bridge, no browser-host token (Rust `TrustedMainWebview` gate
  already denies it), theme consumed read-only.
- The presentation window coordinates with main via the daemon WS, not Tauri
  events (avoids capability wiring; both windows are ordinary WS clients).
- Old diff panel and `(repo_path, branch)` review store: untouched this chunk.

## Implementation Steps

- [x] **PR 1 — Go spine.** `internal/present` (parse, validate, SHA pinning);
      store tables + migration; protocol messages + version bump; daemon
      handlers (`present_open`, feedback, get/list, submit_round persistence);
      `attn present` / `attn present validate` / `attn present feedback` in
      `cmd/attn`; doorbell-on-submit (ticket activity when bound, bare
      doorbell otherwise). Tests as mapped. (Merged: #465.)
- [x] **PR 2 — window shell.** Rust `open_presentation_window` (build-or-show,
      hide-on-close-request), label-guarded `on_page_load` show,
      `capabilities/present.json` (core:default + clipboard), focus-aware
      `on_menu_event`/`dispatch_native_shortcut` (⌘W on present window hides
      it, never touches main panes); `main.tsx` `?window=present` branch with
      a hello-world `PresentRoot` proving WS + theme; banner notice in main
      window wired to `presentation_added` → open window; banner persists
      while window hidden (that IS minimize-to-banner). (Merged: #466.)
- [x] **PR 3 — the reader.** Ordered file list, pinned-SHA diffs through
      `DiffView`, per-file notes + summary (existing simple markdown renderer;
      no new markdown stack), keyboard nav, inline comment drafts, submit
      dialog with handback toggle, drift banner (round head vs current branch
      head). `get_file_diff` head-ref extension lands here with its daemon
      tests. (Merged: #482.)
- [x] **PR 4 — packaged evidence + polish.** Real-app scenario for the full
      loop; live dev-app smoke with a real delegated agent; CHANGELOG entry. (Open: #484 — live-verified, in review.)

## Decisions

- **Fresh presentation-scoped storage**, not the `(repo_path, branch)` reviews
  key — the vision retires that identity; migrating old rows buys nothing.
- **Explicit `side` column** on comments instead of inheriting the
  negative-`line_end` encoding from ReviewComment — fresh schema, no hack.
- **Pin = SHAs, content fetched live** at those SHAs; drift is a visible
  banner. No per-round content snapshots (vision: rounds pin, never freeze).
- **Single HTML entry + `?window=present`** (grounded mechanism A) over a
  separate Vite entry — maximal component sharing; `test-harness` precedent
  exists if a split becomes worth it.
- **Separate capability file** for the `present` label; `default.json` stays
  `["main"]` (a browser-host test pins it, and the child-webview security
  invariant depends on it).
- **Round N+1 = re-run `attn present --presentation <id>`** with re-pinned
  SHAs — no separate "update" verb to learn.

## Open Questions

- Doorbell copy + whether a submitted round should also flip a bound ticket to
  a state the chief narrates (leaning: ticket activity only, no status write).
- Banner stacking when several presentations are open at once (chunk 1: newest
  wins, count badge; revisit with real use).
- Presentation close/archive lifecycle (chunk 1: `status=closed` on
  handback-with-no-reopen is deferred; instances just accumulate).

## Follow-ups

- Level ≥2 guidance (line annotations, threads, tour summary card) — next
  chunk, brings the jaunt authoring-skill port with it.
- Document (`kind: doc`) reader + annotation; shared markdown renderer paying
  the ticket-board debt.
- Changes-since-round view; gates; pull; `/teach`; ⌘⇧E demolition — per the
  vision's big rocks.
