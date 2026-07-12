# Plan: Notebook editor polish

## Goal

Close the gap between the notebook's live markdown editor and a trustworthy daily-driver
(Obsidian-style unified editor, no View/Edit toggle). Phase 1 (this doc) is an evidence-based
gap catalog from driving the real dev app, plus a most-valuable-first PR sequence. Phase 2
implements the approved subset as small PRs, each verified in the live app.

Invariants that must survive every PR: debounce autosave + hash-CAS; conflict banner
(Reload / Keep mine); flush awaits write and aborts navigation on conflict; reloadFromDisk
stale-navigation guard; editor kind contract (.md → live editor, text → plain, binary →
never read bytes, reopen probe kind-checks); CSS-variable CM theming.

## What was verified working (live, attn-dev.app)

Driven via the real-app harness (CGEvent input + `capture_screenshot_data`), screenshots
01–33 in session scratchpad:

- Autosave → disk + "Saved" indicator; hash-CAS conflict banner both paths (Reload from
  disk restores disk content; Overwrite anyway writes buffer over disk).
- External change while clean → minimal-edit apply, viewport pixel-stable (no scroll jump).
- List continuation on Enter (lang-markdown's default keymap is active — bullets, ordered
  lists continue; static analysis had wrongly suggested otherwise).
- ⌘-click link navigation, backlinks rail, outline jump, broken-link ⚠ flagging.
- Frontmatter card → click-to-edit raw YAML.
- ⌘P finder open/filter/Enter; fresh files indexed within ~2s of creation.
- Paste of multi-line markdown (⌘V through the native menu) → verbatim insert + autosave.
- Checkbox click toggles `- [ ]` ↔ `- [x]` on disk.
- 280KB / 2500-section note: opens fast, outline complete, end-jump instant, typing lag-free.
- Plain-text editor for `.txt` (TEXT badge); binary placeholder for `.png` (no bytes read).

## Gap catalog (all confirmed live unless noted)

Ranked by user pain. "Menu trap" = AGENTS.md Critical Pattern 8 (native accelerator swallows
the key before the WebView; invisible to Playwright/jsdom).

| # | Gap | Evidence |
|---|-----|----------|
| G1 | **Undo/redo dead in packaged app.** Edit > Undo still owns ⌘Z (`app_menu()` never removes it), so CM history never sees it. Redo's menu item is already removed, but the DOM resolver routes ⇧⌘Z to `terminal.toggleZoom` — a real binding conflict, not just a menu removal. | Typed text, ⌘Z → nothing; `lib.rs` app_menu confirms Undo item present |
| G2 | **No in-editor search.** ⌘F does nothing (searchKeymap disabled in basicSetup; no native Find either). Unusable for long notes. | Shot 24 |
| G3 | **Tables render as raw pipe text.** No live-preview table rendering. | Shots 32 |
| G4 | **No syntax highlighting in code fences** (and none for inline markdown syntax — `syntaxHighlighting` disabled wholesale). | Shots (code fence flat) |
| G5 | **Images render as mangled text** — `![alt](path)` shows "image/knowledge/.../pic.png" instead of the image or a clean widget. | Shot 13 area |
| G6 | **No formatting keybindings** — ⌘B/⌘I do nothing (no bindings; ⌘B also unclaimed by menu, safe to bind). | Live probe |
| G7 | **Blockquotes and horizontal rules unstyled** — `>` quote renders as plain text with the marker visible, `---` as literal dashes. | Shot 32 |
| G8 | **Send-to-chief selection pill occludes the line above the selection.** | Shot 07 |
| G9 | **Conflict-banner buttons keep focus** — after clicking Reload/Overwrite, keyboard nav goes to the button, not the editor; typing does nothing until you click back in. | Live probe (Cmd+↓ no-op after banner click) |
| G10 | **YAML list items inside the raw frontmatter edit region get markdown • bullets** — the live-preview decorator doesn't exempt the frontmatter block while it's being edited. | Shot 16 |
| G11 | **`#fragment` links silently ignored**; bare relative links (`foo.md`) classified external while brokenLinks resolves them in-notebook — two resolvers disagree (`parseNotebookHref` vs `brokenLinks.ts`). | Code + live |
| G12 | **frontmatterCard runs full-doc `toString()` per keystroke** — O(doc) work on every edit; invisible at 280KB today but pure waste. | Code read |
| G13 | Finder index transient: right after bulk fixture creation, ⌘P missed a matching note once (opened a scattered-path match instead). Not reproducible — fresh files index in ~2s. Low confidence, watch only. | Shots 17–20 |
| G14 | `AUTOSAVE_DELAY_MS = 700` vs contract doc's 500ms — doc drift, not a bug. | Code |

Test-coverage gaps (from coverage map): conflict banner / flush-on-nav / binary reopen probe
are unit-only — the e2e `writeFile` mock hardcodes `conflict:false`, so no e2e can exercise
the banner; undo/redo, paste, tables have zero coverage anywhere; menu-trap class needs
packaged-app scenarios by definition.

## PR sequence (most valuable first, each its own small PR)

Each PR ships with unit tests where the logic is pure, and is verified live in attn-dev
(`make dev`, drive the real editor). Menu-trap PRs additionally get a packaged-app harness
scenario where feasible.

