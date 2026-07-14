# Plan: Markdown reader + annotator

Revised 2026-07-14 after an adversarial gap-check of the first draft against the
plannotator source (two independent deep-read passes: rendering, annotation).
Corrections from that pass are folded in throughout.

## Goal

Make attn the best markdown reader and annotator for working with agents:

1. Cmd+click a `.md` path in a session terminal opens it in a markdown tile.
2. The tile renders markdown beautifully (plannotator-grade reading quality).
3. The user annotates everything — anchored comments, redlines, quick-labels,
   global comments — and sends the feedback to a specific agent session in one
   turn, via the nudge/doorbell mechanism. No blocking approve/deny loop; that
   is Present's realm.

## Research summary

### Plannotator (clone at ~/projects/plannotator)

What makes it great, distilled — with the key correction that its reading
quality does NOT come from a standard markdown stack:

- **The renderer is entirely hand-rolled** (`parseMarkdownToBlocks` +
  `BlockRenderer.tsx` + a ~1,100-line inline scanner `InlineMarkdown.tsx`).
  Most of the reading-quality features live in that inline scanner, not in CSS:
  smart punctuation (curly quotes, `...`→…, `---`→— with a deliberate carve-out
  so CLI flags like `--watch` are never mangled), emoji shortcodes, bare-URL
  linkification with balanced-paren tail-trimming, wiki-links, link protocol
  sanitization (`javascript:`/`data:`/`file:` → plain text), file-path
  detection. None of this comes free from react-markdown + remark-gfm.
- **Typography tuned for prose**: width-capped (832px) centered `bg-card
  rounded-xl` card with responsive padding (`py-5 md:py-8 lg:py-10 xl:py-12`);
  body paragraphs `text-[15px] leading-relaxed text-foreground/90`, list items
  14px, headings `tracking-tight` with opacity de-emphasis (`h2
  text-foreground/90`, `h3 /80`) and rhythm `mt-6/mt-8` + `first:mt-0`; fonts
  **Inter + JetBrains Mono** (not Geist Mono) with Inter feature settings
  `"ss01","ss02","cv01"`; themed `::selection`, thin themed scrollbars,
  color-only global transitions (no transform/opacity — scroll jank),
  `prefers-reduced-motion` support.
- **Block features**: highlight.js code blocks (github-dark) with hover copy
  button; GitHub alerts (`[!NOTE]` etc.) with real Octicons and per-kind
  light/dark color tables; `:::` directive callouts; YAML frontmatter stripped
  and rendered as a metadata card (and `contentStartLine` tracked so line
  anchors stay correct); GitHub-slug heading anchors with dedup + smooth
  scroll that offsets the sticky bar and scrolls the container (not window);
  interactive task-list checkboxes with `tabular-nums` ordered markers;
  tables with `overflow-x-auto`, header/hover tinting, and a popout dialog
  (sort/filter/CSV export); image lightbox with `imageBaseDir`-relative
  resolution; mermaid with zoom/pan/fullscreen; KaTeX math with a "money
  guard" so `$5-$10` prose is never parsed as math; raw HTML blocks set via
  imperative innerHTML so a user-opened `<details>` never collapses on
  re-render.
- **Annotation engine decoupled from the renderer**. The renderer guarantees
  two invariants: every block carries `data-block-id`, and inline content is
  real DOM text nodes. `useAnnotationHighlighter.ts` runs their
  `web-highlighter` fork on top: select any text (across blocks) → wrapped in
  `<mark class="annotation-highlight">`.
- **Dual anchoring**: visual highlights anchor by DOM path
  (`startMeta`/`endMeta`) + `originalText`, with a 3-tier text-search fallback
  (exact single-node → cross-node full-text offset mapping →
  whitespace-normalized index map, with a retry through the smart-punctuation
  transform since rendered text ≠ source bytes). Char offsets are explicitly
  legacy. Line numbers are NOT stored on the annotation — they're computed at
  export time from the anchored block.
