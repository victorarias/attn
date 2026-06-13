# Plan: Configurable keyboard shortcuts

## Goal

Make attn's keyboard shortcuts user-configurable, end to end:

1. **All shortcuts rebindable** — every registry entry can be re-bound or unbound (a small
   protected set must keep *a* binding), with a clickable "Restore Defaults".
2. **Leader-key chords** — depth-2 sequences (`⌘K` then a key), ~600ms window, `Esc` cancels.
3. **Config-driven, collapsible dock** — the sidebar dock is rebuilt from config (ordered list
   of shortcut IDs), reflects rebinds live, and collapses independently of the sidebar.
4. **Shortcut editor** — a dedicated modal where each shortcut is rebindable, conflicts
   auto-reassign (VSCode-style confirm), and a per-shortcut "show in dock" toggle drives the dock.

## Locked decisions (from planning)

- **Chord model**: leader-key, depth 2 (step 1 = modifier+key, step 2 = single key). ~600ms
  timeout, `Esc` cancels. Leaders are *exclusive* — a combo registered as a leader can't also
  be a standalone binding.
- **Conflicts**: auto-reassign with confirm. Binding a taken combo prompts "this unbinds X",
  and on confirm the previous holder becomes unbound (override = `null`).
- **Dock pool**: *any* shortcut is dock-eligible (requires a human `label` on every entry).
- **Footgun guard**: protect `app.quit`, the editor-opener, `ui.showShortcuts`, and Escape-class
  bindings from being left unbound. `Restore Defaults` is mouse-clickable.

## Current State (researched)

```text
registry.ts          const SHORTCUTS: Record<ShortcutId, ShortcutDef>   (~40, single-combo)
                     matchesShortcut(e, def) | validateNoConflicts() @ module load (throws)
                     ShortcutId = keyof typeof SHORTCUTS   (compile-time union, used everywhere)
useShortcut.ts       one window keydown listener (capture). per keydown: iterate SHORTCUTS,
                     first match -> preventDefault/stopProp -> fire handlers Map<Id, Set<fn>>
useKeyboardShortcuts.ts   wires ~30 ids -> App callbacks
terminalKeyHandler.ts     SECOND dispatch path: hardcoded subset re-dispatched via triggerShortcut()
formatShortcut.ts    SHORTCUTS[id] -> "⌘⇧N" (Mac glyphs); Keycap.tsx renders <kbd>
cheatsheet.ts        4 read-only categories -> ShortcutsModal.tsx (⌘/)
Sidebar.tsx:783      dock = expanded-footer only; chips are PLAIN STRINGS ("⇧G diff"),
                     deduped by string, NOT linked to ShortcutId
App.tsx:2983-3051    sidebarHeaderActions + sidebarFooterShortcuts hardcoded; shortcutHint strings
settings             frontend <-ws-> daemon <-> SQLite settings(key,value:string).
                     complex config = one JSON key (precedent: review_loop_prompt_presets).
                     ws_settings.go validates + broadcasts SettingsUpdatedMessage.
                     SetSetting/SettingsUpdated already in protocol -> NO version bump needed.
```

## Target Architecture

