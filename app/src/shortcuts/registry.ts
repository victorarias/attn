// app/src/shortcuts/registry.ts

import { isMacLikePlatform } from './platform';

export interface ShortcutDef {
  key: string;
  code?: string;
  meta?: boolean;
  shift?: boolean;
  ctrl?: boolean;
  alt?: boolean;
  editableTarget?: 'native';
}

/** A single key combo. Alias of ShortcutDef so chord code reads naturally. */
export type Combo = ShortcutDef;

/**
 * A leader-key chord: press `leader`, then `then` within the timeout. Depth is
 * fixed at two — one leader plus one follow key — which covers the useful space
 * without a parser or nested timeout bookkeeping.
 */
export interface Chord {
  leader: Combo;
  then: Combo;
}

/** A bound action is either a single combo or a two-step chord. */
export type Binding = Combo | Chord;

export function isChord(b: Binding | null | undefined): b is Chord {
  return !!b && typeof b === 'object' && 'leader' in b && 'then' in b;
}

const ALLOWED_CONFLICT_PAIRS = new Set([
  'session.close|terminal.close',
  // One verb — send the annotations you just made to the session — on one key,
  // reached from two surfaces. Both registrations are gated on their own
  // surface having focus, and a focused markdown tile and a focused terminal
  // pane are mutually exclusive, so only one handler is ever registered.
  'markdown.sendAnnotations|terminal.sendAnnotations',
]);