- **Annotation types**: COMMENT (anchored), DELETION (redline — created
  instantly on selection in redline mode, no popover), GLOBAL_COMMENT (a
  header-button flow, not a mode), quick-labels (a COMMENT with
  `isQuickLabel` + the emoji+label baked into `text`, plus an optional `tip`
  string injected into the payload — the tips are load-bearing prompt
  engineering, e.g. "This seems like an assumption. Verify by reading the
  actual code before proceeding."). Keybindings: `Alt+1..0` applies label
  N from the toolbar; bare digits work when the picker is open; a fixed 👍
  button and a ⚡ picker button sit in the toolbar. No insert/replace types —
  a replacement is a comment. Code blocks are annotated as a whole via a
  hover toolbar, not by text selection.
- **UX sauce worth porting**: floating selection toolbar (center-above for
  prose, top-right for code; closes when the anchor scrolls out; 0.15s
  in/out animations), type-to-comment with a careful guard set (ignores
  IME composition, editable targets, modifier combos, multi-char keys),
  comment popover with Cmd/Ctrl+Enter submit, click-outside protection when
  dirty, popover↔dialog expand, off-screen "jump back" pill, module-level
  draft store surviving unmount; sidebar list sorted by document order with
  focus-glow scroll (2s glow, skipped for the just-created annotation);
  quick-label picker positioned at the mouseup cursor.
- **Payload is structured markdown, not JSON** (`exportAnnotations()`),
  items sorted by document position, exact shapes below. The forceful
  "YOUR PLAN WAS NOT APPROVED" preamble belongs to the plan-deny
  (ExitPlanMode) path only — the annotate-a-file path uses a mild
  `annotateFileFeedback` framing, which is the right model for our
  non-blocking nudge.
- **Skip**: their transport (hook + local HTTP server + blocking
  waitForDecision), share portals, code-review/diff stack, pinpoint input
  mode (orthogonal input axis; dropping it also drops the Alt-hold gesture —
  confirmed not load-bearing), math special-casing (~40% of the highlighter
  hook), mobile/touch bridges, collaborative `author` identity, the
  `createdA` typo and legacy offset fields.

### attn today

- **Markdown tile exists end-to-end**: `attn open <file.md>` → `CmdOpenMarkdown`
  → `handleOpenMarkdown` (`internal/daemon/tilecontent.go`) docks a single
  stable `tile-markdown`, pushes content via `workspace_tile_content`,
  live-reloads on a 750ms disk watcher. Rendered by `WorkspaceDockTile.tsx` via
  the shared `<Markdown>` (react-markdown + remark-gfm + mermaid).
- **But the reader baseline is near zero**: no syntax highlighting, no copy
  buttons, no callouts, no frontmatter handling, no heading anchors, no
  lightbox, no smart typography, no width cap/type scale, thin mermaid.
  Every reading-quality item above is a from-zero addition, not a tweak.
- **Cmd+click plumbing 95% there**: `.md` paths already detected/linkified
  (`terminalLinks.ts`); the single chokepoint `openLink` in
  `GhosttyTerminal.tsx` currently routes paths to the OS opener
  (`@tauri-apps/plugin-opener`).
- **Delivery template**: Present's `handbackPresentationRound`
  (`internal/daemon/present.go`) — `typeDoorbell(sessionID, text)` (bracketed
  paste + Enter; best-effort, silently skipped when the session is
  `pending_approval`) with a durable ticket-comment fallback.
- **Session resolution**: `attn open` resolves via `ATTN_SESSION_ID`; terminal
  panes carry their `SessionID`; `FindWorkspaceLayoutPaneBySessionID` maps
  session → workspace/pane.

## Design decisions (settled with Victor, 2026-07-14)

1. **Session binding**: bind at open time. `attn open` binds to the opening
   agent's session; cmd+click binds to the clicked terminal pane's session.
   The binding is stored on the tile and shown in the tile header with a
   dropdown to retarget among the workspace's sessions. "Send" always goes to
   the bound session — deterministic, visible, overridable.
2. **Annotations are daemon-persisted per file** (like Present rounds): drafts
   survive tile close, app restart, and reopening the same doc later.
3. **Multiple markdown tiles** are allowed. Opening a file already shown in a
   tile in that workspace focuses/reuses that tile; a new file docks a new
   tile. Tile IDs become dynamic (derived, e.g. `tile-markdown-<hash(path)>`),
   replacing the single stable `tile-markdown`.
4. **v1 annotation scope**: comment, redline, global comment, quick-labels
   (Alt+1..0 + fixed 👍 + ⚡ picker), floating toolbar, type-to-comment,
   sidebar list. Deferred: pinpoint mode, image attachments, math.
5. **No blocking loop**: submit = format payload + doorbell the bound session.
   If the session is `pending_approval`, surface it in the UI (retry/queue),
   never silently drop. Ticket-bound sessions can also get the durable
   ticket-comment path.

## Architecture

### Rendering: react-markdown + plugins, honestly scoped

We keep react-markdown (attn already uses it; remark gives true GFM parsing
and `node.position.start.line` for free, which beats plannotator's line
scanner for anchoring). But the gap-check makes clear this is NOT "typography
+ a rehype pass" — each plannotator feature is a plugin or custom renderer we
add deliberately:

- **Anchoring invariants**: rehype pass stamping `data-block-id` +
  `data-source-line`/`data-source-line-end` on top-level blocks (and each
  `<li>`, so list items anchor individually like plannotator's per-item
  blocks). Must account for frontmatter stripping so line numbers reflect
  the raw file (plannotator's `contentStartLine` lesson).
- **Code**: highlighting via rehype/highlight.js (or Shiki, already in the
  app via Present) + hover copy button.
- **Callouts**: `[!NOTE]`-style GitHub alerts (remark plugin or custom
  blockquote renderer) with Octicons and light/dark palettes; `:::`
  directives via `remark-directive` (v1 optional).
- **Frontmatter**: `remark-frontmatter` + metadata card renderer.
- **Headings**: GitHub-style slugs with dedup, anchor links, smooth scroll
  targeting the tile's scroll container with sticky-header offset.
- **Prose transforms**: smartypants + emoji shortcodes — but ported with
  plannotator's narrowed en-dash rule (never rewrite `--flags`). NOTE: these
  make rendered text differ from source bytes; the annotation text-search
  must search transformed text (see below).
- **Links/safety**: protocol sanitization (`javascript:`/`data:`/`file:` →
  plain text), `target=_blank rel=noopener` for external, bare-URL
  linkification with balanced-paren tail-trim (verify remark-gfm's autolink
  behavior; patch where it differs), relative `.md` links open in a tile
  (follow-up), relative images resolved against the doc's directory via
  Tauri `convertFileSrc`.
- **Task lists**: styled interactive-looking checkboxes, `tabular-nums`
  ordered markers (read-only in v1 — no write-back).
- **Tables**: `overflow-x-auto` wrapper + header/hover tinting (popout dialog
  deferred).
- **Images**: lightbox (portal, Escape/click-out, alt caption).
- **HTML blocks**: rehype-raw + rehype-sanitize; `<details>` open-state must
  survive live reload (see re-render gating below).
- **Typography**: the exact plannotator values (832px card, 15px/1.55 body,
  heading rhythm and opacity de-emphasis, Inter + JetBrains Mono with
  `ss01/ss02/cv01`, selection/scrollbar/reduced-motion treatment), adapted to
  attn's theme variables.
- **Deferred**: KaTeX (when added, port the money-guard heuristics), mermaid
  zoom/pan/fullscreen (attn's basic mermaid stays for v1), print stylesheet,
  table popout.

### Anchoring (the foundation — decided 2026-07-14)

Plannotator anchors by DOM path (`parentTagName/parentIndex/textOffset`) with
text-search fallbacks — fragile by construction, held up by their flat
hand-rolled DOM. We do NOT copy it. We adopt the W3C Web Annotation selector
model (the Hypothesis approach, battle-tested on documents that change), and
exploit a property plannotator doesn't have: **our render is a pure function
of file content**.

- **Anchor record** (text space, never DOM paths):
  `{blockId, startLine, endLine, exact, prefix, suffix, start, end,
  contentHash}` — `exact` is the selected rendered text; `prefix`/`suffix`
  ~32 chars of rendered context (TextQuoteSelector, disambiguates duplicate
  text); `start`/`end` are offsets into the block's normalized rendered text
  (TextPositionSelector — immune to text-node splitting by highlight spans
  and to react-markdown's DOM shape); `contentHash` is the file content the
  anchor was created/last rebased against.
- **Re-anchor state machine keyed by content hash** (not per-render fuzz):
  - Hash unchanged (reopen, restart, re-render): stored offsets are exact by
    construction — zero heuristics on the common path.
  - Hash changed (agent edited the file): one fuzzy re-anchor per change —
    find `exact` with prefix/suffix scoring, transform- and whitespace-
    normalized tiers, nearest-to-old-position wins — then **re-baseline**:
    rewrite offsets/lines/hash against the new content and persist. Fuzz
    never compounds across edits.
  - No confident match: annotation becomes **orphaned** — flagged in the
    sidebar with its quote, still sendable ("~line N (moved)" label), never
    silently painted on the wrong text.
- **Painting is a separate, swappable layer** behind the anchor interface.
  Preferred: **CSS Custom Highlight API** (`CSS.highlights` + `::highlight()`,
  WKWebView ≥ Safari 17.2) — paints resolved Ranges with zero DOM mutation,
  eliminating the React-vs-foreign-DOM conflict under the 750ms live reload
  entirely. Supports background/color/line-through (all we need for
  comment/redline). Click/hover use point-in-range hit-testing. Fallback if
  it disappoints: mark-insertion (plannotator-style) behind the same
  interface. Either way, re-renders stay gated on content hash (notebook
  reload lesson).
- **Pure core module, tested before any UI**: `(content, anchor) →
  resolution` and `(oldAnchor, newContent) → rebased | orphan` as pure
  functions with a brutal fixture suite — duplicated paragraphs, the
  annotated sentence rewritten, line shifts from insertions above,
  smart-punctuation transforms, selections spanning inline code/emphasis.
- **Known limitation (shared with plannotator)**: payload line labels are
  block-granular and can drift between send and the agent reading the file —
  hence the payload always embeds the quoted text; lines are a hint, the
  quote is the contract.

### Annotation layer

- Study `useAnnotationHighlighter.ts` for interaction logic, but the engine
  is ours (anchor model above); `@plannotator/web-highlighter` is not used
  unless the mark-insertion fallback is needed. Math branches (~40% of their
  hook) dropped with the math deferral.
- **Model** (fresh, diverging from plannotator's legacy fields):
  `{id, type: comment|deletion|global, text?, anchor?, quickLabelId?,
  quickLabelTip?, createdAt}` — `anchor` as above (absent for global).
  Unlike plannotator (which stores `blockId` and computes lines at export),
  lines live in the anchor and are kept fresh by re-baselining. Quick-labels
  are a structured field, not a string baked into `text` (plannotator
  reverse-matches on the literal "emoji text" string — don't copy that).
- **Interaction details to port faithfully**: redline creates instantly on
  selection (no popover); type-to-comment with the full guard set (IME,
  editable targets, modifiers, key length 1); popover Cmd/Ctrl+Enter submit,
  click-outside blocked when dirty, draft text survives popover unmount;
  toolbar center-above for prose / top-right for code, closes on scroll-out;
  quick-label picker at the mouseup cursor with one-tick-deferred dismiss;
  sidebar sorted by document position with focus-glow scroll (skip scroll for
  just-created); `exceptSelectors` extended with attn chrome (tile header,
  session picker, sidebar) so selecting UI never creates annotations;
  highlight CSS incl. the `padding:0 2px; margin:0 -2px` trick that avoids
  text shift; Cmd/Ctrl+Enter as the global "send annotations" shortcut.
- **Code blocks**: adopt plannotator's whole-block annotation via hover
  toolbar, but implement it react-side (overlay/class on the block, not
  `innerHTML` destruction of highlight spans). In-code text selection also
  works (code is not in exceptSelectors).
- **Overlaps**: allowed (nested marks), matching plannotator; no undo/redo
  (plannotator has none either — sidebar delete is the undo).

### Persistence

- Daemon-side store keyed by absolute file path (+ workspace), holding draft
  annotations; CRUD over new ws commands; cleared on send.
- **Generation tombstoning is required**: frontend saves are debounced, so a
  late save can land after clear-on-send and resurrect ghost drafts.
  Every save carries a generation; the daemon rejects saves with generation ≤
  the tombstoned one. (Directly from plannotator's `useAnnotationDraft`
  contract — their most subtle correctness rule.)

### Delivery

- New ws command `markdown_annotations_submit {path, targetSessionID}` (drafts
  already live daemon-side). Result event reports delivered /
  skipped-pending-approval.
- **Payload**: plannotator's `exportAnnotations` format exactly, with
  `subject: "document"` and the mild annotate framing (NOT the plan-deny
  preamble — that belongs to blocking approve/deny flows):

  ```
  # Markdown Annotations

  File: <abs path>

  I've reviewed this document and have N pieces of feedback:

  ## 1. (lines 12–18) Feedback on: "the selected text"
  > the reviewer's comment

  ## 2. (line 30) Remove this
  ```
  the deleted original text
  ```
  > I don't want this in the document.

  ## 3. (line 5) [👍 Looks good] Feedback on: "selected text"
  > optional quick-label tip

  ## 4. General feedback about the document
  > a global comment

  ---
  ## Label Summary
  - **👍 Looks good**: 2

  Please address the annotation feedback above.
  ```

  Items sorted by document position (not creation order); single-line label is
  `line N`, ranges use an en-dash `lines A–B`. Quick-label tips ported
  verbatim minus plannotator-specific references (e.g. the
  `~/.plannotator/plans` path in "Consider alternatives").

## PR sequence (small PRs)

1. **Cmd+click `.md` → tile**: `openLink` branch in `GhosttyTerminal.tsx`
   routes `.md`/`.markdown` paths to `open_markdown` with the pane's session
   id; tile params gain the bound session; dynamic tile IDs + reuse-if-open
   semantics (multiple tiles). Protocol bump.
2. **Reader core**: typography (card, scale, fonts) + anchoring rehype pass
   (`data-block-id`/`data-source-line`, frontmatter-aware) + syntax
   highlighting + copy button + heading anchors/slugs + link sanitization +
   frontmatter card. Frontend-only.
3. **Reader polish**: callouts, task-list/table styling, lightbox +
   relative-image resolution, smart punctuation/emoji (with annotation-search
   implications), details-state-safe HTML blocks, content-hash re-render gate.
4. **Anchoring core**: the pure anchor/re-anchor module + fixture suite, the
   rehype block/offset integration, and a paint-layer spike (CSS Custom
   Highlight API vs mark-insertion) proven against live reload. No UI yet.
5. **Annotation UI (local)**: selection toolbar, comment/redline/global/
   quick-labels, popover, sidebar, orphan display. Daemon draft persistence +
   CRUD commands with generation tombstoning.
6. **Send**: submit command, payload formatter, doorbell delivery,
   pending_approval surfacing, session picker in tile header, Cmd+Enter.

Each PR gets live-app verification on a throwaway profile per AGENTS.md.

## Open items

- Exact quick-label set and whether labels are user-configurable (v1: fixed
  set mirroring plannotator's defaults, tips rewritten where
  plannotator-specific).
- Whether sent annotations are archived (history view) or just cleared.
- Relative-link navigation inside rendered markdown (open sibling `.md` in a
  tile) — natural follow-up once multiple tiles exist.
- CSS Custom Highlight API ergonomics in WKWebView (hit-testing, styling
  limits) — resolved by the PR 4 paint-layer spike; mark-insertion is the
  fallback behind the same anchor interface.
- Mermaid zoom/pan/fullscreen and table popout as later polish.
- **Fix md path links inside dot-directories.** Found during PR 1 live
  verification: the Tauri fs scope (`app/src-tauri/capabilities/default.json`)
  allows only `$HOME/**`, and its glob does not match dot-directory segments —
  so the path-link existence check (`exists()` in `GhosttyTerminal`) silently
  fails for anything under a hidden dir, and cmd+click never linkifies paths
  like `~/.claude/plan.md` or `~/.config/foo.md`, which agents print all the
  time. Fix is widening the fs scope (e.g. dot-dir-tolerant glob entries or
  `require_literal_leading_dot: false`) while keeping it $HOME-bounded, then
  extending `scenario-terminal-md-link` with a dot-dir fixture.
