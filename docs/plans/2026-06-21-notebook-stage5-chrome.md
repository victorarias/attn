---
title: Notebook UI Stage 5 — chrome design
status: proposed
related: docs/plans/2026-06-20-notebook-ui-gap.md
---

> Synthesized by an ultracode design workflow (sonnet readers + opus design panel/judges).
> Companion to the gap-map plan; covers gap items 12 (collapsible rails), 13 (responsiveness),
> 16 (top-bar chrome), 17 (footer), with 19/20 visual touches.

# Stage 5 — Notebook UI Chrome: Final Design

## 1. Recommendation

Build on the **prototype-faithful chrome shell** as the spine (top bar + spanning footer + manual edge-rail folds, 4 frontend-only PRs, no protocol bump) — it's the best small-PR/low-risk reading of stage 5 — but correct its one load-bearing flaw and graft the mode-machine's stage-7 seam *in shape only*. Specifically: render the footer as a **grid row** (`grid-column: 1 / -1`) inside the body grid, not a flex sibling, so it reflows flush with folding panes; write fold state in the `folded = override === null ? auto : override` tri-state shape with `auto` hardcoded `false` (zero stage-5 cost, makes stage 7 a one-boolean flip); and adopt extend-in-place's locally-derived `chiefActive` prop (one ~4-line derivation in `AppContent`, no 4-place threading). We do **not** build the `useNotebookLayout` hook or any ResizeObserver now — that 420-line engine is dormant in stage 5 and is exactly the scope-creep all three judges flagged; we ship the same user-visible behavior in a fraction of the diff and let stage 7 introduce the observer when `auto` actually has a source.

## 2. PR Slice Plan

Ordered, each independently shippable, each leaves the app working, all frontend-only, **no `make generate-types` / no `ProtocolVersion` bump**. Files are all under `app/src/components/` unless noted.

| # | Title | Scope | Key files | Size | Risk |
|---|-------|-------|-----------|------|------|
| **PR1** | Grid → CSS-var column model + footer **grid row** scaffold (static) | Convert `.notebook-browser-body` `grid-template-columns` to `var(--nb-treew,…) 1fr var(--nb-railw,…)`; add `grid-template-rows: 1fr auto`; add `.notebook-browser-footer` as `grid-column: 1 / -1` with **static placeholder** text. No behavior change yet. Also fix the harness to pass `existsFile` (declared in props, omitted today) so later PRs can mount specs. | `NotebookBrowser.css`, `NotebookBrowser.tsx`, `test-harness/harnesses/NotebookBrowserHarness.tsx` | ~180 | **Low.** The reviewable seam. Must keep panes' `min-height:0` so footer can't push them off-screen and internal scroll survives — verify in this PR. Must keep the existing `.has-rail` 2-vs-3-column distinction working through the token migration. |
| **PR2** | Manual edge-rail folds (‹ ›) for tree + context rail | Two absolutely-positioned handles inside `.notebook-browser-body`; `treeOverride`/`ctxOverride` tri-state (`null`/`true`/`false`) with `auto = false` hardcoded; `folded = override === null ? auto : override`; toggling drives `--nb-treew`/`--nb-railw` → `0px` with a CSS width transition; folded pane gets `opacity:0; pointer-events:none; aria-hidden`. Right handle hidden when `!showRail`. | `NotebookBrowser.tsx`, `NotebookBrowser.css`, `e2e/notebook-browser.spec.ts` | ~250 | **Low-med.** (a) Handle clip at the 14px radius — anchor inside body, not the rounded shell. (b) `aria-hidden` on folded pane so AT/keyboard don't tab into invisible content. (c) Use `onMouseDown`-preventDefault on handles so a click never blurs CodeMirror (focus-ownership pattern 6). No global shortcut — avoids pattern-8 menu trap. |
| **PR3** | Top-bar chrome: brand glyph + chief pulse + help overlay + placeholder mode control | Replace/extend the 72px header into the prototype's top bar (see §5 on which); brand glyph + `attn · knowledge base`; **disabled** mode segmented control (Fullscreen active, Tile disabled w/ tooltip); chief pulse from a new `chiefActive?: boolean` prop derived in `AppContent`; `?` help button → overlay pushed onto the **existing** `useEscapeStack`. | `NotebookBrowser.tsx`, `NotebookBrowser.css`, `App.tsx` (~4 lines: derive + pass one prop) | ~300 | **Low-med.** Verify the pulse against a real working chief in `attn-dev` — derivation must filter `chief_of_staff === true && state === 'working'` (never treat `undefined` as chief). Help overlay must order correctly on `useEscapeStack` (Esc closes help before modal). |
| **PR4** | Footer live data (note count) + kind badge pill | Replace footer placeholder with `🔒 saved to the attn vault` + `vault · <root> · N notes`; `N` = `.md` count from one `sendNotebookList('')` full-walk at mount, recomputed on the `changeSignal` it already receives; held in component state. Badge pill in note header from existing `fileKind()`/`entry.type`. | `NotebookBrowser.tsx`, `NotebookBrowser.css` | ~200 | **Med (count only).** Isolated here so it's droppable to "vault path only" if Victor dislikes the walk. `sendNotebookList` re-enters the `notebook_*` surface the component was repivoted off of — acceptable because it's quarantined and the only count source. Debounce/coarse the recount; don't recompute per-write on a large vault. Badge is trivial. |

