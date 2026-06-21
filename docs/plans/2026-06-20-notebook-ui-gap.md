# Notebook UI — Prototype vs. Shipped: Gap Map

Status: ✅ **Rebuilt — all stages shipped (stage 6 cut).** See the staged list below
for what landed. The gap analysis and "Shipped (current)" snapshot that follow describe
the *pre-rebuild* baseline this map was scoped against — kept as the historical reference
point, not the current state.

**Target (prototype):** `docs/prototypes/kb-markdown-ui.html` — self-contained
interactive mock. Open in a browser to see it rendered.

**Shipped (current):**
- `app/src/components/NotebookBrowser.tsx` (+ `NotebookBrowser.css`) — the whole UI
- mounted as a fullscreen **modal** from `App.tsx` (`notebookOpen`), opened via the
  action-menu item "Browse the Notebook"
- daemon surface: `notebook_list` / `notebook_read` / `notebook_backlinks` /
  `notebook_write` / `notebook_to_chief` / `notebook_task_list` / `notebook_task_retry`
  (all in `app/src/hooks/useDaemonSocket.ts`)

**Plan reference:** `docs/plans/2026-06-13-notebook.md` → `## Markdown UI
(Obsidian-style)` (line 356) specified the prototype's design; only a trimmed
subset shipped.

---

## TL;DR — the shape is wrong, not just the details

The prototype is a **panel-based, three-pane knowledge workspace** with two display
modes (a pane that lives *inside the workspace grid beside agent terminals*, and a
wide fullscreen view), a **file tree**, and a **right context rail** (outline +
backlinks). What shipped is a **single fullscreen modal** with a **two-column**
layout: a **flat category list** on the left and a markdown view/edit pane on the
right, with backlinks tacked onto the bottom of the document. There is **no right
rail, no tree, no outline, no tile mode, no collapsible panes, no frontmatter card,
and no selection menu** — so it reads as a different product, which matches the "this
is NOTHING like I wanted" reaction.

The good news: the **majority of the gaps are frontend-only** — the daemon already
serves the data (full note list with paths, backlinks, content+hash, type). The one
genuinely large, architectural piece is **tile mode** (notebook as a workspace pane).

---

## Gap table

Verdict: **Missing** (not built), **Partial** (built but reduced), **Different**
(built differently). Build column: **FE** = frontend-only (daemon already serves what
is needed), **FE+proto** = needs a small protocol/daemon addition, **Big** =
architectural.

| # | Dimension | Prototype (target) | Shipped (current) | Verdict | Build |
|---|-----------|--------------------|-------------------|---------|-------|
| 1 | Display modes | Tile (in workspace grid, beside terminals) **and** Fullscreen, segmented toggle, file preserved across toggle | Single fullscreen **modal** only | **Missing** (tile) / **Different** (fullscreen=modal) | **Big** (tile) / FE (full) |
| 2 | Overall layout | **Three panes**: tree │ note │ context, + footer spanning all | **Two columns**: list │ document | **Different** | FE |
| 3 | Left pane | **File tree**: nested folders, chevrons, expand/collapse | **Flat list** bucketed into 7 fixed category groups, no nesting | **Different** | FE |
| 4 | Left-pane item | icon + **kind dot** (journal=amber / memory=blue / plain) + name | 3px type **marker bar** + title + path | **Partial** | FE |
| 5 | Right pane | **Context rail**: Outline + Backlinks, each collapsible | **None** | **Missing** | FE |
| 6 | Outline (TOC) | jump-to-heading list (h1/h2/h3 indented) | **None** (heading slug ids exist, unused for TOC) | **Missing** | FE |
| 7 | Backlinks | In the **right rail**, card per source: mono path + context snippet | `<section>` at **bottom of document**, title-only buttons | **Different** | FE |
| 8 | Frontmatter | **Frontmatter card**: title, summary, tag chips, sources (internal/external), created/updated meta | Raw content rendered through markdown; only title/path in header | **Missing** | FE (+proto optional) |
| 9 | Broken links | In-notebook link to a missing note flagged red with ⚠ | Not detected; click just navigates (daemon 404s) | **Missing** | FE |
| 10 | View / Edit | Per-note **View/Edit** segmented toggle in note header | "Edit" button → textarea editor (same capability, different control) | **Partial** | FE |
| 11 | Editor + conflict | textarea + Save/Cancel + save-status (ok/conflict) | textarea + Save/Cancel + conflict reconcile ("reload" / "overwrite") | **Shipped** (parity ✓) | — |
| 12 | Collapsible panes | Edge **rails** (‹ ›) fold tree/context; manual in full, auto in tile | Only the **Tasks** section collapses; panes never fold | **Missing** | FE |
| 13 | Width responsiveness | Tile responds to **its own width** (ResizeObserver): wide→3-pane, medium→tree+note (right auto-folds), narrow→**file-picker**+note (tree folds), short→trimmed chrome; live mode indicator | Only a window media query at 760px shrinks the left column | **Missing** | FE |
| 14 | Selection → chief | Floating **"Send to chief"** button **+ context menu** (Send to chief ⌘⏎ / **New memory note from selection** / **Copy link**) | Floating "Send to chief" button **only** | **Partial** | FE (+proto for new-note path) |
| 15 | Confirmations | **Toast** stack (bottom-right) with snippet, accent/warm variants | Transient inline status badge in the doc header | **Different** | FE |
| 16 | Top chrome | Persistent **top bar**: brand glyph, mode segmented control, **chief: active** pulse, help | Modal header: logo + "Notebook" + Close (esc) | **Different** | FE (+signal for chief status) |
| 17 | Footer | KB footer spanning panes: "saved to the attn vault" + "N notes" path status | None | **Missing** | FE |
| 18 | Tasks panel | Not in the KB (prototype only mocks terminals); background work is just terminal output | **Tasks** runner panel (collapsible) in the left pane: state badges, attempts, next-attempt, retry, errors | **Shipped beyond proto** (keep; decide placement) | — |
| 19 | Kind / type system | journal vs memory vs plain, expressed as dots + header **badge pill** | `entry.type` drives only the marker bar color | **Partial** | FE |
| 20 | Visual language | Obsidian-ish: tree gradient panels, fm card, chips, mono paths, density differs tile vs full | attn settings-style panels; flat groups; single density | **Different** | FE |

