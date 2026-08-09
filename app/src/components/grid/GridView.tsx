// Grid mode host: every live session as a terminal tile in ONE WebGL context.
// It observes only — it taps the PTY firehose and never attaches or resizes, so
// it can never claim PTY geometry. A zoomed tile takes keyboard input, forwarded
// with ptyWrite, which claims no geometry either.
//
// A new tile is seeded from getScreenSnapshot and the live firehose is deduped
// against the snapshot's sequence watermark, or it would stay blank until the
// session next emits.
import { useEffect, useMemo, useRef, useState } from 'react';
import { InputHandler } from 'ghostty-web';
import { loadGhostty } from '../../ghostty/wasm';
import { listenPtyEvents, ptyWrite } from '../../pty/bridge';
import { installTerminalKeyHandler } from '../SessionTerminalWorkspace/terminalKeyHandler';
import type { ScreenSnapshotResult } from '../../hooks/useDaemonSocket';
import type { UISessionState } from '../../types/sessionState';
import { getTerminalTheme, getTerminalAnsiPalette } from '../../utils/terminalSizing';
import { UnifiedGridRenderer } from './UnifiedGridRenderer';
import { ensureTerminalIconFont } from '../../utils/terminalIconFont';
import { GridCompositor, type GridTileSpec } from './GridCompositor';
import type { Rect } from './GridRenderer';
import { GridHiddenSessions, type HiddenGridSession } from './GridHiddenSessions';
import { setGridAutomationHandle, INACTIVE_GRID_STATE } from './gridAutomation';
import {
  FONT_FAMILY,
  FONT_SIZE,
  TERMINAL_SCROLLBACK_BYTES,
  colorNumber,
  measureCanonicalCell,
} from './gridConfig';
import './grid.css';

export interface GridSessionTile {
  runtimeId: string;
  // The runtimeId changes across restarts; this does not.
  sessionId: string;
  title: string;
  attention: boolean;
  state: UISessionState;
}

interface GridViewProps {
  tiles: GridSessionTile[];
  // Resolved upstream; App slices `tiles` to fit, so tiles.length <= rows*cols.
  layout: { rows: number; cols: number };
  // Live sessions that did not fit the fixed shape; always 0 in Auto mode.
  offBoardCount?: number;
  // Optional so the grid still runs without membership wiring (tests).
  hiddenSessions?: HiddenGridSession[];
  onRemoveTile?: (sessionId: string) => void;
  onRestoreTile?: (sessionId: string) => void;
  resolvedTheme: Parameters<typeof getTerminalTheme>[0];
  // Optional: without it the grid is live-fill only (tests, no daemon socket).
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

  // runtimeId -> sessionId, for the hover-remove button (tile id == runtimeId).
  const sessionIdByRuntime = useMemo(() => {
    const map = new Map<string, string>();
    for (const t of tiles) map.set(t.runtimeId, t.sessionId);
    return map;
  }, [tiles]);

  // rect is container-space, aligning with the canvas tiles.
  const [removeTarget, setRemoveTarget] = useState<{ sessionId: string; rect: Rect } | null>(null);
  // Read from a ref so the mount/sync effects don't re-run on a shape change.
  const layoutRef = useRef(layout);
  layoutRef.current = layout;

  // A generation per live runtime id: seeds each exactly once per appearance and
  // invalidates a fetch still in flight when the tile was removed and re-added.
  const seedGenRef = useRef<Map<string, number>>(new Map());
  const seedCounterRef = useRef(0);
  const getSnapshotRef = useRef(getScreenSnapshot);
  getSnapshotRef.current = getScreenSnapshot;

  // Ref-stable (everything read from refs) so both effects share one instance.
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
          // Superseded by a remove+re-add, or torn down mid-flight.
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

  // The tiles array is rebuilt every parent render; this fires only on changes.
  const signature = useMemo(
    () => tiles.map((t) => `${t.runtimeId}:${t.state}:${t.attention ? 1 : 0}`).join('|'),
    [tiles],
  );

  // One renderer + compositor per theme; unmount releases the single WebGL
  // context deterministically.
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

    void loadGhostty().then((ghostty) => {
      if (disposed) return;
      const comp = new GridCompositor(renderer, ghostty, stage, metrics, {
        scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
        fgColor: colorNumber(theme.foreground),
        bgColor: colorNumber(theme.background),
        cursorColor: colorNumber(theme.cursor),
        palette: getTerminalAnsiPalette(resolvedTheme),
      });
      compRef.current = comp;
      const current = tilesRef.current;
      comp.syncTiles(toSpecs(current));
      comp.setLayout(layoutRef.current.rows, layoutRef.current.cols);
      reconcileSeeding(comp);
      comp.start();

      // Drop blank icon glyphs cached before the Nerd Font loaded; the grid can
      // open before that completes on a cold start.
      void ensureTerminalIconFont(FONT_SIZE).then(() => {
        if (!disposed) renderer.invalidateGlyphCache();
      });

      // With no tile zoomed there is no target, so overview keystrokes are
      // swallowed rather than leaked.
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
      // Take focus off the hidden-but-mounted terminal so its InputHandler stops
      // receiving keys; closing the grid re-focuses the active pane.
      stage.focus({ preventScroll: true });

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
            stats: c.getStats(),
            tiles: tileStates,
          };
        },
        getTileText: (id) => compRef.current?.getTileText(id) ?? null,
        zoom: (id) => compRef.current?.zoomTo(id),
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

    // One firehose listener for the grid's lifetime; untiled sessions are ignored.
    void listenPtyEvents((evt) => {
      const comp = compRef.current;
      if (!comp) return;
      const p = evt.payload;
      if (p.event === 'data') {
        if (comp.hasTile(p.id)) {
          comp.writeBytes(p.id, typeof p.data === 'string' ? b64ToBytes(p.data) : p.data, p.seq);
        }
      } else if (p.event === 'local_resize') {
        // The snapshot and subsequent output are geometry-dependent.
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
      // A rebuilt compositor must re-seed its fresh, blank tile models.
      seedGenRef.current.clear();
      const comp = compRef.current;
      compRef.current = null;
      if (comp) comp.dispose();
      else renderer.dispose();
    };
  }, [resolvedTheme, reconcileSeeding]);

  // setLayout here keeps the reflow snapshot aligned when a tile change also
  // shifts the Auto shape.
  useEffect(() => {
    const comp = compRef.current;
    if (!comp) return;
    const current = tilesRef.current;
    comp.syncTiles(toSpecs(current));
    comp.setLayout(layoutRef.current.rows, layoutRef.current.cols);
    reconcileSeeding(comp);
  }, [signature, reconcileSeeding]);

  // setLayout is idempotent on unchanged dims, so overlapping with the sync
  // effect above is a safe no-op.
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
      // The zoomed tile only receives keys while the stage owns focus.
      stageRef.current?.focus({ preventScroll: true });
    }
  };

  // Overview only: while zoomed the rect would be mid-morph. The handlers live
  // on .grid-view, not the stage, so moving onto the × button — a .grid-view
  // child — fires no mouseleave and cannot flicker it.
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
    if (removeTarget && removeTarget.sessionId === sessionId) return;
    setRemoveTarget({ sessionId, rect: hit.rect });
  };

  const clearRemoveTarget = () => {
    if (removeTarget) setRemoveTarget(null);
  };

  return (
    <div className="grid-view" onMouseMove={updateRemoveTarget} onMouseLeave={clearRemoveTarget}>
      <div className="grid-view-stage" ref={stageRef} onClick={onStageClick} />
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