Suggested start order: PR1 → PR2 are the structural spine; PR3 → PR4 are the live-data leaves. PR4's count half is the only droppable piece.

## 3. Responsiveness Model

**Stage 5 ships MANUAL FOLD ONLY. No ResizeObserver, no width→mode machine, no breakpoints.**

- Each edge-rail click sets that side's override to the opposite of the currently rendered state and persists until clicked again. The fold decision is written in the prototype's own shape:

  ```
  treeFolded = treeOverride === null ? autoTree : treeOverride
  autoTree   = false   // hardcoded in stage 5
  ```

  This costs nothing now (`auto` is a constant `false`, so collapse is purely manual, exactly matching the prototype's fullscreen `ctxAuto = tile ? … : false` semantics) but means **stage 7 only changes the source of `auto`** — it swaps the constant for `autoFold && w < THRESHOLD` fed by a ResizeObserver. We keep the tri-state instead of collapsing to a boolean precisely so the toggle handlers never need to change.

- Visual collapse is CSS-variable driven (`--nb-treew`/`--nb-railw` → `0px` with a width transition) so the pane stays **mounted** — CodeMirror/scroll state survives a fold/unfold. Never conditional-render the pane away.

- The stage-5 plan line *"narrow the window → panes fold"* means **the user clicking the rails**, not auto-fold.

**The modal-now vs tile-later (stage 7) boundary — held hard:**

| Stage 5 (modal, now) | Stage 7 (tile, deferred — DO NOT BUILD) |
|---|---|
| Manual edge-rail fold (tri-state, `auto=false`) | ResizeObserver on the tile element; `auto = autoFold && w < threshold` |
| Top bar, footer, badge pill | `wide`/`medium`/`narrow` breakpoints (1000/440) + `short` (360) height trim |
| Mode segmented control **disabled placeholder** | Functional Tile/Fullscreen mode toggle |
| — | Live `.rmode` mode-indicator pill in note header |
| — | Compact `<select>` file-picker narrow fallback |
| — | Tile-density CSS (footer 22px/10px, badge 9.5px), splitter drag |

The existing `@media (max-width: 760px)` query is **replaced by the manual model** (its own comment calls it a placeholder for stage 5). Keep the full-bleed padding collapse at very narrow widths; drop the auto `display:none` on the rail (manual fold replaces it).

## 4. Collapsible Rails

**Two distinct mechanisms coexist; neither replaces the other.**

1. **Section collapse — REUSED AS-IS, untouched.** The in-pane Tasks (left) and Outline + Backlinks (right rail) toggles use the existing caret pattern: `aria-expanded` button + CSS-triangle caret rotating 90° on `.is-open` (120ms) + pure conditional render of the body, no height animation. Stage 5 does not touch these.

2. **Pane/rail collapse — NEW (PR2).** Whole-pane edge-rail folds via the prototype's `--treew`/`--rightw` → `0px` model, ported onto the existing grid as `--nb-treew`/`--nb-railw`. Driven by class/var toggle, **not** unmount, so editor state survives. Glyph + title flip on fold state (‹↔›, Hide↔Show).

**Deliberate asymmetry to keep:** the big structural pane fold **animates** (CSS-var width transition); the tiny in-pane section toggles stay **instant**. "Big fold slides, small section snaps" — cheap fidelity win, and it matches the prototype.

The left handle always shows (tree always renders). The right handle shows **only when `showRail`** is true (markdown + note loaded) — text/binary files keep the 2-column layout with no orphan tab. This is also why the token migration in PR1 must preserve the `.has-rail` distinction: the grid must still tell "rail absent for this file type" apart from "rail present but folded."

## 5. Top-Bar Chrome & Footer

**Top bar** — persistent, present in fullscreen now and tile later. Contents left→right:
- Brand: 18px blue→amber gradient glyph + `attn` + muted `· knowledge base`.
- Mode segmented control as a **visibly disabled placeholder** (Fullscreen active; Tile disabled with a `title` like "Coming with workspace tiles"). Renders the prototype silhouette without faking a mode that doesn't exist. **No keyboard shortcut** (sidesteps the pattern-8 native-menu trap).
- Chief pulse: green pulsing dot (`@keyframes pulse` ring) + `chief: active` when active, dim dot + `chief: idle` otherwise (live two-state, not the prototype's static string).
- `?` help button → small Escape-dismissible overlay on the existing `useEscapeStack`.
- Close/esc affordance (still calls `requestClose` to flush dirty edits).

**Footer** — `.notebook-browser-footer` as the body grid's second row, `grid-column: 1 / -1`, spanning all columns so it reflows flush when rails fold (the corrected primitive). Left: lock + `saved to the attn vault`. Right (mono, final segment highlighted): `vault · <notebook root> · N notes`.

**Daemon/protocol signals — recommendation: NO protocol bump, derive client-side for both.**

- **Chief pulse:** *Pure frontend, no bump.* `chief_of_staff` is already a real `DaemonSession` boolean and `AppContent` already derives `chiefSession` (App.tsx:1583) and maps `chiefOfStaff` (App.tsx:1228). Compute `chiefActive = enrichedLocalSessions.some(s => s.chiefOfStaff && s.state === 'working')` in `AppContent` and pass **one `chiefActive?: boolean` prop**. This is the single best wiring idea in the whole panel — it avoids the 4-place `useDaemonSocket` threading (and its silent-`undefined` risk) the other two approaches carry, because the value is derived locally, not pulled fresh from the socket hook.
- **Note count:** *Pure frontend, no bump, but it's the one real data decision.* No count field exists in the protocol; `fs_list` is shallow by design. A true total of `.md` files needs a full walk — reuse the existing `sendNotebookList('')` recursive `WalkDir` once at mount, `.length`, refreshed on the `changeSignal` the component already receives. Isolated in PR4 and **relaxable** to "vault path only" if Victor doesn't want the walk. I recommend shipping the count (notebook-scale, one gated effect) but quarantining it exactly so it's a clean drop.

**Lock icon:** inline SVG, not the literal 🔒 emoji, for crispness — purely decorative, does **not** imply encryption.

## 6. Open Questions for Victor

1. **Top-bar layout: separate 42px bar vs. fold into the existing 72px header?** The prototype is a distinct 42px top bar above the body. Folding brand/chief/help into the current 72px header is the smaller diff but diverges from the two-tier silhouette. **My lean: a separate 42px bar** — PR3 is already the place for it, and it's what makes the footer-as-grid-row pay off visually (true prototype shell). Extend-in-place argued for in-place to save lines; I'd spend the lines here for fidelity. *Your call on fidelity vs. diff size.*

2. **Footer note count: ship the full-walk count, or relax to "vault · \<root\>" only?** Recommend shipping it (isolated/droppable in PR4). The only cost is one `sendNotebookList('')` walk at mount + on change.

3. **Mode segmented control: disabled placeholder now, or omit until stage 7?** Recommend the disabled placeholder — stage 7 only flips it live, and the top bar reads complete now. (Plan doesn't prescribe.)

4. **Tasks panel final home: leave in the left pane, or move into the right rail as a third section?** Stage 3 shipped it in the left pane. Stage 5 is the last window before stage 6. Recommend **leave it** — moving it is churn with no item-12/16/17 payoff. Confirm explicitly since this is the last cheap moment to decide.

5. **Chief label when no chief is working: live `chief: idle` (dim dot) vs. hide the indicator entirely?** Recommend the live two-state label over the prototype's static `chief: active` string.

6. **Fold animation: CSS width transition vs. instant?** Recommend the transition for the pane fold (keeps the "big fold slides, small section snaps" asymmetry), but it's a cheap toggle if you'd rather keep PR2 minimal.

---

**Net:** PR1 (grid+footer-row scaffold) is the reviewable seam — start there. PR2 (manual folds, written in tri-state shape) is the spine. PR3/PR4 add the live chrome data. Total ~930 lines across 4 PRs, zero protocol bump, each manually verifiable in `attn-dev.app`. The two corrections vs. the spine approach — footer as a grid row, and fold state in `override===null ? auto : override` shape — cost almost nothing now and remove the stage-7 rework both would otherwise force.