---

## What this means for effort

**Frontend-only (daemon already serves the data):** items 2–10, 12, 13, 15, 17, 19,
20. This is the bulk of the redesign. The daemon's `notebook_list` returns every
note's full path (build the **tree** client-side by splitting paths), `notebook_read`
returns content+hash (parse the **frontmatter** block and headings for the **card**
and **outline** client-side), `notebook_backlinks` already feeds the **right-rail
backlinks**, and the full note list lets us flag **broken links** by cross-reference.
None of these need a protocol bump.

**Small protocol/daemon additions (optional, for cleanliness):**
- Item 8: a structured `frontmatter` object on `NotebookReadResult` (the daemon
  already parses title/type, so it has the YAML) — avoids re-parsing client-side. Not
  required; frontend can parse it.
- Item 14: "New memory note from selection" needs a target path/name; `notebook_write`
  to a new path already exists, so this is mostly a frontend new-note flow.
- Item 16: a "chief active" signal for the pulse indicator (if not already broadcast).

**The one big rock — item 1, tile mode:** rendering the notebook as a **pane type
inside the workspace layout** (beside agent terminals, with the existing split/resize
machinery and its own width-responsiveness) is architectural. It touches the
workspace pane/layout system (`SessionTerminalWorkspace/`, the layout model) and
likely needs layout **persistence** for the new pane type. This is the piece that
makes the notebook "live where you work" instead of being a modal you pop open. It is
separable from the fullscreen redesign and should probably be its own phase.

**Already at/above parity (keep):** editor + hash-CAS conflict reconcile (item 11),
live `notebook_changed` refresh, and the **Tasks runner panel** (item 18) — which the
prototype never had. Decide whether Tasks stays in the left pane, moves to the right
rail as a third section, or becomes its own surface.

---

## Hard requirement: one mode (read + type together)

The current **View/Edit toggle is removed**. There is a single surface that is both
readable (rendered markdown) and directly typeable — no "switch to edit". The chosen
editing model (Live Preview vs WYSIWYG vs highlighted source) is the one open
decision below; everything else in the order is independent of it. The notebook's
files must stay plain canonical `.md` (external-sync invariant in
`2026-06-13-notebook.md`), so the editor's source of truth stays raw markdown.

## Staged rebuild order — verifiable at every stage

Each stage is a small PR that leaves the app working and is **manually verifiable in
the running app on its own**. We transform the existing component in place (no
big-bang rewrite), so there is always something to open and check. Order is by
dependency + "address the loudest wrongness first", not by the prototype's phasing.