```text
settings['keybindings_config']  (one JSON blob; daemon validates = parseable; broadcast as today)
  -> useKeybindingsConfig() hook (like useTheme/useUIScale): parse -> feed resolver + dock + collapse
       -> resolver.setOverrides(overrides)            (module-level mutable resolved map)
       -> KeybindingsContext { config, setBinding, toggleDock, reorderDock, restoreDefaults, ... }

resolver.ts (new)
  resolve(id): Binding            override(id) ?? SHORTCUTS[id]; ignore unknown ids; per-id
                                  fallback to default if override malformed (never crash)
  resolvedList(): {id, binding}[]  used by dispatch + conflict checks + editor + dock

DISPATCH (single path for both global + terminal)
  useShortcut.ts global listener  -> dispatchKey(e, ctx)   (ctx: inTerminal, editableTarget)
  terminalKeyHandler.ts           -> same dispatchKey(...)  (no more hardcoded subset)
  dispatchKey:
    if pendingLeader: match e against `then` of bindings sharing that leader -> fire | cancel
    else: single-combo match -> fire
          | combo is a leader -> enter pendingLeader (timer ~600ms, HUD hint), preventDefault
  chordState.ts (new)  isolated pending-leader state machine + timeout + Esc cancel + HUD event

DOCK (config-driven)
  App: dockItems = config.dock.items
        .map(id => ({ id, label, tokens: formatTokens(resolve(id)),
                      active?: dockActions[id]?.isActive(), onClick?: dockActions[id]?.run }))
        .filter(applicable)              // terminal-only ids hidden when no active session
  dockActions: Record<ShortcutId, {run, isActive?}>   // only ids with interactive dock behavior;
                                                       // others render as informational chips
  Sidebar: dedup by ID (not label); independent collapse toggle reads/writes config.dock.collapsed

EDITOR
  ShortcutEditorModal.tsx (new)   overlay + FocusTrap + useEscapeStack + design tokens
    rows grouped by category(meta): label | KeyCombos(resolve) | Rebind | [show in dock] | reset
    KeyCaptureInput.tsx (new): record combo; record leader+then for chord; live conflict warn
    conflict -> validateBinding(b, excludeId) -> confirm "reassign (unbinds X)" -> setBinding +
                setBinding(otherId, null)
    Restore Defaults (button) ; protected ids can't be set to null
  entry points: ShortcutsModal "Edit shortcuts", a SettingsModal row, and its own (protected) shortcut
```

## Data Model

```ts
// registry.ts — defaults stay in code; ADD metadata (keeps ShortcutId compile-time union)
type Combo = { key: string; code?: string; meta?: boolean; ctrl?: boolean; alt?: boolean; shift?: boolean }
type Binding = Combo | { leader: Combo; then: Combo }      // chord = leader-key depth 2
type ShortcutDef = Binding & {
  label: string                 // NEW — required, human name for editor + dock + cheatsheet
  category: ShortcutCategory    // NEW — grouping in editor/cheatsheet
  editableTarget?: 'native'
  protected?: boolean           // NEW — cannot be unbound (still rebindable)
}

// persisted: settings['keybindings_config'] (JSON string, owned by frontend, daemon = passthrough+broadcast)
type KeybindingsConfig = {
  version: 1
  overrides: Partial<Record<ShortcutId, Binding | null>>   // null = explicitly unbound
  dock: {
    collapsed: boolean
    items: ShortcutId[]          // ordered membership; default derived from today's chips
  }
}
// resolver: resolve(id) = overrides[id] === null ? UNBOUND : (overrides[id] ?? SHORTCUTS[id])
//           unknown override ids ignored; malformed override -> fall back to default for that id
```

## Boundaries

- `registry.ts` owns *defaults* + metadata + the `ShortcutId` union. A broken config can never
  orphan an action id, because ids always exist in code.
- `resolver.ts` owns the merged view; it is the **only** place dispatch/format/dock/editor read
  bindings. It must tolerate any persisted blob (per-id fallback, never throw at runtime).
- `dispatchKey` (shared by global listener + terminal handler) owns matching + chord state. The
  terminal path must route through it — otherwise rebinds/chords behave differently inside the PTY
  or leak the leader keystroke (see CLAUDE.md "Terminal Focus Ownership" / PTY-input rules).
- `KeybindingsContext` owns mutation + persistence (one `set_setting` write keeps bindings + dock
  atomic). Daemon stays a dumb passthrough; **no ProtocolVersion bump**.
- `dockActions` map owns interactive dock behavior (click/active) keyed by id; the dock renderer
  knows nothing about specific shortcuts.

## Implementation Steps

