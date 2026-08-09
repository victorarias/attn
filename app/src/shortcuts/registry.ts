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

/** A leader-key chord: press `leader`, then `then` within the timeout. Depth is fixed at two. */
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
  // One verb on one key from two surfaces; a focused markdown tile and a
  // focused terminal pane are mutually exclusive, so only one handler registers.
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

  // In editable targets ⌘F belongs to the focused editor (CodeMirror search).
  'terminal.find': { key: 'f', meta: true, editableTarget: 'native' },

  // Session management
  'session.new': { key: 'n', meta: true },
  'session.newHorizontal': { key: 'n', meta: true, shift: true },
  'session.newWorkspace': { key: 't', meta: true },
  'session.close': { key: 'w', meta: true },
  'session.prev': { key: 'ArrowUp', meta: true, editableTarget: 'native' },
  'session.next': { key: 'ArrowDown', meta: true, editableTarget: 'native' },
  'session.goToDashboard': { key: 'h', meta: true, shift: true },
  'view.toggleGrid': { key: 'g', meta: true },
  'session.jumpToWaiting': { key: 'j', meta: true },
  // Bound only while the queue arrangement is on — an invisible verb that
  // stamps state is worse than none. ⌘E stays free: Notebook inline code uses
  // it, and the shortcut editor's chord tests record it as an exclusive leader.
  'session.settle': { key: 'e', meta: true, shift: true },
  // Bound only while the queue arrangement is on. ⌘⇧S carries no Menu::default
  // accelerator and, being a Cmd chord, never reaches the PTY.
  'session.snooze': { key: 's', meta: true, shift: true },
  // The one key here the WebView never sees: AppKit consumes ⌘. as
  // `cancelOperation:` before any DOM keydown (verified in the packaged app), so
  // a native menu item in `app_menu` (src-tauri/src/lib.rs) delivers it via
  // `attn:native-shortcut`. The entry stays because the cheatsheet, editor, and
  // indicators render from this table; `nativeDelivery` in metadata.ts blocks a
  // rebind that could not be honored.
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

  // Option-modified letters need the `code` fallback: with ⌥ held macOS/WebKit
  // reports a dead-key character as e.key, never 'n', so a key-only match never
  // fires on a real keystroke. Playwright synthesizes key:'n', so e2e can't see it.
  'notebook.openTile': { key: 'n', code: 'KeyN', meta: true, alt: true },
  'notebook.openFullscreen': { key: 'n', code: 'KeyN', meta: true, alt: true, shift: true },

  // A focused notebook tile keeps its own in-tile ⌘P (see paletteClaim.ts);
  // this is what ⌘P does everywhere else.
  'file.open': { key: 'p', meta: true },

  // Tickets board; meta+shift+T parallels meta+T = new workspace.
  'board.open': { key: 't', meta: true, shift: true },

  // `editableTarget: 'native'` keeps the capture-phase dispatcher out of
  // inputs — the annotation popover's own ⌘Enter must win there — and the
  // handler is registration-gated on tile focus-within so ⌘Enter still reaches
  // the PTY from a terminal pane.
  'markdown.sendAnnotations': { key: 'Enter', meta: true, editableTarget: 'native' },

  // Registered only while the annotated pane is the focused leaf and holds at
  // least one mark, so ⌘Enter still reaches the PTY everywhere else.
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
 * Whether two combos share a keystroke: equal modifiers AND an overlapping key
 * OR code, mirroring matchesShortcut so conflicts match dispatch semantics.
 */
export function combosConflict(a: Combo, b: Combo): boolean {
  if (!modifiersEqual(a, b)) return false;
  if (a.key.toLowerCase() === b.key.toLowerCase()) return true;
  return !!a.code && !!b.code && a.code === b.code;
}

/**
 * Whether two bindings collide; the single definition shared by the load-time
 * validator and the runtime resolver. Two chords need both leader and follow to
 * match (several chords may share a leader), while a combo equal to a chord's
 * leader collides: dispatch fires the combo first, so the chord could never arm.
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

/** Ids that intentionally share a combo but are context-gated at dispatch. */
export function isAllowedConflict(idA: ShortcutId, idB: ShortcutId): boolean {
  const pair = [idA, idB].sort().join('|');
  return ALLOWED_CONFLICT_PAIRS.has(pair);
}

/** Throws at module load when two shortcuts share a key combination. */
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

export function matchesShortcut(e: KeyboardEvent, def: ShortcutDef): boolean {
  const keyMatches = e.key.toLowerCase() === def.key.toLowerCase()
    || (!!def.code && e.code === def.code);
  const wantsMeta = !!def.meta;
  const isMac = isMacLikePlatform();
  const accelPressed = isMac ? e.metaKey : (e.metaKey || e.ctrlKey);
  // Without the accelerator, disallow both Cmd and Ctrl so Ctrl-modified keys
  // don't trigger non-meta shortcuts on macOS.
  const metaMatches = wantsMeta
    ? accelPressed
    : !(e.metaKey || e.ctrlKey);
  const shiftMatches = !!def.shift === e.shiftKey;
  const altMatches = !!def.alt === e.altKey;

  return keyMatches && metaMatches && shiftMatches && altMatches;
}

validateNoConflicts();
