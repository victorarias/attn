import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
} from 'react';
import { DiagramFocusView } from './DiagramFocusView';
import './Markdown.css';

const OVERSIZED_FIT_RATIO = 0.8;

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
  presentation?: 'static' | 'reader';
}

interface RenderedMermaidDiagramProps extends MermaidDiagramProps {
  theme: MermaidTheme;
}

interface DiagramSize {
  width: number;
  height: number;
  availableWidth: number;
}

interface FocusState {
  origin: HTMLElement | null;
  initialCenter: { x: number; y: number };
}

function parseViewBox(svg: SVGSVGElement): { width: number; height: number } | null {
  const parts = svg.getAttribute('viewBox')?.trim().split(/[ ,]+/).map(Number);
  if (!parts || parts.length !== 4 || !parts.every(Number.isFinite)) return null;
  const width = parts[2];
  const height = parts[3];
  if (width <= 0 || height <= 0) return null;
  return { width, height };
}

function scrollDiagram(viewport: HTMLDivElement, event: KeyboardEvent<HTMLDivElement>): boolean {
  const distance = event.shiftKey ? 216 : 72;
  const delta = {
    ArrowLeft: [-distance, 0],
    ArrowRight: [distance, 0],
    ArrowUp: [0, -distance],
    ArrowDown: [0, distance],
  }[event.key];
  if (delta) {
    viewport.scrollBy({ left: delta[0], top: delta[1], behavior: 'auto' });
    return true;
  }
  if (event.key === 'PageUp' || event.key === 'PageDown') {
    viewport.scrollBy({
      top: viewport.clientHeight * (event.key === 'PageUp' ? -0.8 : 0.8),
      behavior: 'auto',
    });
    return true;
  }
  if (event.key === 'Home') {
    viewport.scrollTo({ left: 0, top: 0, behavior: 'auto' });
    return true;
  }
  if (event.key === 'End') {
    viewport.scrollTo({ left: viewport.scrollWidth, top: viewport.scrollHeight, behavior: 'auto' });
    return true;
  }
  return false;
}

export function MermaidDiagram(props: MermaidDiagramProps) {
  const theme = useMermaidTheme();
  return <RenderedMermaidDiagram key={`${theme}:${props.code}`} {...props} theme={theme} />;
}

function RenderedMermaidDiagram({
  code,
  onLayoutChange,
  presentation = 'static',
  theme,
}: RenderedMermaidDiagramProps) {
  const rawId = useId();
  const [svg, setSvg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [size, setSize] = useState<DiagramSize | null>(null);
  const [focusState, setFocusState] = useState<FocusState | null>(null);
  const idRef = useRef(`mermaid-${rawId.replace(/:/g, '')}-${renderCounter++}`);
  const viewportRef = useRef<HTMLDivElement>(null);
  const pendingFocusReturnRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    let cancelled = false;

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

  const measure = useCallback(() => {
    const viewport = viewportRef.current;
    const svgElement = viewport?.querySelector<SVGSVGElement>('svg');
    if (!viewport || !svgElement) return;
    const viewBox = parseViewBox(svgElement);
    if (!viewBox) return;
    const next = {
      ...viewBox,
      availableWidth: viewport.clientWidth || viewport.getBoundingClientRect().width,
    };
    setSize((current) => (
      current
      && current.width === next.width
      && current.height === next.height
      && current.availableWidth === next.availableWidth
        ? current
        : next
    ));
  }, []);

  useLayoutEffect(() => {
    if (presentation !== 'reader' || !svg) return;
    measure();
    const viewport = viewportRef.current;
    if (!viewport || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [focusState, measure, presentation, svg]);

  useLayoutEffect(() => {
    if (focusState || !pendingFocusReturnRef.current) return;
    const origin = pendingFocusReturnRef.current;
    pendingFocusReturnRef.current = null;
    const frame = requestAnimationFrame(() => {
      if (origin.isConnected) {
        origin.focus({ preventScroll: true });
      } else {
        viewportRef.current?.focus({ preventScroll: true });
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [focusState]);

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
  if (presentation === 'static') {
    return <div className="markdown-mermaid" dangerouslySetInnerHTML={{ __html: svg }} />;
  }

  const isOversized = Boolean(size && size.availableWidth / size.width < OVERSIZED_FIT_RATIO);
  const intrinsicStyle = size ? {
    '--md-diagram-intrinsic-width': `${size.width}px`,
    '--md-diagram-intrinsic-height': `${size.height}px`,
  } as CSSProperties : undefined;

  const openFocus = (origin: HTMLElement | null) => {
    const viewport = viewportRef.current;
    if (!viewport || !size) return;
    setFocusState({
      origin,
      initialCenter: {
        x: viewport.scrollWidth > 0
          ? (viewport.scrollLeft + viewport.clientWidth / 2) / viewport.scrollWidth
          : 0.5,
        y: viewport.scrollHeight > 0
          ? (viewport.scrollTop + viewport.clientHeight / 2) / viewport.scrollHeight
          : 0.5,
      },
    });
  };

  const closeFocus = () => {
    pendingFocusReturnRef.current = focusState?.origin ?? viewportRef.current;
    setFocusState(null);
  };

  const handleViewportKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    if (event.key === 'Enter') {
      event.preventDefault();
      openFocus(event.currentTarget);
      return;
    }
    if (scrollDiagram(event.currentTarget, event)) {
      event.preventDefault();
    }
  };

  const viewportClass = `markdown-mermaid ${isOversized ? 'markdown-mermaid--oversized' : ''}`.trim();
  const viewport = focusState ? (
    <div
      ref={viewportRef}
      className={viewportClass}
      style={intrinsicStyle}
      tabIndex={isOversized ? 0 : undefined}
      role={isOversized ? 'region' : undefined}
      aria-label={isOversized ? 'Large Mermaid diagram. Press Enter for diagram focus.' : undefined}
      onKeyDown={isOversized ? handleViewportKeyDown : undefined}
      onDoubleClick={isOversized ? (event) => openFocus(event.currentTarget) : undefined}
    >
      <div
        className="markdown-mermaid-placeholder"
        style={{ width: size?.width, height: size?.height }}
        aria-hidden="true"
      />
    </div>
  ) : (
    <div
      ref={viewportRef}
      className={viewportClass}
      style={intrinsicStyle}
      tabIndex={isOversized ? 0 : undefined}
      role={isOversized ? 'region' : undefined}
      aria-label={isOversized ? 'Large Mermaid diagram. Press Enter for diagram focus.' : undefined}
      onKeyDown={isOversized ? handleViewportKeyDown : undefined}
      onDoubleClick={isOversized ? (event) => openFocus(event.currentTarget) : undefined}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );

  return (
    <div className={`markdown-mermaid-frame ${isOversized ? 'markdown-mermaid-frame--oversized' : ''}`.trim()}>
      {isOversized && (
        <div className="markdown-mermaid-toolbar" data-md-chrome="1">
          <span className="markdown-mermaid-status">Large diagram · 100%</span>
          <button
            type="button"
            className="markdown-mermaid-focus-button"
            onClick={(event) => openFocus(event.currentTarget)}
          >
            Focus diagram
          </button>
        </div>
      )}
      {viewport}
      {focusState && size && (
        <DiagramFocusView
          svg={svg}
          intrinsicWidth={size.width}
          intrinsicHeight={size.height}
          initialCenter={focusState.initialCenter}
          onClose={closeFocus}
        />
      )}
    </div>
  );
}