export const SHORTCUTS = {
  // App
  'app.quit': { key: 'q', meta: true },

  // Terminal panel
  'terminal.open': { key: '`', meta: true },
  'terminal.collapse': { key: '~', shift: true },  // Shift+` produces ~ on US keyboards
  'terminal.splitVertical': { key: 'd', meta: true },
  'terminal.splitHorizontal': { key: 'd', meta: true, shift: true },
  'terminal.toggleZoom': { key: 'z', meta: true, shift: true, editableTarget: 'native' },
  'terminal.toggleMaximize': { key: 'Enter', meta: true, shift: true },
  'terminal.close': { key: 'w', meta: true },
  'terminal.focusLeft': { key: 'ArrowLeft', meta: true, alt: true },
  'terminal.focusRight': { key: 'ArrowRight', meta: true, alt: true },
  'terminal.focusUp': { key: 'ArrowUp', meta: true, alt: true },
  'terminal.focusDown': { key: 'ArrowDown', meta: true, alt: true },

  // Find in terminal scrollback. In editable targets ⌘F belongs to the focused
  // editor (the notebook editor's CodeMirror search); terminal find only makes
  // sense when focus isn't in a text field.
  'terminal.find': { key: 'f', meta: true, editableTarget: 'native' },

  // Session management
  'session.new': { key: 'n', meta: true },
  'session.newHorizontal': { key: 'n', meta: true, shift: true },
  'session.newWorkspace': { key: 't', meta: true },
  'session.close': { key: 'w', meta: true },
  'session.prev': { key: 'ArrowUp', meta: true, editableTarget: 'native' },
  'session.next': { key: 'ArrowDown', meta: true, editableTarget: 'native' },
  // Grid view moved Home (go-to-dashboard) off ⌘G to ⌘⇧H so ⌘G can toggle the grid.
  'session.goToDashboard': { key: 'h', meta: true, shift: true },
  'view.toggleGrid': { key: 'g', meta: true },
  'session.jumpToWaiting': { key: 'j', meta: true },
  // Close the turn on the selected session. Only bound while the queue
  // arrangement is on: with the band hidden the keystroke has nothing visible
  // to act on, and an invisible verb that silently stamps state is worse than
  // no verb at all.
  // ⌘⇧E rather than ⌘E: plain ⌘E toggles inline code in the Notebook editor,
  // and the shortcut editor's chord tests lean on ⌘E being free to record as an
  // exclusive leader.
  'session.settle': { key: 'e', meta: true, shift: true },
  // Open the snooze duration menu for the selected session. Like session.settle
  // it is bound only while the queue arrangement is on. ⌘⇧S carries no
  // Menu::default accelerator (the default menu claims no S at all) and, being a
  // Cmd chord, never reaches the PTY.
  'session.snooze': { key: 's', meta: true, shift: true },
  // Call off whatever countdown is on screen — the auto-settle about to close a
  // turn, an incoming ticket nudge, or both at once.
  //
  // ⌘. is the macOS cancel idiom, and it is the one key in this table the WebView
  // never sees: AppKit claims Command-period as `cancelOperation:` and consumes
  // it before any DOM keydown is dispatched. Verified against the packaged app —
  // an always-enabled action rebound to ⌘. does not fire, while the same action
  // on its default key does. So this entry is NOT what delivers the keystroke; a
  // native menu item carrying the ⌘. accelerator does (see `app_menu` in
  // src-tauri/src/lib.rs), and it re-dispatches this id into the page through the
  // same `attn:native-shortcut` path ⌘W uses.
  //
  // The entry stays here because it is what the cheatsheet, the shortcut editor,
  // and the countdown indicators render from — the key a user is told to press
  // must come from one table. It is marked `nativeDelivery` in metadata.ts, which
  // is what stops the editor from offering a rebind it could not honor.
  'session.cancelCountdown': { key: '.', meta: true },
  'session.toggleSidebar': { key: 'b', meta: true, shift: true },
  'session.refreshPRs': { key: 'r', meta: true },

  // Workspace switching
  'workspace.select1': { key: '1', code: 'Digit1', meta: true },
  'workspace.select2': { key: '2', code: 'Digit2', meta: true },
  'workspace.select3': { key: '3', code: 'Digit3', meta: true },
  'workspace.select4': { key: '4', code: 'Digit4', meta: true },
  'workspace.select5': { key: '5', code: 'Digit5', meta: true },
  'workspace.select6': { key: '6', code: 'Digit6', meta: true },
  'workspace.select7': { key: '7', code: 'Digit7', meta: true },
  'workspace.select8': { key: '8', code: 'Digit8', meta: true },
  'workspace.select9': { key: '9', code: 'Digit9', meta: true },

  // Dock panels
  'dock.attention': { key: 'p', meta: true, shift: true },

  // Action menu
  'ui.actionMenu': { key: 'k', meta: true },

  // Settings
  'ui.openSettings': { key: ',', meta: true },

  // Keyboard shortcuts cheatsheet
  'ui.showShortcuts': { key: '/', meta: true },

  // Font scaling
  'ui.increaseFontSize': { key: '=', meta: true },
  'ui.decreaseFontSize': { key: '-', meta: true },
  'ui.resetFontSize': { key: '0', meta: true },

  // Notebook (meta+alt+N is free — the only meta+alt combos are the pane-focus arrows)
  // Option-modified letters need a `code` fallback: with ⌥ held, macOS/WebKit
  // reports the dead-key character ('˜'/'Dead') as e.key, never 'n', so a
  // key-only match can never fire on a real (or CGEvent) keystroke — the key
  // falls through into the terminal PTY. matchesShortcut's e.code branch
  // matches the physical key regardless of that translation. Browser e2e
  // can't catch the broken state: Playwright synthesizes key:'n' directly.
  'notebook.openTile': { key: 'n', code: 'KeyN', meta: true, alt: true },
  'notebook.openFullscreen': { key: 'n', code: 'KeyN', meta: true, alt: true, shift: true },

  // Markdown file opener: a ⌘P-style palette listing recently opened markdown
  // files, then fuzzy-filtering the selected session's tree as you type. A
  // focused notebook tile keeps its own in-tile ⌘P (see paletteClaim.ts); this
  // is what ⌘P does everywhere else.
  'file.open': { key: 'p', meta: true },

  // Tickets board (fullscreen surface). meta+shift+T parallels meta+T = new workspace.
  'board.open': { key: 't', meta: true, shift: true },

  // Markdown annotations: send the current tile's draft to its bound session.
  // Plain ⌘Enter is otherwise unbound (terminal.toggleMaximize is ⌘⇧Enter) and
  // macOS's default menu carries no Enter accelerator. `editableTarget:
  // 'native'` keeps the capture-phase dispatcher out of inputs/textareas — the
  // annotation popover's own ⌘Enter (submit comment) must win there. The
  // handler is additionally registration-gated on tile focus-within so ⌘Enter
  // still reaches the PTY when a terminal pane is focused.
  'markdown.sendAnnotations': { key: 'Enter', meta: true, editableTarget: 'native' },

  // The same ⌘Enter for the annotations made on an agent's message in the
  // terminal. Registered only while the annotated pane is the focused leaf and
  // the panel is holding at least one mark, so ⌘Enter still reaches the PTY
  // everywhere else — including in a pane with nothing to send.
  'terminal.sendAnnotations': { key: 'Enter', meta: true, editableTarget: 'native' },
} as const;