PR 1 — Foundation + single-key rebinding + editor (chords/dock deferred) — DONE
- [x] Add `label`/`category`/`protected` metadata to all `SHORTCUTS` entries (`shortcuts/metadata.ts`)
- [x] `resolver.ts`: override layer, `resolveBinding`/`resolvedShortcutEntries`, runtime-safe fallbacks
- [x] Runtime `findConflict(binding, excludeId)` (keeps `ALLOWED_CONFLICT_PAIRS` for context-gated)
- [x] Route `useShortcut.ts` dispatch + `formatShortcut` + `terminalKeyHandler.ts` through resolver
- [x] Daemon: `keybindings_config` settings key (validate = valid JSON) in `ws_settings.go`
- [x] `KeybindingsProvider`/`useKeybindings` (parse/persist, feed resolver, optimistic + echo-safe)
- [x] `ShortcutEditorModal` + `KeyCaptureInput` (single-combo rebind, auto-reassign confirm,
      protected guard, Restore Defaults); entry points from cheatsheet + command menu
- [x] Tests: resolver merge/fallback/parse, conflict + reassign, protected guard, daemon validation
- [ ] Entry point from Settings (deferred — cheatsheet + command-menu cover discovery for now)
- Note: dock hint strings in `App.tsx` still memoized without the config dep, so they reflect
  rebinds only on next natural re-render. PR 2's ID-driven dock fixes live reactivity.

PR 2 — Config-driven collapsible dock + "show in dock"
- [ ] ID-driven dock model in `App.tsx` + `Sidebar.tsx`; dedup by id; `dockActions` map
- [ ] Default `dock.items` migrated from current hardcoded chips (mapped to ids)
- [ ] Independent `dock.collapsed` + collapse toggle UI in the dock header
- [ ] "show in dock" checkbox in editor -> `config.dock.items`; reorder support
- [ ] Tests: dock rebuilds from config, reflects rebinds, collapse persists

PR 3 — Leader-key chords (depth 2)
- [ ] Extend `Binding`/resolver/validation for `{leader, then}`; enforce leader exclusivity
- [ ] `chordState.ts` pending-leader machine (timeout, Esc cancel) inside shared `dispatchKey`
- [ ] Transient leader HUD hint (e.g. "⌘K …")
- [ ] `KeyCaptureInput` records leader + then
- [ ] Tests: chord match, timeout/cancel, terminal path (Playwright — capture-phase + PTY focus)

## Decisions

- **Override map over defaults, not a data-driven registry.** Smallest blast radius; preserves
  the compile-time `ShortcutId` union used app-wide; a corrupt config can't orphan an action.
- **One JSON settings key for bindings + dock + collapse.** Reuses `set_setting`/`settings_updated`
  with zero protocol/schema work, atomic multi-field writes, cross-window sync for free. A new
  daemon table (C2) would force the full protocol-versioning ritual for no current benefit.
- **Single shared `dispatchKey` for global + terminal.** The existing second dispatch path in
  `terminalKeyHandler.ts` is the most likely place for silent rebind/chord breakage and leader
  leakage into the PTY; collapsing to one matcher is required, not optional.
- **Leaders are exclusive.** A combo used as a chord leader can't also be a standalone binding —
  removes prefix/standalone ambiguity and keeps the chord engine simple.
- **Phased into 3 review-sized PRs.** Foundation+editor, then dock, then chords. Each is
  independently shippable; chords (the largest net-new logic) land last and isolated.

## Open Questions

- Default-migration policy when a *default* binding changes in a future release: user override wins
  silently (simplest) vs. surface "a default changed". Lean silent + Restore Defaults as escape hatch.
- Collapsed-sidebar (icon rail) currently hides the dock entirely. Keep hidden when collapsed, or
  show a compact dock? Lean keep-hidden for PR 2.
- Should `dockActions` informational chips (split/zoom/pane-focus — no click/active) still be
  dock-eligible? "Any shortcut" says yes; they just render as static hints.

## Follow-ups

- Fuzzy search in the editor list if it grows.
- Per-binding "changed from default" indicator in the editor.
- Consider a `view`/scope column in the editor (global vs terminal vs panel) for clarity.

## Verification notes

- Packaged-app verification required for terminal + chord cases (browser e2e can't model
  capture-phase ordering or PTY focus; cf. Cmd+C menu-intercept note in CLAUDE.md).
- `triggerShortcut(id)` and the `attn:native-shortcut` custom event already exist as test hooks.
