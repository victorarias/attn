# Open what Claude just handed the user

When Claude Code calls `SendUserFile`, it is saying "look at this". attn is
the window the user is looking at, so attn should open the file instead of
leaving it as a transcript card the user has to chase.

## Shape

```
Claude Code calls SendUserFile({files: [...]})
  → catch-all PostToolUse hook (`attn _hook-tool-use`) already fires
  → hook forwards the sent paths to the daemon: open_sent_files
  → daemon gates on the open_sent_files_enabled setting (default ON)
  → .md/.markdown → openMarkdownTile (same path as `attn open`)
  → anything else → dropped, one daemon log line, no user-visible noise
```

Decisions, with the receipts behind them:

- **Ride the existing catch-all matcher, not a new `SendUserFile`
  matcher.** The `*` PostToolUse hook already spawns `attn _hook-tool-use`
  for every tool call; reading one more tool name out of the same payload
  costs nothing, while a second matcher would pay another process spawn
  per call and — because hook settings are baked into a per-session
  `--settings` file at spawn — would reach only sessions launched after
  the change. With the catch-all, already-running sessions pick the
  behavior up as soon as the installed binary is replaced.
- **Routing and the gate live in the daemon, behind a new unix-socket
  command (`open_sent_files`).** "What attn can show" is daemon/app
  knowledge, and the off switch must be live-toggleable and visible —
  `get_settings`/`set_setting` are websocket-only, so a hook-side gate
  could not read it. The hook stays a dumb forwarder: tool name matches,
  paths resolved against cwd, one fire-and-forget command. This needs a
  protocol bump; that is the honest cost of putting the gate where the
  user can reach it.
- **Markdown only.** The markdown tile is the whole reason this exists.
  The browser tile rejects `file://` (http/https only, `validateBrowserURL`),
  and no tile renders images, PDF, or CSV — and changing the tiles is out
  of scope. A non-markdown path is dropped silently: no error, no card,
  only a `d.logf` line for debugging.
- **Every markdown file in the call opens, no cap.** Markdown tiles are
  per-path (`markdownTileIDForPath`), so N files dock N individually
  closable tiles and re-sends reuse them. `files` arrays are one or two
  entries in practice; a cap would need a receipt nobody can produce and
  a surface to report itself on. If a real call ever docks an absurd
  number of tiles, that is the moment to measure and cap.
- **Off switch: `open_sent_files_enabled`, default ON, explicit `false`
  disables** (the `notebookDutyEnabled` pattern). Surfaced EFFECTIVE in
  `get_settings` and toggleable from the app's settings modal — the way
  to see it and the way out.
- **The hook can never hurt the session.** Daemon down, unknown session,
  vanished path, disabled gate, version-skewed daemon that does not know
  the command — the hook warns on stderr at most and exits 0.
- **Claude only.** `SendUserFile` does not exist for Codex; `codex.go` is
  untouched. A Codex payload never matches the tool name, so no explicit
  agent check is needed.

## Surfaces

- protocol: `OpenSentFilesMessage` in `main.tsp`, regenerate, bump
  `ProtocolVersion` (constants.go + `useDaemonSocket.ts` lockstep).
- hook: `internal/hooks` gains `SentFiles` (payload → absolute paths)
  beside `MarkdownEdits`; `runHookToolUse` forwards.
- client: `OpenSentFiles(sessionID, paths)`.
- daemon: `handleOpenSentFiles` + setting key, validation, effective
  surfacing, `command_meta` entry.
- app: settings-modal toggle.
- docs: this plan, a `changelog.d/` fragment.

## Verification

Unit: `SentFiles` payload parsing; daemon handler (markdown docks a tile,
non-markdown drops, gate off does nothing, no session resolves like
`open_markdown`). Live, on a non-production profile installed from the
branch: a Claude session calls `SendUserFile` with a markdown file and the
tile appears without stealing terminal focus; toggle off, send again,
nothing happens. Evidence recording on the PR.
