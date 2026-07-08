import { useEffect, useId, useRef, useState } from 'react';
import './Markdown.css';

// Loaded lazily so mermaid (large) is code-split out of the main bundle and
// only fetched when a document actually contains a mermaid fence.
let mermaidModulePromise: Promise<typeof import('mermaid')> | null = null;
function loadMermaid() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid');
  }
  return mermaidModulePromise;
}

type MermaidTheme = 'dark' | 'neutral';

function resolveTheme(): MermaidTheme {
  const attr = document.documentElement.getAttribute('data-theme');
  if (attr === 'dark') return 'dark';
  if (attr === 'light') return 'neutral';
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'neutral' : 'dark';
}

/** Track the resolved theme, re-evaluating on data-theme changes or (when unset) OS scheme changes. */
function useMermaidTheme(): MermaidTheme {
  const [theme, setTheme] = useState<MermaidTheme>(resolveTheme);

  useEffect(() => {
    const recompute = () => setTheme(resolveTheme());

    const observer = new MutationObserver(recompute);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });

    const media = window.matchMedia('(prefers-color-scheme: light)');
    media.addEventListener('change', recompute);

    return () => {
      observer.disconnect();
      media.removeEventListener('change', recompute);
    };
  }, []);

  return theme;
}

let renderCounter = 0;

interface MermaidDiagramProps {
  code: string;
  // Called after the diagram's rendered height may have changed — i.e. after
  // the loading placeholder is replaced by the real SVG or an error fallback.
  // A CodeView host uses this to know when its cached item layout is stale.
  onLayoutChange?: () => void;
}

export function MermaidDiagram({ code, onLayoutChange }: MermaidDiagramProps) {
  const theme = useMermaidTheme();
  const rawId = useId();
  const [svg, setSvg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const idRef = useRef(`mermaid-${rawId.replace(/:/g, '')}-${renderCounter++}`);

  useEffect(() => {
    let cancelled = false;
    setSvg(null);
    setError(null);

    loadMermaid()
      .then(async ({ default: mermaid }) => {
        mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme });
        return mermaid.render(idRef.current, code);
      })
      .then(({ svg: rendered }) => {
        if (cancelled) return;
        setSvg(rendered);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        // mermaid can leave an orphan error node in the document on a failed render.
        document.getElementById(`d${idRef.current}`)?.remove();
        setError(err instanceof Error ? err.message : String(err));
      });

    return () => {
      cancelled = true;
    };
  }, [code, theme]);

  // Kept in a ref (not an effect dep) so a caller passing a fresh callback
  // identity on every render — e.g. a parent re-rendered by an unrelated
  // items-version bump — never re-fires this; only an actual svg/error
  // transition (a genuine layout change) does.
  const onLayoutChangeRef = useRef(onLayoutChange);
  onLayoutChangeRef.current = onLayoutChange;

  useEffect(() => {
    if (svg || error) {
      onLayoutChangeRef.current?.();
    }
  }, [svg, error]);

  if (error) {
    return (
      <div className="markdown-mermaid-error-wrap">
        <p className="markdown-mermaid-error-note">Diagram failed to render: {error}</p>
        <pre className="markdown-mermaid-error">{code}</pre>
      </div>
    );
  }

  if (!svg) {
    return <pre className="markdown-mermaid-loading">{code}</pre>;
  }

  // mermaid.render returns sanitized SVG markup (securityLevel: 'strict').
  return <div className="markdown-mermaid" dangerouslySetInnerHTML={{ __html: svg }} />;
}
