// app/src/components/Keycap.tsx
// Keycap rendering shared by the shortcuts cheatsheet and the what's-new modal.

import './Keycap.css';

/**
 * A combo rendered as adjacent keycaps. The literal 'then' token (emitted by
 * shortcutTokens for a chord, e.g. ['⌘','K','then','D']) renders as a small
 * separator word rather than a keycap, so chords read "⌘K then D".
 */
export function KeyCombo({ tokens }: { tokens: string[] }) {
  return (
    <span className="key-combo">
      {tokens.map((token, i) => (
        token === 'then'
          ? <span className="key-combo-then" key={`then-${i}`}>then</span>
          : <kbd className="keycap" key={`${token}-${i}`}>{token}</kbd>
      ))}
    </span>
  );
}

/** Multiple combos rendered with a "/" separator (e.g. ⌘↑ / ⌘↓). */
export function KeyCombos({ combos }: { combos: string[][] }) {
  return (
    <span className="key-combos">
      {combos.map((combo, i) => (
        <span className="key-combos-item" key={i}>
          {i > 0 && <span className="key-combos-sep">/</span>}
          <KeyCombo tokens={combo} />
        </span>
      ))}
    </span>
  );
}