export type ShortcutId = keyof typeof SHORTCUTS;

function modifiersEqual(a: Combo, b: Combo): boolean {
  return !!a.meta === !!b.meta
    && !!a.ctrl === !!b.ctrl
    && !!a.alt === !!b.alt
    && !!a.shift === !!b.shift;
}

/**
 * Whether two single combos could be triggered by the same keystroke — equal
 * modifiers AND an overlapping key OR code. This mirrors matchesShortcut's
 * key-OR-code equivalence so conflict detection matches dispatch semantics: a
 * localized digit capture (e.g. `key:'&'`, `code:'Digit1'`) collides with ⌘1
 * even though the printed key differs.
 */
export function combosConflict(a: Combo, b: Combo): boolean {
  if (!modifiersEqual(a, b)) return false;
  if (a.key.toLowerCase() === b.key.toLowerCase()) return true;
  return !!a.code && !!b.code && a.code === b.code;
}

/**
 * Whether two bindings collide. Shared by the load-time validator and the
 * runtime resolver so there is one definition of "conflict".
 *
 * - combo × combo: same keystroke (the original rule).
 * - chord × chord: same leader AND same follow key. Sharing only a leader is
 *   fine — that is how several chords hang off one leader (⌘K D, ⌘K G).
 * - chord × combo: collide iff the combo equals the chord's leader. Dispatch
 *   matches single combos first and fires them immediately, so a chord sharing
 *   that leader keystroke could never arm — they genuinely collide and the
 *   editor must force a reassign.
 */
export function bindingsConflict(a: Binding, b: Binding): boolean {
  const aChord = isChord(a);
  const bChord = isChord(b);
  if (aChord && bChord) {
    return combosConflict(a.leader, b.leader) && combosConflict(a.then, b.then);
  }
  if (aChord) return combosConflict(a.leader, b as Combo);
  if (bChord) return combosConflict(b.leader, a as Combo);
  return combosConflict(a as Combo, b as Combo);
}

/**
 * Two ids are an allowed conflict when they intentionally share a combo but are
 * context-gated at dispatch (e.g. session.close vs terminal.close on ⌘W).
 */
export function isAllowedConflict(idA: ShortcutId, idB: ShortcutId): boolean {
  const pair = [idA, idB].sort().join('|');
  return ALLOWED_CONFLICT_PAIRS.has(pair);
}

/**
 * Validate that no two shortcuts have the same key combination.
 * Throws an error at startup if conflicts are found.
 */
export function validateNoConflicts(): void {
  const entries = Object.entries(SHORTCUTS) as Array<[ShortcutId, ShortcutDef]>;
  for (let i = 0; i < entries.length; i++) {
    for (let j = i + 1; j < entries.length; j++) {
      const [idA, defA] = entries[i];
      const [idB, defB] = entries[j];
      if (!bindingsConflict(defA, defB)) continue;
      if (isAllowedConflict(idA, idB)) continue;
      throw new Error(`Shortcut conflict: "${idA}" and "${idB}" use the same combo`);
    }
  }
}

/**
 * Check if a keyboard event matches a shortcut definition
 */
export function matchesShortcut(e: KeyboardEvent, def: ShortcutDef): boolean {
  const keyMatches = e.key.toLowerCase() === def.key.toLowerCase()
    || (!!def.code && e.code === def.code);
  const wantsMeta = !!def.meta;
  const isMac = isMacLikePlatform();
  const accelPressed = isMac ? e.metaKey : (e.metaKey || e.ctrlKey);
  // When a shortcut does not want the accelerator, disallow both Cmd and Ctrl so
  // Ctrl-modified keys don't accidentally trigger non-meta shortcuts on macOS.
  const metaMatches = wantsMeta
    ? accelPressed
    : !(e.metaKey || e.ctrlKey);
  const shiftMatches = !!def.shift === e.shiftKey;
  const altMatches = !!def.alt === e.altKey;

  return keyMatches && metaMatches && shiftMatches && altMatches;
}

// Validate on module load
validateNoConflicts();
