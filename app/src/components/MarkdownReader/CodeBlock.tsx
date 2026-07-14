import { useEffect, useRef, useState } from 'react';
import type { HTMLAttributes } from 'react';

// Shiki is loaded lazily so the tile paints immediately with plain text and
// hydrates highlights async; the dynamic import also keeps shiki out of the
// main bundle. The `shiki` shorthand bundle manages its own highlighter
// singleton and loads languages/themes on demand.
let shikiModule: Promise<typeof import('shiki') | null> | null = null;
function loadShiki() {
  shikiModule ??= import('shiki').catch((error) => {
    console.warn('[MarkdownReader] Failed to load shiki:', error);
    return null;
  });
  return shikiModule;
}

const COPY_ICON_PATH =
  'M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z';
const CHECK_ICON_PATH = 'M5 13l4 4L19 7';

interface CodeBlockProps {
  code: string;
  language?: string;
  /** Anchoring data-* attributes stamped on the hast <pre>, forwarded to it. */
  preProps?: HTMLAttributes<HTMLPreElement>;
}

/**
 * Fenced code block: plannotator chrome (rounded pre, 13px mono, hover copy
 * button) + shiki dual-theme highlighting. Unknown languages fall back to
 * plain text with the same chrome.
 */
export function CodeBlock({ code, language, preProps }: CodeBlockProps) {
  const [highlighted, setHighlighted] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!language) {
      setHighlighted(null);
      return;
    }
    let cancelled = false;
    void loadShiki().then(async (shiki) => {
      if (!shiki || cancelled) {
        return;
      }
      try {
        const html = await shiki.codeToHtml(code, {
          lang: language,
          themes: { light: 'github-light-default', dark: 'github-dark-default' },
          defaultColor: false,
          structure: 'inline',
        });
        if (!cancelled) {
          setHighlighted(html);
        }
      } catch {
        // Unknown language: keep plain text, same chrome.
        if (!cancelled) {
          setHighlighted(null);
        }
      }
    });
    return () => {
      cancelled = true;
    };
  }, [code, language]);

  useEffect(() => () => {
    if (copyTimerRef.current !== null) {
      clearTimeout(copyTimerRef.current);
    }
  }, []);

  const handleCopy = () => {
    navigator.clipboard.writeText(code).then(() => {
      setCopied(true);
      if (copyTimerRef.current !== null) {
        clearTimeout(copyTimerRef.current);
      }
      copyTimerRef.current = setTimeout(() => setCopied(false), 2000);
    }).catch((error) => {
      console.error('Failed to copy:', error);
    });
  };

  return (
    <div className="md-codeblock">
      <button
        type="button"
        className={`md-copy-btn ${copied ? 'md-copy-btn--copied' : ''}`.trim()}
        title={copied ? 'Copied!' : 'Copy code'}
        aria-label={copied ? 'Copied!' : 'Copy code'}
        onClick={handleCopy}
      >
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d={copied ? CHECK_ICON_PATH : COPY_ICON_PATH} />
        </svg>
      </button>
      <pre {...preProps}>
        {highlighted !== null ? (
          <code
            className="md-shiki"
            // eslint-disable-next-line react/no-danger -- shiki output is
            // library-generated spans over the code text, not document HTML.
            dangerouslySetInnerHTML={{ __html: highlighted }}
          />
        ) : (
          <code>{code}</code>
        )}
      </pre>
    </div>
  );
}
