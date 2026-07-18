# Plan: Editor Over Arbitrary Roots

## Why

The notebook UI proved itself, but pinning it to `notebook.root` walls the user
off from the rest of their working tree: a repo that *contains* the notebook
can't be browsed, so the tool blocks the user's own system. The UI surface
becomes a folder editor over any root, defaulting to the selected workspace's
directory. The storage concept (keeper, journal, knowledge base) stays bound to
`notebook.root` and keeps the Notebook name.

## Decisions

- Naming: storage keeps **Notebook**; the UI surface's user-facing name becomes
  **Editor**. Internal identifiers (`tile_kind: 'notebook'`, component/file
  names, shortcut ids) keep their names for now — persisted layouts and protocol
  strings aren't worth a migration; rename opportunistically later.
- The frontend stays root-relative: every fs command (`fs_list/read/write/
  exists/rename/delete/read_asset`) gains an optional absolute `root` (omitted
  or empty = notebook root, full back-compat). The daemon resolves and validates
  it (absolute, `~` expansion, outside the attn data dir) and keeps today's
  traversal/symlink guards (fsdoc `cleanRel` + `EnsureWithinResolvedRoot`).
  Per-root `fsdoc.Store` cache replaces the single cached store.
- Watchers become a per-root registry with client-refcounted `fs_watch` /
  `fs_unwatch` (disconnect drops refs, watcher closes at zero; hard cap on live
  watchers). The notebook root keeps its always-on watcher and dual
  `notebook_changed` + `fs_changed` broadcast; other roots broadcast
  `fs_changed` only. `Watcher.trackable()` generalizes from .md-only to all
  non-dot files. `fs_changed` gains a `root` field so clients can filter.
- Finder over big repos: new bounded `fs_index` command (recursive walk,
  skips dot-dirs / `node_modules`, entry cap with a `truncated` flag) becomes
  the ⌘P finder's source, replacing the .md-only `notebook_list` walk.
- ⌘⌥N opens the editor tile at the selected workspace's `Directory` (fallback:
  notebook root). Notebook entry points (sidebar button, ⌘⌥⇧N fullscreen) stay
  notebook-rooted. The tile header shows the root and offers a switcher
  (workspace dir / Notebook / browse).
- Notebook-only affordances (backlinks rail) hide when the tile's root differs
  from the effective notebook root.
- tileParams becomes `{ root?, path? }` JSON; a bare string tileParams keeps
  meaning "path under the notebook root" (back-compat with persisted tiles).
- Authorization boundary: an explicit `root` names an arbitrary absolute
  directory, so path validation alone isn't sufficient — any accepted local
  WebSocket client could otherwise read, write, delete, index, or watch files
  anywhere on disk via the daemon. Every `fs_*` command that accepts a
  non-default `root` (including `fs_index`, `fs_watch`, `fs_unwatch`) requires
  the authenticated app identity — the existing `IsBrowserHost()` boundary used
  for the Tauri client — before honoring it. Omitted/empty `root` (today's
  notebook-only behavior) stays available to any accepted client, unchanged.
  An unauthenticated client requesting a non-default root gets a protocol
  error, not a silent notebook-root fallback.

## Verification

- PR1 lands negative protocol tests in the daemon slice: an ordinary/untrusted
  WebSocket client is proven unable to operate (read, write, index, or watch)
  on an arbitrary root. Subsequent PRs adding `root`-aware commands extend
  this coverage rather than assuming it.

## PRs

- [ ] PR1 daemon: `root` on fs_* + fs_read_asset + `fs_changed`, per-root store
      cache, root validation, `IsBrowserHost()` authorization gate for
      non-default roots, negative protocol tests, protocol bump
- [ ] PR2 daemon: per-root watcher registry, generalized trackable,
      `fs_watch`/`fs_unwatch`
- [ ] PR3 daemon: `fs_index` bounded recursive file index
- [ ] PR4 frontend: tile root plumbing (tileParams `{root, path}` + string
      back-compat), root-aware fs calls, `fs_changed` root filtering
- [ ] PR5 frontend: workspace-directory default for ⌘⌥N, header root switcher,
      finder on `fs_index`
- [ ] PR6 frontend: off-root affordance gating, Editor labels + settings copy,
      changelog, packaged-app bridge-only scenario (editor over a workspace
      root)

## Open Questions

- Respect `.gitignore` in `fs_index`? Start with the fixed ignore list and
  revisit if the finder is noisy in big repos.
- Recent-roots persistence for the switcher — start with workspace + Notebook +
  browse only.