- [ ] **PR1 — Undo/redo (G1).** Remove Edit > Undo predefined item in `app_menu()`
      (`app/src-tauri/src/lib.rs`) so ⌘Z reaches the WebView; CM history (already in
      basicSetup) handles it when the editor has focus, and WebKit's native undo still covers
      plain inputs/textareas. For redo: make the ⇧⌘Z resolution context-aware in
      `useShortcut.ts` — when focus is inside a CM editor, deliver to the editor (CM redo);
      otherwise keep `terminal.toggleZoom`. Verify live: type → ⌘Z → ⇧⌘Z in the editor;
      zoom still works in a terminal pane; undo still works in the ticket comment box.
      Packaged-app scenario for the editor path (this exact class of bug is invisible to e2e).
- [ ] **PR2 — In-editor search (G2).** Enable `@codemirror/search` (searchKeymap + panel)
      in `LiveMarkdownEditor.tsx`, themed via CSS variables; scope ⌘F so it only claims the
      key when editor is focused. Esc closes the search panel *before* the surface's
      Esc-to-close handler fires (stopPropagation when panel open — Esc currently closes the
      whole fullscreen surface).
- [ ] **PR3 — Fence highlighting + blockquote/HR styling (G4, G7).** Re-enable
      `syntaxHighlighting` with a CSS-variable highlight style; add fence language support
      (`@codemirror/language-data` lazy import) and blockquote/HR decorations in
      `liveMarkdownPreview.ts`.
- [ ] **PR4 — Tables (G3).** Live-preview table widget in `liveMarkdownPreview.ts`: render
      GFM tables as a styled widget when the cursor is outside, reveal raw source on the
      cursor's row (same reveal model the rest of the preview uses).
- [ ] **PR5 — Formatting keybindings (G6).** ⌘B/⌘I (+ ⌘E inline code) toggle wrap/unwrap
      around selection or word; pure command functions unit-tested, bound via the editor
      keymap. Check combos against default-menu accelerators first (⌘B/⌘I/⌘E are free).
- [ ] **PR6 — Image rendering (G5).** Render `![...]` as an image widget (asset path
      resolved through the notebook fs surface via Tauri `convertFileSrc` or equivalent —
      must not add fs permissions beyond the notebook root) with a clean broken-image
      placeholder; raw source on cursor line.
- [ ] **PR7 — Small interaction fixes (G8, G9, G10).** Selection pill repositioned to not
      occlude the line above; conflict-banner actions restore editor focus; frontmatter
      region exempted from markdown list decoration while in raw-edit mode. Could split if
      any turns non-trivial.
- [ ] **PR8 — Link resolver unification (G11).** Single resolution helper shared by
      `parseNotebookHref` (NotebookSurface) and `brokenLinks.ts`: bare-relative resolves
      against the current note's dir; `#fragment` scrolls to the matching heading in-note
      (or at least on same-note links). Unit-test the resolver.
- [ ] **PR9 — Cheap hygiene (G12, G14).** frontmatterCard reads only the frontmatter prefix
      instead of full-doc `toString()` per keystroke; fix the 500ms→700ms doc drift.

Not planned (watch only): G13 finder transient — no repro; re-open if seen again.

## Boundaries

- `LiveMarkdownEditor.tsx` owns CM extensions/keymaps; `NotebookSurface.tsx` owns
  load/save/conflict/navigation state. New bindings must not leak save logic into the editor.
- `app_menu()` in `lib.rs` owns which keys ever reach the WebView; `useShortcut.ts` owns
  DOM-side dispatch. Every new editor shortcut gets checked against `Menu::default`
  accelerators (Critical Pattern 8) before implementation.
- The live-preview plugin (`liveMarkdownPreview.ts`) owns all decoration; new render features
  (tables, images, quotes) extend it rather than adding parallel plugins.
- Image rendering must not widen fs capability (the capability guard test bans fs
  permissions) — serve through the existing notebook read surface only.

## Decisions

- Undo fix removes the native Edit > Undo item (menu-trap pattern, same as the ⇧⌘Z zoom
  fix) rather than forwarding via `dispatch_native_shortcut` — keeps user rebinding working
  through the DOM resolver. WebKit still handles ⌘Z for plain inputs without the menu item.
- Redo requires focus-aware dispatch because ⇧⌘Z is already bound to `terminal.toggleZoom`;
  we resolve by focus context instead of moving either binding.
- Tables/images follow the existing cursor-reveal model (widget when outside, raw when on
  the line) — no split View mode, per the unified-editor invariant.
- Large-note perf work dropped from the plan: 280KB/2500 sections measured fine live; only
  the free frontmatterCard fix (G12) stays.

## Open Questions

- PR4 tables: read-only widget first, or in-widget cell editing? Proposal: read-only widget
  + raw-reveal editing first; cell editing only if it hurts in practice.
- PR2: should ⌘F fall back to a whole-vault search later? Out of scope here; in-note only.

## Follow-ups

- E2e harness: teach the `writeFile` mock to return `conflict:true` on demand so the conflict
  banner gets e2e coverage (currently unit-only).
- Packaged-app scenario for notebook editor basics (type/undo/search) to catch future
  menu-trap regressions.
- Outline rail is unvirtualized (2500 rows fine today); revisit only if notes grow 10×.
