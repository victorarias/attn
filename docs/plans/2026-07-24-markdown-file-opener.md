# Markdown file opener

## Why / Alignment

Today a markdown file only reaches a reader tile through a link a user can ⌘+click or an
`attn open` an agent runs. When there is no link on screen and the agent is busy, there is
no way to open a document. This chunk gives the user a keyboard-driven opener that does not
use the native macOS file dialog and is not a plain file tree.

Done, for the first PR: pressing ⌘P anywhere in the app opens a finder that lists recently
opened markdown files, filters markdown files under the current session's working directory
as you type, and opens the pick as a docked reader tile.

### Aligned on

- **Two modes, one input.** Default is a fuzzy filter over markdown files in the selected
  session's cwd (falling back to the notebook root in a session-less workspace). A query
  starting with `/`, `~/`, or `./` switches to path navigation: the text before the last
  slash is listed, the text after it filters that listing. A leading slash is the only mode
  trigger, so `docs/plan` stays an ordinary fuzzy query.
- **Recents are one unified list, not a pinned section.** On an empty query the list is
  recents. As soon as the user types, recents and index results rank together with a recency
  bonus — no second list to navigate, no mode change mid-typing.
- **Every open counts.** Recents are recorded daemon-side in the `open_markdown` handler,
  which is the single chokepoint for ⌘+click, `attn open`, and finder picks alike. No client
  bookkeeping, no origin flag.
- **Ranked by frecency**, following the existing `recent_locations` model (`last_seen` plus
  `use_count`). A recent whose file has since disappeared is pruned when opening it fails,
  not stat'd on every summon.
- **The schema anticipates agent-edited files.** The table is a file-activity log
  (path, source, session id, timestamp, count) with `source = "opened"` today. Surfacing
  files that Claude/Codex edited becomes a new source value plus a ranking weight, not a
  migration. When built, the signal comes from the existing `PostToolUse` / `post_tool_use`
  hook wiring — precise per-session attribution, partial agent coverage accepted — and
  edited files float up inside the same single list rather than getting their own group.
- **⌘P is a rebindable registry shortcut.** A focused notebook tile keeps its existing
  in-tile finder; everywhere else summons the global one.
- **Opening goes through `open_markdown`** bound to the selected session, so tile reuse,
  live reload, and the annotation Send target come for free.
- **Honest bounds.** `fs_index` is capped at 25k entries server-side; when a walk reports
  `truncated`, the finder says the index is capped rather than implying completeness.
  Path mode lists one directory at a time via `fs_list`, so navigating `~/` never triggers a
  huge walk. `~` expands daemon-side, where `resolveFsRoot` already gates root access.

### In scope (first PR)

File-activity table and migration; recording in the `open_markdown` handler; a protocol
command to read the list; the global finder overlay with fuzzy mode, recents, and keyboard
navigation; the ⌘P registry entry and its tile-focus precedence rule.

### Deferred

- Path-navigation mode (`/`, `~/`, `./`) — second PR, on the same overlay.
- Agent-edited files as a ranking signal — schema is ready, feature is not built.
- Non-markdown file types; creating a file that does not exist.
- Multi-root workspaces (a workspace whose sessions span several repos).
- Remote/hub endpoints — v1 browses the local filesystem only.
- Caching the enumeration in the daemon (invalidated by the existing refcounted
  `fsRootWatch` watcher). Deferred pending measurement on a large repo — with git
  enumeration a cold pass should be cheap, and the async load hides it. Revisit with
  numbers rather than adding cache lifecycle and staleness risk on spec.

### Index performance

The walk is what stops scaling, not the payload, so the fuzzy corpus is built as:

- **Markdown filtered server-side.** `fs_index` takes an extension filter, so the
  25k entry cap applies to markdown only. Today the cap counts every file and a
  large repo can truncate — lexicographically, via `WalkDir` order — before the walk
  ever reaches the documents being searched for.
- **Enumerated through git inside a repo.** `git ls-files` for tracked files plus
  `--others --exclude-standard` for untracked-but-not-ignored, falling back to
  `WalkDir` outside a repository. This honors `.gitignore` instead of the hardcoded
  `node_modules` + dot-directory skip list, so build output and vendored trees cost
  nothing, and it reads an index git already maintains rather than stat-ing the tree.
- **Loaded asynchronously.** The palette opens immediately on recents; the fuzzy
  corpus lands when ready. A cold enumeration never blocks ⌘P.

Consequence, accepted: a gitignored markdown file is invisible to fuzzy mode. Path
mode still reaches it, since that lists directories directly.

## Pre-work

The app already has three hand-rolled finder overlays, each re-implementing the same
shell (overlay, input, listbox with `aria-activedescendant`, Arrow/Enter/Escape,
`scrollIntoView`, mousedown-to-pick, backdrop dismiss):

| Component | Owns | Scorer | Data source |
| --- | --- | --- | --- |
| `components/ActionMenu.tsx` | global command palette | substring terms | in-memory actions |
| `components/notebook/NotebookFinder.tsx` | per-tile ⌘P file finder | `finderRank.ts` subsequence | `fs_index` |
| `components/LocationPicker.tsx` | new-session path picker | server-side prefix | `browse_directory` + `get_recent_locations` |

