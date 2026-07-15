# Plan: Richer ticket content, staged — markdown, previews, HTML

**Status: SUPERSEDED on 2026-07-13.** Slice (a)'s user-facing goal shipped through
the shared `Markdown` renderer, which now renders ticket descriptions and comments
with GFM and Mermaid support. The access-pattern dependency also shipped as the
ticket chip and in-pane `TicketDetailPanel` overlay.

Slices (b) and (c) were superseded by
[2026-07-09-design-artifact-handover.md](2026-07-09-design-artifact-handover.md).
Current ticket artifacts are visible, filesystem-canonical Markdown files under
`tickets/<ticket-id>/`; ticket detail opens them in the Notebook editor and supports
attach, rename, and delete. The arbitrary hidden-attachment model, byte-preview
protocol, and sandboxed HTML attachment server proposed below are no longer the
product architecture. Revisit non-Markdown ticket artifacts only through a new
alignment and plan if real usage earns that capability.

One narrow requirement was not carried forward: this plan proposed ticket-specific
link and image components that block remote image loads and route safe links through
the app opener. The shared renderer currently uses its default link/image behavior.
If that boundary still matters, track it as a focused Markdown-hardening change rather
than resuming this plan.

## Goal

Make a ticket's content worth reading in-app. Today `TicketDetailPanel` renders the
description and every comment as plain text (`<p className="ticket-detail-description">`,
`ActivityEntry`'s comment `<div>`), and an attachment is a dead row — filename + note,
no way to see it. Agents already hand over real artifacts (`attn ticket attach`), and
the vision's "distilled by default, drill-in on demand" principle
(docs/vision/chief-delegation-awareness.md) only works if the drill-in view can actually
present what was handed over. Three independently-landable slices, strictly staged and
usage-gated: **(a)** markdown rendering of description + comments, **(b)** attachment
previews (images inline, text peek), **(c)** HTML attachments rendered in attn's
existing embedded browser — so an agent can hand Victor a diagram, a dynamic explainer,
or a prototype with zero new agent-side infrastructure. The board still only informs;
nothing here auto-renders into Victor's face — every render is behind a click.

**The seam and the gate.** The sibling ticket-pane-overlay plan
(docs/plans/2026-07-02-ticket-pane-overlay.md) is the access-pattern experiment this
plan rides on: it puts the ticket detail view where Victor actually works. The seam is
`TicketDetailPanel` itself — every slice here lands inside that component (and its new
children), so every host (the App.tsx `ticketDetail` dock panel today, the pane overlay
when it lands) inherits richer content with no per-host work. The gate: slice (a) ships
once the overlay experiment shows tickets get read in-app; (b) ships only if rendered
markdown is actually appearing in briefs/comments Victor reads; (c) only if previews get
used. Gates are Victor's judgment call — no usage telemetry.

## Architecture Map

```text
Current:
  TicketBoardSurface / board dock ── onOpenTicket ──> App.tsx dock panel 'ticketDetail'
    -> TicketDetailPanel (fetchTicket = ws get_ticket -> sendGetTicketWSResult, ticket_board.go)
         description/comments: plain text
         attachments: filename + note only (protocol TicketAttachment{id,filename,path,note})
  attachment files: attn ticket attach -> handleTicketAttach (ticket_attach.go)
    -> copyTicketAttachment -> <notebookRoot>/.attn/tickets/<id>/   (notebook.TicketAttachmentsDir)
    NOT readable via fs_read: fsdoc CleanPath rejects dotdir segments (internal/fsdoc/store.go)

Target:
  (a) TicketDetailPanel description / activity comments
        -> TicketMarkdown (new, app/src/components/TicketMarkdown.tsx)
           react-markdown + remark-gfm (already deps; same stack as CommentPopover,
           DiffCommentThread, WorkspaceDockTile) + safe link/img components map
           (reuses exported resolveMarkdownTarget from WorkspaceDockTile.tsx)
  (b) TicketDetailPanel attachment row [Preview]
        -> fetchTicketAttachment (useDaemonSocket.ts, mirrors fetchTicket promise pattern)
          -> ws ticket_attachment_read -> sendTicketAttachmentReadWSResult (daemon)
            -> store.GetTicket -> read att.Path (capped, inside TicketsDir only)
            -> result { filename, mime, content_base64 }
        image -> <img src="data:mime;base64,..">   text -> <pre> peek
  (c) TicketDetailPanel .html attachment [Open in app browser]  (Tauri only)
        -> handleOpenTicketAttachment (App.tsx, mirrors handleOpenNotebookTile):
           sendWorkspaceDockTile(activeWorkspace, 'tile-browser', 'browser') (ignore exists)
           + sendWorkspaceUpdateTile(activeWorkspace, 'tile-browser', serve_url)
        -> BrowserTileBody -> browser_host_mount (native child WKWebView, http/https only)
        -> GET serve_url on the daemon's NEW loopback attachment listener
           (127.0.0.1 ephemeral port, serves ONLY .attn/tickets/, CSP-sandboxed)

Tests:
  TicketMarkdown.test.tsx / TicketDetailPanel.test.tsx  (vitest, mock fetch props)
  internal/daemon/ticket_attachment_read_test.go        (wsClient{send: chan} + readTicketResult pattern)
  internal/daemon/attachment_server_test.go             (httptest against the new listener + /ws origin denial)
```

## Data Model / Interfaces

No store migration — `ticket_attachments` already holds `filename, path, note` and the
files already land under `.attn/tickets/<id>/`. All changes are protocol + serving.

```tsp
// slice (b): new ws request/result pair (mirror FsReadMessage / FsReadResultMessage)
model TicketAttachmentReadMessage {
  cmd: "ticket_attachment_read";
  ticket_id: string;
  attachment_id: int32;
  request_id?: string;
}
model TicketAttachmentReadResult { filename: string; mime: string; content_base64: string; }
model TicketAttachmentReadResultMessage {
  event: "ticket_attachment_read_result";
  request_id: string; success: boolean; error?: string;
  result?: TicketAttachmentReadResult;
}

// slice (c): TicketAttachment gains the app-facing serving URL
model TicketAttachment { ...existing; serve_url?: string }
// populated in sendGetTicketWSResult only (the app is the sole consumer), from the
// daemon's live attachment-listener base: http://127.0.0.1:<port>/tickets/<ticket-id>/<on-disk-name>
```

Frontend: `TicketDetailPanel` gains an optional `fetchAttachment?: (ticketId: string,
attachmentId: number) => Promise<{filename, mime, contentBase64}>` prop and (c) an
optional `onOpenAttachmentInBrowser?: (serveUrl: string) => void`. Wiring follows the
App.tsx two-component gotcha (app/CLAUDE.md): destructure from `useDaemonSocket()`,
pass to `<AppContent>`, add to `AppContentProps`, destructure in `AppContent`.

## Boundaries

- `TicketMarkdown` owns safe rendering: react-markdown never gets `rehype-raw` (raw HTML
  in a brief/comment stays inert text), links resolve through `resolveMarkdownTarget`
  and open via `openUrl` (plugin-opener) — `javascript:`/`file:`/relative targets render
  as inert spans; images render as a blocked placeholder (mirror
  `workspace-dock-tile-blocked-image`), never a network fetch from the panel.
- The daemon owns attachment bytes. The panel never touches `att.path` directly (it also
  runs in the daemon-served web UI, which has no filesystem); reads go over the ws
  request/result seam, capped at 5 MB, and only for paths inside `notebook.TicketsDir`
  (a legacy row pointing elsewhere gets a "not previewable" error, not a read).
- The attachment HTTP listener serves files, nothing else: read-only GET, only under
  `.attn/tickets/<ticket-id>/` for a ticket that exists in the store, traversal and
  dotfile segments rejected. No /ws, no state-changing route, loopback bind only.
- Agents stay pull-based and unchanged: `attn ticket attach` already accepts any file
  type. Nothing auto-opens; doorbells still trigger a read, never carry content; attn
  still authors exactly one status (crashed). This plan adds zero agent-side machinery.

### Security boundary for HTML attachments (slice c) — required reading

The embedded browser is a native child WKWebView (`browser_host.rs`), which already
gives us: **no Tauri IPC** (it loads external http/https URLs; Tauri injects no invoke
bridge for remote URLs, and every `browser_host_*` command is gated on
`TrustedMainWebview` = the `main` webview label), **scheme allowlist** (`parse_url` and
`on_navigation` accept only http/https/about — which is also why serving must be HTTP:
`file://` cannot load there), and **isolated cookies/storage**
(`BROWSER_DATA_STORE_ID`).

The real exposure is the daemon websocket: `isAllowedLocalOrigin` (websocket.go) admits
**any** `http://localhost`/`http://127.0.0.1` origin, and `/ws` has no default auth — so
agent-authored HTML served from any plain localhost origin could open the ws and drive
the daemon (spawn sessions, pty_input = arbitrary exec). Serving from the daemon's own
port 9849 is worst of all: that origin must stay ws-allowed for the daemon-served web
UI. Hence the design:

1. **Dedicated loopback listener, distinguishable origin.** A second `http.Server` in
   the daemon (`initAttachmentServer`/`runAttachmentServer` beside `initHTTPServer`,
   daemon.go), bound `127.0.0.1:0`. Files only, under `.attn/tickets/` only.
2. **CSP does the client-side sandboxing.** Every response carries
   `Content-Security-Policy: sandbox allow-scripts; connect-src 'none'` plus
   `X-Content-Type-Options: nosniff` and the existing `setNoStoreHeaders`.
   `sandbox allow-scripts` gives the document an opaque origin (any ws attempt sends
   `Origin: null`) with no cookies/storage; `connect-src 'none'` blocks fetch/XHR/WS
   outright. Inline script/style stay allowed (self-contained explainers run), and
   same-listener relative loads let a multi-file prototype reference sibling
   attachments.
3. **Server-side denial as the second independent layer.** `handleWS` rejects, before
   any allow rule, an Origin whose host:port equals the attachment listener's address
   (new `d.isAttachmentOrigin(origin)` check), and `Origin: null` already fails
   `isAllowedWSOrigin`'s host check — both get explicit tests. If WebKit ever failed to
   apply the CSP, the page's real origin is the attachment port and the handshake is
   still refused.
4. The unix socket (`~/.attn/attn.sock`) is unreachable from web content by
   construction; local processes reading the attachment port see only files they could
   already read on disk.

## Implementation Steps

### Slice (a) — markdown rendering (frontend-only, no protocol change)

- [ ] `app/src/components/TicketMarkdown.tsx`: `<ReactMarkdown remarkPlugins={[remarkGfm]}>`
      (the CommentPopover usage) plus a `components` map for `a`/`img` per Boundaries —
      import `resolveMarkdownTarget` from `SessionTerminalWorkspace/WorkspaceDockTile`
      (already exported) with an empty `documentPath` so relative/local targets resolve
      to null. Styles in `TicketDetailPanel.css` (beware the undefined-token gotcha:
      only `var()` refs with fallbacks).
- [ ] Use it in `TicketDetailPanel.tsx` for the description block and both `ActivityEntry`
      comment renders (status-change note + freeform comment).
- [ ] `TicketMarkdown.test.tsx`: gfm renders (list/bold/link); `<script>`/`<img onerror>`
      raw HTML never becomes elements; `javascript:` link renders as inert span;
      http link click calls the mocked opener and never navigates.
- [ ] `TicketDetailPanel.test.tsx`: extend the existing render cases (mirror
      "fetches the full record exactly once on open and renders it") to assert `**bold**`
      in description and comment reaches a `<strong>`.
- [ ] CHANGELOG entry (user-visible: ticket briefs and comments render as markdown).

### Slice (b) — attachment previews (gated on (a) proving used)

- [ ] Protocol: add the `ticket_attachment_read` pair to `internal/protocol/schema/main.tsp`;
      `rm -rf tsp-output && make generate-types`; add the cmd/event constants + decode case
      to `internal/protocol/constants.go`; bump ProtocolVersion (CLAUDE.md critical
      pattern #1); tsc-check generated.ts (quicktype merges value-identical enums).
- [ ] Daemon: `sendTicketAttachmentReadWSResult` in a new `internal/daemon/ticket_attachment_read.go`
      (mirror `sendGetTicketWSResult` + `sendFsReadWSResult`): `store.GetTicket`, find
      attachment by id, refuse paths outside `notebook.TicketsDir(root)`, refuse > 5 MB,
      mime from extension (`mime.TypeByExtension` with a fallback map), base64 the bytes.
      Dispatch beside `CmdGetTicket` in websocket.go.
- [ ] Frontend: `fetchTicketAttachment` in `useDaemonSocket.ts` (copy the `fetchTicket`
      request/result promise at its `get_ticket` key pattern); attachment rows in
      `TicketDetailPanel.tsx` get a lazy Preview toggle — image mimes render
      `<img src="data:...">`, text mimes render a `<pre>` peek, anything else keeps the
      bare row. SVG previews only ever go through `<img>` (no script execution), never
      inline DOM.
- [ ] Tests: `internal/daemon/ticket_attachment_read_test.go` (fixtures via
      `delegateForNotify` + `boundTicketID` + `callTicketAttach`; assert happy path
      base64+mime, unknown id, oversize error, outside-TicketsDir refusal — the
      `wsClient{send: make(chan outboundMessage, 4)}` + `readTicketResult` style from
      ticket_board_test.go). `TicketDetailPanel.test.tsx`: no fetch until toggled;
      image renders data-URL img; text renders peek; fetch error surfaces.
- [ ] CHANGELOG entry.

### Slice (c) — HTML attachments in the embedded browser (gated on (b) proving used)

- [ ] Daemon listener: `internal/daemon/attachment_server.go` — `initAttachmentServer`
      (bind `127.0.0.1:0`, store addr on `Daemon`), handler
      `GET /tickets/<ticket-id>/<name>`: validate ticket via `store.GetTicket`, clean +
      contain the path under `notebook.TicketAttachmentsDir`, reject dot segments, serve
      with the CSP/nosniff/no-store headers from Boundaries. Bind failure degrades
      (no serve_url), never fatal — mirror `maybeStartDiagServer`'s posture.
- [ ] `/ws` hardening: explicit attachment-origin denial in `handleWS` before
      `isAllowedWSOrigin` (needs daemon state, so a `d.` method, not the pure func).
- [ ] Protocol: `serve_url?: string` on `TicketAttachment` in main.tsp (same regen +
      constants + ProtocolVersion ritual as slice b); populate in `sendGetTicketWSResult`
      after `ticketToProtocol` (app-only consumer; agent paths stay untouched).
- [ ] Frontend: `handleOpenTicketAttachment(serveUrl)` in App.tsx (mirror
      `handleOpenNotebookTile`; dock-or-update the `tile-browser` singleton exactly as
      daemon `handleOpenBrowser` does, via `sendWorkspaceDockTile` ignoring
      already-exists + `sendWorkspaceUpdateTile`); pass as `onOpenAttachmentInBrowser`;
      show the button only for `.html`/`.htm` attachments with a `serve_url`, and only
      under `isTauri()` (the web UI has no browser host).
- [ ] Boundary tests: `internal/daemon/attachment_server_test.go` — serves bytes with the
      exact CSP header; traversal (`../`), dotfile, unknown-ticket → 404; and the /ws
      denial: `httptest` against `d.handleWS` with `Origin: http://127.0.0.1:<attachport>`
      → 403, `Origin: null` → 403.
- [ ] Packaged-app probe (the empirical "webview cannot reach the daemon" proof): a
      real-app scenario that attaches a probe.html which attempts
      `fetch('http://127.0.0.1:<wsport>/health')` and `new WebSocket(.../ws)` and writes
      outcomes into the DOM; assert both blocked. Single-tenant, serial (AGENTS.md
      harness rules).
- [ ] One line in the skill reference `internal/agent/attn_skill/references/tickets.md`
      (attach table): a self-contained `.html` attachment renders interactively in-app —
      pull-based discoverability only.
- [ ] CHANGELOG entry.

## Verification

- `pnpm --dir app test TicketMarkdown TicketDetailPanel` (and `make test-frontend` before merge)
- `go test ./internal/daemon -run 'TicketAttachmentRead|AttachmentServer'` (scope `-run`;
  the pre-existing GitStatusScheduler race aborts bare `-race` package runs)
- After each protocol slice: `rm -rf tsp-output && make generate-types` then a tsc check
  of `app/src/types/generated.ts`
- Manual on the dev sibling (`make dev`): delegate a session, `attn ticket attach --file`
  a markdown brief, a png, and a self-contained .html; open the ticket from the board —
  markdown renders, image previews inline, HTML opens in the browser tile; from the
  served HTML's devtools-free probe page confirm the blocked fetch/WS text renders.

## Decisions

(settled with Victor — not up for re-litigation)

- **Reuse, don't add.** Markdown = the existing react-markdown + remark-gfm stack
  (CommentPopover/WorkspaceDockTile pattern); HTML = the existing browser host child
  webview. No new frontend dependency, no new renderer. (The notebook editor's `marked`
  in liveMarkdownPreview.ts is the *editor's* live-preview engine — wrong tool for
  read-only render.)
- **Strict staging with usage gates.** (a) → (b) → (c), each later slice earned by the
  earlier proving used; the ticket-pane-overlay plan is the access-pattern experiment
  underwriting all of it. A full canvas only if earned — see non-goal below.
- **Pull-based agent story.** Agents attach; humans click to render. No auto-open, no
  push-render, no new agent verbs.
- **Previews go over the ws as base64, not http.** The main webview is `tauri://localhost`
  (WKWebView treats it secure; http subresources are mixed content), and the ws read also
  works in the daemon-served web UI. The http listener exists solely because the browser
  host only navigates http/https.
- **Serve HTML from a dedicated loopback listener, never port 9849.** The daemon origin
  must remain ws-allowed for the served web UI, so same-origin serving would make CSP the
  single defense on an arbitrary-exec boundary. Two independent layers instead (CSP
  sandbox + server-side origin denial).
- **`sandbox allow-scripts` without top-navigation allowances.** Links inside a served
  HTML page won't navigate — accepted v1 restriction; diagrams/explainers/prototypes
  don't need it, and loosening later is one directive.

## Open Questions / Follow-ups

- **Deferred non-goal (recorded per brief):** a live canvas/animation runtime beyond
  sandboxed HTML — not planned; revisit only if (c) earns real usage.
- Attachment *download/reveal* (open in Finder / default app) is adjacent but out of
  scope; the existing `open_safe_markdown_target` Tauri command is the likely reuse if
  asked for.
- `attn ticket show --json` (sibling plan docs/plans/2026-07-02-ticket-show.md) exposes
  attachment paths to agents; if it later wants `serve_url` too, populate it there
  deliberately — today it is app-only by design.
- The markdown components map (headings/links/images) now has two hand-rolled variants
  (WorkspaceDockTile, TicketMarkdown). If a third consumer appears, extract a shared
  `app/src/components/markdown/` module.