1. **One mode: read + type together.** ✅ **Done.** Replaced the view/edit split with
   a single CodeMirror 6 live-preview surface (`notebook/LiveMarkdownEditor.tsx` +
   `notebook/liveMarkdownPreview.ts`) in the *current* layout; deleted the View/Edit
   toggle and the rendered/textarea split; edits now **autosave** (debounced, with a
   navigate/close flush) over the same hash-CAS + conflict reconcile, and a selection
   feeds the existing "Send to chief". Headings, bold/italic/code/strike, and links
   render inline with markers hidden off the cursor line; an unfocused editor reads
   fully clean. *Deferred to stage 4 polish:* list bullets/checkboxes and fenced-code
   styling (raw `- ` / ``` still show). CM can't mount under happy-dom, so editor
   behavior is covered by Playwright harnesses (`e2e/live-markdown-editor.spec.ts`,
   `e2e/notebook-browser.spec.ts`); the decoration logic has a headless `EditorState`
   unit test, and the surrounding orchestration is unit-tested with the editor mocked.
   *Verify:* open a note → it renders **and** you can click anywhere and type → edits
   persist; there is no mode toggle.
2. **Left becomes a file tree.** ✅ **Done.** Replaced the flat category list with a
   lazy nested folder tree, and repivoted the surface off `notebook_*` onto the generic
   filesystem API (`fs_list`/`fs_read`/`fs_write`) so any text file is editable. (#366
   daemon, #367 FE/FileTree, #368 rewire.)
   *Verify:* folders nest and fold; clicking a file opens it; dots reflect kind.
3. **Three-pane shell + right context rail.** ✅ **Done.** Added the context rail with an
   **outline** (jump-to-heading, parsed from the live draft) + backlinks moved into it.
   (#370.)
   *Verify:* outline lists the note's headings and scrolls to them on click;
   backlinks now live in the rail; three columns render.
4. **Frontmatter card + broken-link flags + markdown polish.** ✅ **Done.** Markdown
   polish — list bullets, task checkboxes, fenced code (#371); an in-editor frontmatter
   card (#372); the title = first-H1 rule, retiring frontmatter `title:` (#373); and
   broken-link flags via a new `fs_exists` primitive (#375).
   *Verify:* a card shows summary/tags/sources/meta; an in-notebook link to a
   missing note is flagged.
5. **Collapsible rails + width responsiveness + footer + top-bar chrome.** ✅ **Done.**
   Foldable tree/rail (folded panes go `inert`, stay mounted; grid column animates to 0)
   + header chrome (chief pulse + kind badge). (#376.) **Trimmed after testing:** the
   footer, the disabled fullscreen/tile-mode control, and the `?` help popover were
   removed — they only advertised pre-tiling features.
   *Verify:* fold the tree/rail via the edge handles; narrow the window → panes fold.
6. **Selection menu + toasts.** ❌ **Cut (2026-06-21).** The selection context menu
   (menu-ified Send to chief / New note from selection / Copy link) was dropped as
   unnecessary: the floating "Send to chief" pill already covers the real need, and
   "new note from selection" conflicts with the chief-curated knowledge base (promotion
   is a keeper job, not a reader action). The bottom-right toast stack was deferred — the
   existing inline "Saved"/status indicator stays as-is. No code change.
7. **Tile mode (the one architectural piece).** ✅ **Done.** Notebook as a workspace tile
   (reuses attn's tile split-tree / dock / resize / persist, no protocol bump) — 5 PRs
   (#378–#383): `notebook` TileKind, NotebookSurface `modal|tile` variant, width
   auto-fold, render + entry (⌘⌥N tile / ⌘⌥⇧N fullscreen), and a tile-local ⌘P fuzzy
   finder. Follow-ups: #384 (packaged finder scenario + ⌘P-after-Esc focus fix), #386
   (⌘P finder in the fullscreen modal + a sidebar button; dropped the fullscreen entry
   from ⌘K).
   *Verify:* open the notebook as a tile inside a workspace; resize it; ⌘⌥⇧N for fullscreen.

Tasks runner panel (item 18) stays through all stages; it gets a final home in stage
3 or 5 (left pane vs right-rail third section) — decide when the rail exists.

## The one decision that shapes the build — DECIDED

**Editing model for stage 1: Live Preview (Obsidian-style).** Type raw markdown,
rendered inline as you go; raw syntax reveals only on the cursor's line. Source of
truth stays plain `.md` (honors the external-sync invariant). Implementation:
CodeMirror 6 with markdown language support + live-preview decorations (hide syntax
markers off the active line, size headings, style bold/italic/code, make links
clickable, render list bullets/checkboxes). Rejected: WYSIWYG (lossy `.md`
round-trip) and highlighted-source-only (not "rendered reading").

Lower-stakes decisions, defaultable as we go: fullscreen stays a modal vs becomes a
real app view (lean: real view); frontmatter parsed client-side vs a new read-result
field (lean: client-side); tree derived client-side from paths vs daemon-returned
(lean: client-side — no protocol change).