`LocationPicker` already implements the path-navigation behavior this feature wants —
`~` expansion, per-segment listing, recents and directories merged into one keyboard
list (`PathSelectableItem = {kind: 'recent' | 'directory'}`) — on a different protocol
command, inside a 1166-line component.

The markdown opener must not become the fourth copy. Pre-work below leaves the codebase
with one palette shell and one path-navigation backend.

### R1 — Extract the palette shell (before the opener) — DONE

Add `app/src/components/palette/`, owning chrome, focus, keyboard handling, a11y, and
selection clamping, parameterized by rows, a row renderer, `onPick`, and `onClose`.
`NotebookFinder` becomes a thin wrapper over it; the markdown opener is the second
consumer. `ActionMenu` migrates after, only if its `FocusTrap` + `useEscapeStack` usage
lands cleanly — its scorer stays its own.

Collapse the duplicate mount while here: `NotebookSurface.tsx` renders `<NotebookFinder>`
twice (tile variant and modal variant) with identical props.

No behavior change; the existing `NotebookFinder` and `finderRank` tests are the guard.

### R2 — Get the scorer off `NotebookEntry` (with R1) — DONE

`finderRank.ts` scores `NotebookEntry`, a notebook-note metadata type (`type`, `summary`,
`size`). `App.tsx` already coerces arbitrary `fs_index` results into it via
`fsIndexToNotebookEntries`, which is a semantic lie predating this work — an arbitrary
root is not a notebook. Introduce a minimal `FileCandidate { path, title?, updated? }`,
move the scorer beside the palette, and let `NotebookEntry` structurally satisfy it.
Pure move, no behavior change.

### R3 — One path-navigation backend (with path mode, PR2)

Two directory-listing surfaces exist:

- `browse_directory` (`internal/daemon/ws_picker.go`): `~` expansion, last-segment prefix
  filtering, `home_path` round-trip, endpoint-aware (remote support later comes free) —
  but **directories only**.
- `fs_list`: returns files and directories with kind and size, root-scoped, auth-gated —
  but no `~` or partial-segment parsing.

Extend `browse_directory` with an optional extension filter and reuse
`useFilesystemSuggestions` + `utils/locationPickerPaths` wholesale, rather than
re-implementing `parseBrowseInput`'s parsing in TypeScript.

Attached finding: `fs_index` and `fs_list` gate arbitrary roots on the authenticated app
client via `resolveFsRoot`; `browse_directory` has no equivalent gate. That is tolerable
while it leaks only directory names. Making it list files is the moment to close it.

### R4 — File-activity store placement (with the opener) — DONE

`recent_locations` lives inline in `store.go` with its own frecency scorer, pruning, and
delete-on-missing. Put file activity in its own `internal/store/file_activity.go`
(matching the newer convention of `markdown_annotations.go`) and lift frecency scoring
into a shared helper instead of copying that SQL a second time.

### R5 — Hook seam (note only, not built)

Every `PostToolUse` matcher generated by `internal/hooks` calls `_hook-state` and discards
the JSON payload. When agent-edit tracking arrives, the naive move — a second hook entry —
adds a second process spawn on *every* tool call. The right shape is one `_hook-tool-use`
that sets state and records the touched path together. Knowing this now because it may
change how the hook config is generated.

### Daemon fixes to fold in

Both landed with the opener PR, except the dot-directory harmonization: fuzzy
mode still hides dot-prefixed paths (git enumeration applies the same rule the
walk always did), while `listDirectoryEntries` still shows them. Path mode is
where that inconsistency becomes visible, so it is folded into PR2 (R3).


- `fs_index`'s 25k cap counts every file, before any markdown filter, and truncation
  follows `WalkDir` lexicographic order — so in a large repo, documents under
  late-alphabet directories silently vanish from fuzzy mode. An optional server-side
  extension filter makes the cap apply to markdown files only.
- Dot-directory rule is inconsistent: `fs_index` skips dot-directories entirely,
  `listDirectoryEntries` does not. So `~/.claude/` is reachable in path mode but invisible
  in fuzzy mode. Pick one rule and apply it to both.

### Shipped so far

- PR1 (merged): R1 + R2 — the shared palette shell and the scorer's move off
  `NotebookEntry`.
- PR2 (this PR): the opener itself. `file_activity` table (migration 80) plus
  `internal/store/file_activity.go`; recording in `openMarkdownTile`, which also
  now refuses a deleted file and forgets it; protocol 185 with `recent_files`
  and an `extensions` filter on `fs_index`; git-backed enumeration; the global
  ⌘P palette with recents, unified ranking, and an async index load; the
  `paletteClaim` hand-off that keeps a focused Editor tile's own ⌘P. Verified
  live by `real-app:scenario-markdown-opener`.
- Next: path mode (R3).

### Order

R1 + R2 ship as one no-behavior-change PR. The opener PR then lands on a shell that
already has two consumers, and carries R4. R3 lands with path mode in PR2. R5 stays a
note until agent-edit tracking is built.
