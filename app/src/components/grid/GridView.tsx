// Grid mode host: a global "mission control" that renders every live session as
// a live terminal tile inside ONE WebGL context (UnifiedGridRenderer). It is a
// read-only OBSERVER — it taps the global PTY event firehose for live bytes and
// never attaches or resizes, so it can never claim PTY geometry (AGENTS.md #7).
//
// Feed: every session's pane is already attached by the workspace layer, so its
// output already flows through listenPtyEvents(); we add one more listener and
// route bytes by runtimeId into the matching tile model.
//
// Seeding: a freshly tiled session would otherwise stay blank until it next
// emits. So when a tile appears we fetch its current screen from the daemon
// (getScreenSnapshot) and paint it, then dedup the live firehose against the
// snapshot's sequence watermark so nothing double-paints or is lost.
//
// Input: read-only is for the overview. Once a tile is ZOOMED it becomes the
// keyboard-input target — a Ghostty InputHandler bound to the stage encodes
// keystrokes for that session's model and we forward them with ptyWrite (which
// claims no geometry, so AGENTS.md #7 still holds: we never attach or resize).
// We grab stage focus on open/zoom so input follows the zoom instead of leaking
// to whichever pane held focus when the grid opened.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Ghostty, InputHandler } from 'ghostty-web';
import ghosttyWasmUrl from 'ghostty-web/ghostty-vt.wasm?url';
import { listenPtyEvents, ptyWrite } from '../../pty/bridge';
import { installTerminalKeyHandler } from '../SessionTerminalWorkspace/terminalKeyHandler';
import type { ScreenSnapshotResult } from '../../hooks/useDaemonSocket';
import type { UISessionState } from '../../types/sessionState';
import { getTerminalTheme, getTerminalAnsiPalette } from '../../utils/terminalSizing';
import { UnifiedGridRenderer } from './UnifiedGridRenderer';
import { GridCompositor, type GridTileSpec } from './GridCompositor';
import type { Rect } from './GridRenderer';
import { GridHiddenSessions, type HiddenGridSession } from './GridHiddenSessions';
import { setGridAutomationHandle, INACTIVE_GRID_STATE } from './gridAutomation';
import {
  FONT_FAMILY,
  FONT_SIZE,
  TERMINAL_SCROLLBACK_LINES,
  colorNumber,
  measureCanonicalCell,
} from './gridConfig';
import {
  persistGridStatePresentation,
  readGridStatePresentation,
  type GridStatePresentation,
} from './gridStatePresentation';
import './grid.css';

export interface GridSessionTile {
  runtimeId: string;
  // Stable session identity, used to remove/restore the tile from the grid (the
  // runtimeId can change across restarts; the sessionId does not).
  sessionId: string;
  title: string;
  attention: boolean;
  state: UISessionState;
}

interface GridViewProps {
  tiles: GridSessionTile[];
  // Concrete grid shape, resolved upstream (App). The grid is layout-dumb: it
  // simply lays `tiles` into this rows×cols. App slices `tiles` to fit, so
  // tiles.length is always <= rows*cols.
  layout: { rows: number; cols: number };
  // How many live sessions did NOT fit the chosen fixed shape (off-board). Drives
  // a "not shown" hint. 0 in Auto mode (Auto always fits).
  offBoardCount?: number;
  // Sessions the user removed from the grid (for the restore control), and the
  // remove/restore handlers. Optional so the grid still runs without membership
  // wiring (tests / no daemon socket).
  hiddenSessions?: HiddenGridSession[];
  onRemoveTile?: (sessionId: string) => void;
  onRestoreTile?: (sessionId: string) => void;
  resolvedTheme: Parameters<typeof getTerminalTheme>[0];
  // Fetch a session's current screen to seed its tile. Optional so the grid
  // still runs (live-fill only) in contexts without a daemon socket (tests).
  getScreenSnapshot?: (runtimeId: string) => Promise<ScreenSnapshotResult | null>;
}

const RESET_BYTES = new TextEncoder().encode('\x1bc');

function b64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const len = binary.length;
  const out = new Uint8Array(len);
  for (let i = 0; i < len; i += 1) out[i] = binary.charCodeAt(i);
  return out;
}

const toSpecs = (tiles: GridSessionTile[]): GridTileSpec[] =>
  tiles.map((t) => ({ id: t.runtimeId, attention: t.attention, state: t.state }));

