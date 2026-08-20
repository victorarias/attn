// The garden is one object read at two sizes: docked beside your work, or
// holding the window. This is the box it lives in, and the travel between them.
//
// GardenPanel is rendered exactly once, inside a fixed-position element whose
// rectangle is either the dock slot's or the window's. Nothing unmounts, so the
// trail, the open seed, the scroll offset and focus survive the promotion by
// construction rather than by being copied between two instances.
//
// The panel is never told which frame it is in — only how wide it is. It draws
// the walk as a stack or as Miller columns depending on the room it has, so the
// columns arrive during the flight, when the box gets wide enough to hold them,
// rather than being switched on at the far end. That is what makes the
// promotion read as one object growing instead of a second surface opening.
import FocusTrap from 'focus-trap-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { GardenBoard, type Verb } from './GardenBoard';
import { GardenPanel } from './GardenPanel';
import type { SeedDocument } from './SeedDocumentView';
import './GardenFrame.css';

export type GardenMode = 'closed' | 'dock' | 'full';

export interface FrameRect {
  top: number;
  left: number;
  width: number;
  height: number;
}

// The window frame's gutter, matching the notebook's fullscreen surface.
const FULL_INSET = 12;
// Expand is the reveal and gets the longer beat; collapse is a dismissal and
// wants to be out of the way. Both on the dock's own easing, so the promotion
// sounds like the dock it came from.
const EXPAND_MS = 180;
const COLLAPSE_MS = 150;

function reduceMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/**
 * The rectangle the dock reserved for the garden, in viewport coordinates.
 *
 * Attach the ref to a detached dock panel's slot (see RightDock): the dock
 * lays the slot out exactly where the panel would have gone — stack offset and
 * `clamp()`'d width included — and paints nothing in it.
 */
