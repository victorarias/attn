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
- **Arbitrary roots are gated on the authenticated app client.** An explicit
  `root` on any fs command (`fs_list/read/read_asset/write/rename/delete/
  exists`, and later `fs_watch`/`fs_unwatch`/`fs_index`) is honored only for
  the connection that proves it is the attn app itself: trusted Tauri origin
  at accept time, plus the per-profile browser-host secret verified in
  `client_hello` (constant-time compare against `config.BrowserHostToken()`),
  plus `client_kind == "tauri-app"` — i.e. the existing `IsBrowserHost`
  identity minus the browser-tile capability bit, exposed as
  `wsClient.isTrustedAppClient()`. Enforcement lives in the single
  root-resolution chokepoint (`resolveFsRoot(client, raw)` /
  `fsStoreFor(client, raw)`), so every current and future root-taking command
  inherits the gate and cannot bypass it. An omitted/empty root still resolves
  to the Notebook root for every accepted client, unchanged — back-compat is
  preserved. Each PR that adds a root-taking command must ship negative
  protocol tests proving an ordinary (unauthenticated) WS client is denied
  explicit roots (read AND write paths, plus watch/index when they land)
  while omitted-root behavior is unaffected. PR1 carries the predicate,
  chokepoint, and the first negative tests; PR2 (watch) and PR3 (index) each
  add their own denial test.

## PRs

- [ ] PR1 daemon: `root` on fs_* + fs_read_asset + `fs_changed`, per-root store
      cache, root validation, protocol bump + auth gate: explicit roots
      require the authenticated app client (isTrustedAppClient), with
      negative protocol tests
- [ ] PR2 daemon: per-root watcher registry, generalized trackable,
      `fs_watch`/`fs_unwatch` (fs_watch inherits the root auth gate via
      resolveFsRoot + its own denial test)
- [ ] PR3 daemon: `fs_index` bounded recursive file index (fs_index inherits
      the root auth gate + denial test)
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