export function GridView({
  tiles,
  layout,
  offBoardCount = 0,
  hiddenSessions = [],
  onRemoveTile,
  onRestoreTile,
  resolvedTheme,
  getScreenSnapshot,
}: GridViewProps) {
  const stageRef = useRef<HTMLDivElement | null>(null);
  const compRef = useRef<GridCompositor | null>(null);
  const tilesRef = useRef(tiles);
  tilesRef.current = tiles;
  const [statePresentation, setStatePresentation] = useState<GridStatePresentation>(readGridStatePresentation);
  const statePresentationRef = useRef(statePresentation);
  statePresentationRef.current = statePresentation;
  const selectStatePresentation = useCallback((presentation: GridStatePresentation) => {
    setStatePresentation(presentation);
    persistGridStatePresentation(presentation);
    compRef.current?.setStatePresentation(presentation);
  }, []);

  // runtimeId -> sessionId, so the hover-remove button (which knows the
  // compositor's tile id == runtimeId) can report the stable session identity.
  const sessionIdByRuntime = useMemo(() => {
    const map = new Map<string, string>();
    for (const t of tiles) map.set(t.runtimeId, t.sessionId);
    return map;
  }, [tiles]);

  // The tile currently offering a remove (×) button: the one under the pointer in
  // the static overview. Cleared while zoomed/animating and when the pointer
  // leaves the grid. rect is container-space (aligns with the canvas tiles).
  const [removeTarget, setRemoveTarget] = useState<{ sessionId: string; rect: Rect } | null>(null);
  // Read the live layout from a ref inside the mount/sync effects so they need
  // not re-run when only the shape changes (a dedicated effect handles that).
  const layoutRef = useRef(layout);
  layoutRef.current = layout;

  // Seeding bookkeeping. Each live runtime id maps to a generation number so we
  // seed it exactly once per appearance: an id already in the map is skipped;
  // ids no longer live are pruned (so a session that returns re-seeds with a
  // fresh generation). The generation also invalidates a stale in-flight fetch
  // if the tile was removed and re-added mid-round-trip. Read via refs so the
  // mount effect need not re-run when the snapshot fetcher's identity changes.
  const seedGenRef = useRef<Map<string, number>>(new Map());
  const seedCounterRef = useRef(0);
  const getSnapshotRef = useRef(getScreenSnapshot);
  getSnapshotRef.current = getScreenSnapshot;

  // Reconcile seeding against the current tile set: prune dead ids, then begin
  // seeding any new id and paint its snapshot when it arrives. Ref-stable (reads
  // everything from refs) so both effects can call the same instance.
  const reconcileSeeding = useRef((comp: GridCompositor) => {
    const liveIds = new Set(tilesRef.current.map((t) => t.runtimeId));
    for (const id of [...seedGenRef.current.keys()]) {
      if (!liveIds.has(id)) seedGenRef.current.delete(id);
    }
    const fetchSnapshot = getSnapshotRef.current;
    for (const id of liveIds) {
      if (seedGenRef.current.has(id)) continue;
      const gen = (seedCounterRef.current += 1);
      seedGenRef.current.set(id, gen);
      if (!fetchSnapshot) continue; // no daemon socket: live-fill only
      comp.beginSeeding(id);
      fetchSnapshot(id)
        .then((result) => {
          // Skip if superseded by a remove+re-add, or the compositor was torn
          // down / the tile vanished while the fetch was in flight.
          if (seedGenRef.current.get(id) !== gen) return;
          if (compRef.current !== comp || !comp.hasTile(id)) return;
          if (!result) {
            comp.cancelSeeding(id);
            return;
          }
          const bytes = result.screenSnapshot ? b64ToBytes(result.screenSnapshot) : new Uint8Array(0);
          comp.seedTile(id, bytes, result.lastSeq, result.screenCols, result.screenRows);
        })
        .catch(() => {
          if (seedGenRef.current.get(id) === gen && compRef.current === comp) comp.cancelSeeding(id);
        });
    }
  }).current;

  // A content signature so the sync effect only fires on real changes, not on
  // every parent render (the tiles array is rebuilt each render upstream).
  const signature = useMemo(
    () => tiles.map((t) => `${t.runtimeId}:${t.state}:${t.attention ? 1 : 0}`).join('|'),
    [tiles],
  );

  // Create the renderer + compositor once per theme; tear down on unmount so the
  // single WebGL context is released deterministically (mirrors the pane path).
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;

    let disposed = false;
    let unlisten: (() => void) | null = null;
    let inputHandler: InputHandler | null = null;
    const metrics = measureCanonicalCell();
    const theme = getTerminalTheme(resolvedTheme);
    const renderer = new UnifiedGridRenderer(FONT_SIZE, FONT_FAMILY, metrics, {
      background: theme.background,
      foreground: theme.foreground,
      cursor: theme.cursor,
    });

    void Ghostty.load(ghosttyWasmUrl).then((ghostty) => {
      if (disposed) return;
      const comp = new GridCompositor(renderer, ghostty, stage, metrics, {
        scrollbackLimit: TERMINAL_SCROLLBACK_LINES,
        fgColor: colorNumber(theme.foreground),
        bgColor: colorNumber(theme.background),
        cursorColor: colorNumber(theme.cursor),
        palette: getTerminalAnsiPalette(resolvedTheme),
      });
      compRef.current = comp;
      const current = tilesRef.current;
      comp.setStatePresentation(statePresentationRef.current);
      comp.syncTiles(toSpecs(current));
      comp.setLayout(layoutRef.current.rows, layoutRef.current.cols);
      reconcileSeeding(comp);
      comp.start();

      // Keyboard input for the zoomed tile. The InputHandler attaches keydown to
      // the stage (and makes it focusable); we encode against the zoomed model's
      // modes and forward bytes to that session. With no tile zoomed there is no
      // target, so overview keystrokes are swallowed rather than leaked.
      const forward = (data: string) => {
        const id = compRef.current?.zoomedId();
        if (id) void ptyWrite({ id, data });
      };
      inputHandler = new InputHandler(
        ghostty,
        stage,
        forward,
        () => {},
        undefined,
        (event) => !installTerminalKeyHandler(forward)(event),
        (mode) => compRef.current?.getMode(mode) ?? false,
      );
      // Take focus off the underlying (hidden but mounted) terminal so its
      // InputHandler stops receiving keys; closing the grid re-focuses the active
      // pane via SessionTerminalWorkspace's own visibility effect.
      stage.focus({ preventScroll: true });

      // Publish a read/zoom handle for the UI automation bridge (testing only).
      setGridAutomationHandle({
        getState: () => {
          const c = compRef.current;
          if (!c) return INACTIVE_GRID_STATE;
          const tileStates = c.tileSummaries();
          return {
            active: true,
            tileCount: tileStates.length,
            zoomedId: c.zoomedId(),
            layout: c.currentLayout(),
            statePresentation: statePresentationRef.current,
            stats: c.getStats(),
            tiles: tileStates,
          };
        },
        getTileText: (id) => compRef.current?.getTileText(id) ?? null,
        zoom: (id) => compRef.current?.zoomTo(id),
        setStatePresentation: selectStatePresentation,
        hitTest: (x, y) => compRef.current?.hitTest(x, y) ?? null,
        sendText: (text) => {
          const stageEl = stageRef.current;
          if (!stageEl) return false;
          stageEl.focus({ preventScroll: true });
          for (const ch of text) {
            const enter = ch === '\n' || ch === '\r';
            stageEl.dispatchEvent(new KeyboardEvent('keydown', {
              key: enter ? 'Enter' : ch,
              code: enter ? 'Enter' : undefined,
              bubbles: true,
              cancelable: true,
            }));
          }
          return true;
        },
      });
    });

    // One firehose listener for the grid's whole lifetime. Bytes for sessions we
    // aren't tiling are ignored; responses are drained inside the compositor.
    void listenPtyEvents((evt) => {
      const comp = compRef.current;
      if (!comp) return;
      const p = evt.payload;
      if (p.event === 'data') {
        if (comp.hasTile(p.id)) comp.writeBytes(p.id, b64ToBytes(p.data), p.seq);
      } else if (p.event === 'local_resize') {
        // Keep the tile model matching the session's live geometry so the
        // (geometry-dependent) snapshot and subsequent output render correctly.
        if (comp.hasTile(p.id)) comp.resizeTile(p.id, p.cols, p.rows);
      } else if (p.event === 'reset') {
        if (comp.hasTile(p.id)) comp.writeBytes(p.id, RESET_BYTES);
      }
    }).then((dispose) => {
      if (disposed) dispose();
      else unlisten = dispose;
    });

    return () => {
      disposed = true;
      setGridAutomationHandle(null);
      inputHandler?.dispose();
      unlisten?.();
      // Forget seeding state so a rebuilt compositor (e.g. theme change) re-seeds
      // its fresh, blank tile models.
      seedGenRef.current.clear();
      const comp = compRef.current;
      compRef.current = null;
      if (comp) comp.dispose();
      else renderer.dispose();
    };
  }, [resolvedTheme, reconcileSeeding, selectStatePresentation]);

  // Reconcile the live tile set whenever sessions change. Layout is applied by
  // the dedicated effect below; setLayout here keeps the reflow snapshot aligned
  // with the new tile set when a tile change also shifts the (Auto) shape.
  useEffect(() => {
    const comp = compRef.current;
    if (!comp) return;
    const current = tilesRef.current;
    comp.syncTiles(toSpecs(current));
    comp.setLayout(layoutRef.current.rows, layoutRef.current.cols);
    reconcileSeeding(comp);
  }, [signature, reconcileSeeding]);

  // Apply the grid shape whenever it changes (manual pick, or an Auto recompute
  // as the tile count crosses a near-square boundary). setLayout is idempotent on
  // unchanged dims, so the overlap with the sync effect above is a safe no-op.
  useEffect(() => {
    compRef.current?.setLayout(layout.rows, layout.cols);
  }, [layout.rows, layout.cols]);

  // Click toggles zoom; Esc exits zoom.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      const comp = compRef.current;
      if (comp?.isZoomed()) {
        e.preventDefault();
        e.stopPropagation();
        comp.zoomTo(null);
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, []);

  const onStageClick = (e: React.MouseEvent) => {
    const comp = compRef.current;
    if (!comp) return;
    if (comp.isZoomed()) {
      comp.zoomTo(null);
      return;
    }
    const id = comp.hitTest(e.clientX, e.clientY);
    if (id) {
      comp.zoomTo(id);
      setRemoveTarget(null); // a zoomed tile shows no remove button
      // Ensure the stage owns focus so the zoomed tile receives keyboard input
      // (a background click that hits no tile leaves focus as-is).
      stageRef.current?.focus({ preventScroll: true });
    }
  };

  // Offer a remove (×) button on the tile under the pointer, but only in the
  // static overview — never while zoomed (the rect would be mid-morph). The
  // handlers live on .grid-view (not the stage) so moving onto the × button,
  // which is a .grid-view child, doesn't fire a stage mouseleave and flicker it.
  const updateRemoveTarget = (e: React.MouseEvent) => {
    const comp = compRef.current;
    if (!comp || !onRemoveTile || comp.isZoomed()) {
      if (removeTarget) setRemoveTarget(null);
      return;
    }
    const hit = comp.tileAt(e.clientX, e.clientY);
    const sessionId = hit ? sessionIdByRuntime.get(hit.id) : undefined;
    if (!hit || !sessionId) {
      if (removeTarget) setRemoveTarget(null);
      return;
    }
    // The same overview tile keeps a stable rect, so skip a no-op re-render.
    if (removeTarget && removeTarget.sessionId === sessionId) return;
    setRemoveTarget({ sessionId, rect: hit.rect });
  };

  const clearRemoveTarget = () => {
    if (removeTarget) setRemoveTarget(null);
  };

  return (
    <div className="grid-view" onMouseMove={updateRemoveTarget} onMouseLeave={clearRemoveTarget}>
      <div className="grid-view-stage" ref={stageRef} onClick={onStageClick} />
      <div className="grid-state-presentation" role="radiogroup" aria-label="Session state appearance">
        <span className="grid-state-presentation-label">State</span>
        {(['border', 'background'] as const).map((presentation) => (
          <button
            key={presentation}
            type="button"
            className={`grid-state-presentation-option ${statePresentation === presentation ? 'active' : ''}`}
            data-presentation={presentation}
            role="radio"
            aria-checked={statePresentation === presentation}
            onClick={() => selectStatePresentation(presentation)}
          >
            {presentation === 'border' ? 'Border' : 'Tint'}
          </button>
        ))}
      </div>
      {tiles.length === 0 && (
        <div className="grid-view-empty">No active sessions</div>
      )}
      {removeTarget && (
        <button
          type="button"
          className="grid-tile-remove"
          style={{
            left: `${removeTarget.rect.x + removeTarget.rect.w - 28}px`,
            top: `${removeTarget.rect.y + 8}px`,
          }}
          title="Remove from grid"
          aria-label="Remove from grid"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            onRemoveTile?.(removeTarget.sessionId);
            setRemoveTarget(null);
          }}
        >
          ×
        </button>
      )}
      <GridHiddenSessions sessions={hiddenSessions} onRestore={(id) => onRestoreTile?.(id)} />
      {offBoardCount > 0 && (
        <div className="grid-view-offboard">
          {offBoardCount} more {offBoardCount === 1 ? 'session' : 'sessions'} not shown · enlarge the grid or pick Auto
        </div>
      )}
      <div className="grid-view-hint">
        click a tile to zoom &amp; type{onRemoveTile ? ' · hover a tile to remove it' : ''} · Esc to exit zoom · ⌘G closes grid
      </div>
    </div>
  );
}