export function useDockSlotRect() {
  const ref = useRef<HTMLDivElement | null>(null);
  const [rect, setRect] = useState<FrameRect | null>(null);

  useLayoutEffect(() => {
    const measure = () => {
      const slot = ref.current;
      if (!slot) return;
      const box = slot.getBoundingClientRect();
      const next: FrameRect = { top: box.top, left: box.left, width: box.width, height: box.height };
      setRect((prev) => (
        prev && prev.top === next.top && prev.left === next.left
          && prev.width === next.width && prev.height === next.height
          ? prev
          : next
      ));
    };
    measure();
    const observer = new ResizeObserver(measure);
    if (ref.current) observer.observe(ref.current);
    window.addEventListener('resize', measure);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, []);

  return [ref, rect] as const;
}

export interface GardenFrameProps {
  mode: GardenMode;
  /** Where the dock reserved room for it. Null until the dock has been laid out. */
  dockRect: FrameRect | null;
  /** Promote to the window, or hand it back to the dock. */
  onToggleFrame: () => void;
  /** The bottom of the Escape ladder, below climbing: window → dock → gone. */
  onEscapeFloor: () => void;
  /** Leave the garden entirely. */
  onClose: () => void;
  seeds: Seed[];
  seedsTotal: number;
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  onOpenAsTile?: (seedId: string) => void;
  onOpenMarkdownArtifact?: (path: string) => void;
  checkArtifactPath?: (path: string) => Promise<boolean>;
  onResumeSeed?: (seedId: string) => void;
  /** Sessions the daemon still knows — how a board card says its tender left. */
  liveSessions?: Set<string>;
  /** False until the first push lands; an empty garden and an unread one differ. */
  loaded?: boolean;
  /** One real lifecycle move, for the board's drop zones and verb menu. */
  moveSeed?: (seedId: string, verb: Verb, reason?: string) => Promise<unknown>;
  /** Write on a seed's log. */
  noteSeed?: (seedId: string, body: string) => Promise<unknown>;
}

export function GardenFrame({
  mode,
  dockRect,
  onToggleFrame,
  onEscapeFloor,
  onClose,
  seeds,
  seedsTotal,
  fetchSeedDocument,
  onOpenAsTile,
  onOpenMarkdownArtifact,
  checkArtifactPath,
  onResumeSeed,
  liveSessions,
  loaded = true,
  moveSeed,
  noteSeed,
}: GardenFrameProps) {
  // Two views over one garden: the list answers what is here, the board answers
  // how it is moving. The switch lives here rather than in either view, so
  // neither owns the other — and only in the window, because four columns of
  // cards need the room. Promotion lands on the list whatever the reader last
  // chose: the point of the gesture is that the thing you were reading got
  // bigger, and arriving somewhere else would say the opposite.
  const [view, setView] = useState<'list' | 'board'>('list');
  const frameRef = useRef<HTMLDivElement | null>(null);
  const [viewport, setViewport] = useState(() => ({ w: window.innerWidth, h: window.innerHeight }));
  const [flying, setFlying] = useState(false);
  const previousMode = useRef<GardenMode>(mode);

  useEffect(() => {
    if (mode !== 'full') return;
    const onResize = () => setViewport({ w: window.innerWidth, h: window.innerHeight });
    onResize();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [mode]);

  useEffect(() => {
    if (mode !== 'full') setView('list');
  }, [mode]);

  // In list view the bottom of the Escape ladder is registered by GardenPanel,
  // under its own climb. The board is a different view with its own trail, and
  // GardenPanel is not mounted behind it, so the frame carries the floor while
  // the board is up. It arms when the board appears and the board's climb arms
  // when the reader walks into a plot — later, so the climb lands above it.
  const showingBoard = mode === 'full' && view === 'board' && Boolean(moveSeed && noteSeed);
  useEscapeStack(onEscapeFloor, showingBoard);

  useLayoutEffect(() => {
    const was = previousMode.current;
    previousMode.current = mode;
    const promotion = (was === 'dock' && mode === 'full') || (was === 'full' && mode === 'dock');
    if (!promotion) return;
    const root = frameRef.current;
    if (!root) return;
    // Coming back from the window, the trap releases focus to wherever it sat
    // before the promotion — which may be nowhere near the garden. The reader is
    // still reading the garden, so keep focus inside it.
    if (mode === 'dock' && !root.contains(document.activeElement)) {
      (root.querySelector('.garden-frame__body') as HTMLElement | null)?.focus();
    }
    if (reduceMotion()) return;
    setFlying(true);
    const duration = mode === 'full' ? EXPAND_MS : COLLAPSE_MS;
    const done = window.setTimeout(() => setFlying(false), duration + 40);
    return () => window.clearTimeout(done);
  }, [mode]);

  const full: FrameRect = {
    top: FULL_INSET,
    left: FULL_INSET,
    width: Math.max(0, viewport.w - FULL_INSET * 2),
    height: Math.max(0, viewport.h - FULL_INSET * 2),
  };
  const rect = mode === 'full' ? full : dockRect;
  if (!rect) return null;

  const open = mode !== 'closed';
  const boardable = mode === 'full' && Boolean(moveSeed && noteSeed);
  const viewToggle = boardable ? (
    <div className="garden-view-switch" role="group" aria-label="Garden view">
      <button type="button" aria-pressed={view === 'list'} onClick={() => setView('list')}>
        list
      </button>
      <button type="button" aria-pressed={view === 'board'} onClick={() => setView('board')}>
        board
      </button>
    </div>
  ) : undefined;
  return (
    <>
      <div className={`garden-frame-backdrop${mode === 'full' ? ' is-visible' : ''}`} aria-hidden="true" />
      <div
        ref={frameRef}
        className={`garden-frame is-${mode}${flying ? ' is-flying' : ''}`}
        style={{ top: rect.top, left: rect.left, width: rect.width, height: rect.height }}
        role={mode === 'full' ? 'dialog' : undefined}
        aria-modal={mode === 'full' ? true : undefined}
        aria-label="The garden"
        aria-hidden={!open}
        inert={!open || undefined}
        data-testid="garden-frame"
      >
        <FocusTrap
          active={mode === 'full'}
          focusTrapOptions={{
            escapeDeactivates: false,
            clickOutsideDeactivates: false,
            initialFocus: false,
            fallbackFocus: '.garden-frame__body',
            // The frame is not an overlay that opens over your work and hands
            // focus back when it goes — it shrinks. Returning focus to whatever
            // held it before the promotion would take it out of the garden the
            // reader is still reading.
            returnFocusOnDeactivate: false,
          }}
        >
          <div className="garden-frame__body" tabIndex={-1}>
            {showingBoard ? (
              <GardenBoard
                seeds={seeds}
                seedsTotal={seedsTotal}
                liveSessions={liveSessions ?? new Set()}
                loaded={loaded}
                onTransition={moveSeed!}
                onNote={noteSeed!}
                viewToggle={viewToggle}
                onClose={onClose}
              />
            ) : (
              <GardenPanel
                isOpen={open}
                seeds={seeds}
                seedsTotal={seedsTotal}
                fetchSeedDocument={fetchSeedDocument}
                onOpenAsTile={onOpenAsTile}
                onOpenMarkdownArtifact={onOpenMarkdownArtifact}
                checkArtifactPath={checkArtifactPath}
                onResumeSeed={onResumeSeed}
                onClose={onClose}
                viewToggle={viewToggle}
                frame={mode === 'full' ? 'full' : 'dock'}
                onToggleFrame={onToggleFrame}
                onEscapeFloor={onEscapeFloor}
              />
            )}
          </div>
        </FocusTrap>
      </div>
    </>
  );
}
