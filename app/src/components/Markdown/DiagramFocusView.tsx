import FocusTrap from 'focus-trap-react';
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
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../hooks/useEscapeStack';

const MIN_SCALE = 0.25;
const MAX_SCALE = 2;
const SCALE_STEP = 0.1;

type ScaleMode = 'actual' | 'fit' | 'manual';

interface DiagramFocusViewProps {
  svg: string;
  intrinsicWidth: number;
  intrinsicHeight: number;
  initialCenter: { x: number; y: number };
  onClose: () => void;
}

function clampScale(scale: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, scale));
}

/** Full-window, temporary reading surface for one already-rendered Mermaid SVG. */
export function DiagramFocusView({
  svg,
  intrinsicWidth,
  intrinsicHeight,
  initialCenter,
  onClose,
}: DiagramFocusViewProps) {
  const titleId = useId();
  const helpId = useId();
  const canvasRef = useRef<HTMLDivElement>(null);
  const pendingCenterRef = useRef<{ x: number; y: number } | null>(initialCenter);
  const [scale, setScale] = useState(1);
  const [scaleMode, setScaleMode] = useState<ScaleMode>('actual');

  useEscapeStack(onClose, true);

  useEffect(() => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = previousOverflow;
    };
  }, []);

  const currentCenter = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return { x: 0.5, y: 0.5 };
    return {
      x: canvas.scrollWidth > 0
        ? (canvas.scrollLeft + canvas.clientWidth / 2) / canvas.scrollWidth
        : 0.5,
      y: canvas.scrollHeight > 0
        ? (canvas.scrollTop + canvas.clientHeight / 2) / canvas.scrollHeight
        : 0.5,
    };
  }, []);

  const updateScale = useCallback((nextScale: number, mode: ScaleMode) => {
    pendingCenterRef.current = currentCenter();
    setScale(clampScale(nextScale));
    setScaleMode(mode);
  }, [currentCenter]);

  const fit = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const availableWidth = Math.max(1, canvas.clientWidth - 48);
    const availableHeight = Math.max(1, canvas.clientHeight - 48);
    updateScale(Math.min(1, availableWidth / intrinsicWidth, availableHeight / intrinsicHeight), 'fit');
  }, [intrinsicHeight, intrinsicWidth, updateScale]);

  useLayoutEffect(() => {
    const center = pendingCenterRef.current;
    const canvas = canvasRef.current;
    if (!center || !canvas) return;
    pendingCenterRef.current = null;
    canvas.scrollLeft = Math.max(0, center.x * canvas.scrollWidth - canvas.clientWidth / 2);
    canvas.scrollTop = Math.max(0, center.y * canvas.scrollHeight - canvas.clientHeight / 2);
  }, [scale]);

  useEffect(() => {
    if (scaleMode !== 'fit' || typeof ResizeObserver === 'undefined') return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    const observer = new ResizeObserver(() => fit());
    observer.observe(canvas);
    return () => observer.disconnect();
  }, [fit, scaleMode]);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return;

    if (event.key === '+' || event.key === '=') {
      event.preventDefault();
      updateScale(scale + SCALE_STEP, 'manual');
      return;
    }
    if (event.key === '-' || event.key === '_') {
      event.preventDefault();
      updateScale(scale - SCALE_STEP, 'manual');
      return;
    }
    if (event.key === '0') {
      event.preventDefault();
      fit();
      return;
    }
    if (event.key === '1') {
      event.preventDefault();
      updateScale(1, 'actual');
      return;
    }

    const canvas = canvasRef.current;
    if (document.activeElement !== canvas || !canvas) return;
    const distance = event.shiftKey ? 288 : 96;
    const delta = {
      ArrowLeft: [-distance, 0],
      ArrowRight: [distance, 0],
      ArrowUp: [0, -distance],
      ArrowDown: [0, distance],
    }[event.key];
    if (!delta) return;
    event.preventDefault();
    canvas.scrollBy({ left: delta[0], top: delta[1], behavior: 'auto' });
  };

  const stageStyle = {
    '--md-diagram-focus-width': `${intrinsicWidth * scale}px`,
    '--md-diagram-focus-height': `${intrinsicHeight * scale}px`,
  } as CSSProperties;

  return createPortal(
    <FocusTrap
      focusTrapOptions={{
        escapeDeactivates: false,
        returnFocusOnDeactivate: false,
        initialFocus: () => canvasRef.current ?? undefined,
        fallbackFocus: () => canvasRef.current ?? document.body,
      }}
    >
      <div
        className="md-diagram-focus"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={helpId}
        onKeyDown={handleKeyDown}
        data-md-chrome="1"
      >
        <header className="md-diagram-focus-header">
          <div className="md-diagram-focus-heading">
            <span className="md-diagram-focus-kicker">Diagram focus</span>
            <h2 id={titleId}>Mermaid diagram</h2>
          </div>
          <span className="md-diagram-focus-readout" aria-live="polite">
            {Math.round(scale * 100)}%
          </span>
          <button
            type="button"
            className="md-diagram-focus-control"
            onClick={() => updateScale(scale - SCALE_STEP, 'manual')}
            aria-label="Zoom out"
          >
            −
          </button>
          <button
            type="button"
            className="md-diagram-focus-control"
            onClick={() => updateScale(scale + SCALE_STEP, 'manual')}
            aria-label="Zoom in"
          >
            +
          </button>
          <button type="button" className="md-diagram-focus-control" onClick={fit}>
            Fit
          </button>
          <button
            type="button"
            className="md-diagram-focus-control"
            onClick={() => updateScale(1, 'actual')}
          >
            100%
          </button>
          <button type="button" className="md-diagram-focus-return" onClick={onClose}>
            Return to document
          </button>
        </header>
        <p className="md-diagram-focus-help" id={helpId}>
          Use arrow keys to pan, plus or minus to zoom, 0 to fit, 1 for 100%, and Escape to return.
        </p>
        <div
          ref={canvasRef}
          className="md-diagram-focus-canvas"
          tabIndex={0}
          aria-label="Focused diagram canvas"
        >
          <div
            className="md-diagram-focus-stage"
            style={stageStyle}
            // Mermaid rendered this with securityLevel: strict before focus opened.
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        </div>
      </div>
    </FocusTrap>,
    document.body,
  );
}
