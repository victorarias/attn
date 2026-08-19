import { useEffect, useState } from 'react';

// Shiki is loaded lazily so a code block paints immediately as plain text and
// hydrates highlights async; the dynamic import also keeps shiki out of the
// main bundle. The `shiki` shorthand bundle manages its own highlighter
// singleton and loads languages/themes on demand.
let shikiModule: Promise<typeof import('shiki') | null> | null = null;
function loadShiki() {
  shikiModule ??= import('shiki').catch((error) => {
    console.warn('[Markdown] Failed to load shiki:', error);
    return null;
  });
  return shikiModule;
}

export interface Highlighted {
  /** The inputs the html was generated from — stale results never render. */
  code: string;
  language: string;
  html: string;
}

/**
 * Shiki markup for one code block, or null while there is none to show.
 *
 * `enabled` is how a streaming surface opts out. Highlighting text that is
 * still arriving runs shiki once per delta and throws every result but the
 * last away, so the caller passes false until the text can no longer change.
 */
export function useShikiHighlight(
  code: string,
  language: string | undefined,
  enabled = true,
): Highlighted | null {
  const [highlighted, setHighlighted] = useState<Highlighted | null>(null);

  useEffect(() => {
    if (!language || !enabled) {
      setHighlighted(null);
      return;
    }
    let cancelled = false;
    void loadShiki().then(async (shiki) => {
      if (!shiki || cancelled) return;
      try {
        const raw = await shiki.codeToHtml(code, {
          lang: language,
          themes: { light: 'github-light-default', dark: 'github-dark-default' },
          defaultColor: false,
          structure: 'inline',
        });
        // Shiki's inline structure renders line breaks as <br> ELEMENTS, so the
        // hydrated DOM contains no '\n' text. <pre> renders a newline text node
        // identically, and MarkdownReader's anchoring requires DOM text-node
        // parity with extractBlockTexts (see anchoring/domRange.ts), so restore
        // them. Code content is HTML-escaped by shiki, so a literal `<br` in
        // code can't match.
        const html = raw.replace(/<br\s*\/?>/g, '\n');
        if (!cancelled) setHighlighted({ code, language, html });
      } catch {
        // Unknown language: keep plain text, same chrome.
        if (!cancelled) setHighlighted(null);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [code, language, enabled]);

  // Only markup generated from the CURRENT inputs: a block re-rendered in place
  // with new code must not keep the previous version's highlight on screen
  // while the re-highlight is in flight.
  return highlighted && highlighted.code === code && highlighted.language === language
    ? highlighted
    : null;
}
