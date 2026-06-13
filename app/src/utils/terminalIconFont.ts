// Activates the bundled "Symbols Nerd Font Mono" face (see index.html
// @font-face) used as the terminal's icon fallback for Private Use Area glyphs
// (eza --icons, powerline, devicons).
//
// Two WebKit/WKWebView realities make this non-trivial:
//   1. A 2D canvas does not use a font that was only loaded via the CSS Font
//      Loading API — it must also be USED in DOM layout, or fillText() silently
//      substitutes a system face and PUA glyphs rasterize blank. So we mount a
//      hidden element that actually renders the face, forcing WebKit to load and
//      activate it for the document (and thus for canvas).
//   2. Even once mounted, the face loads asynchronously, so glyphs rasterized
//      before it is ready cache blank. ensureTerminalIconFont resolves when the
//      face is ready, letting callers drop those stale glyphs and repaint.

export const TERMINAL_ICON_FONT_FAMILY = 'Symbols Nerd Font Mono';

// Sample PUA glyphs the bundled font provides (folder, powerline separator,
// file) — enough to make WebKit lay the face out and activate it.
const PROBE_GLYPHS = '\uF07B\uE0B0\uF15B';

let probeMounted = false;
let readyPromise: Promise<void> | null = null;

function mountProbe(): void {
  if (probeMounted || typeof document === 'undefined' || !document.body) return;
  probeMounted = true;
  const probe = document.createElement('span');
  probe.setAttribute('aria-hidden', 'true');
  // Off-screen but still laid out (not display:none / visibility:hidden, which
  // can skip font loading), so WebKit actually loads + activates the face.
  probe.style.cssText =
    'position:absolute;left:-9999px;top:-9999px;width:0;height:0;overflow:hidden;' +
    `font-family:"${TERMINAL_ICON_FONT_FAMILY}";font-size:32px;`;
  probe.textContent = PROBE_GLYPHS;
  document.body.appendChild(probe);
}

// Resolves once the icon font is loaded (or immediately if the Font Loading API
// is unavailable). Safe to call repeatedly: the probe is mounted once and the
// load is shared across callers.
export function ensureTerminalIconFont(fontSize: number): Promise<void> {
  mountProbe();
  if (readyPromise) return readyPromise;
  const fonts = typeof document !== 'undefined' ? document.fonts : null;
  if (!fonts || typeof fonts.load !== 'function') {
    readyPromise = Promise.resolve();
    return readyPromise;
  }
  const spec = `${fontSize}px "${TERMINAL_ICON_FONT_FAMILY}"`;
  readyPromise = fonts
    .load(spec)
    .then(() => undefined)
    .catch(() => undefined);
  return readyPromise;
}
