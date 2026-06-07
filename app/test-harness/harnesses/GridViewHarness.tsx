/**
 * GridView Test Harness
 *
 * Renders the real grid (unified WebGL renderer + ghostty models, mock PTY) so
 * Playwright can exercise the membership affordances end-to-end: hover a tile to
 * reveal its remove (×) button, remove it, then restore it from the "N hidden"
 * control. Membership state lives here, mirroring App, so removes/restores
 * actually reflow the grid.
 *
 * `?layout=fixed` uses a fixed 2×2 shape with four tiles. Removing one then
 * leaves the resolved shape at 2×2 (the dims do NOT change), which is the exact
 * scenario where the render-on-demand loop used to skip the repaint and leave
 * the removed tile's stale frame on screen ("only every other hide hides").
 */
import { useEffect, useMemo, useState } from 'react';
import { GridView, type GridSessionTile } from '../../src/components/grid/GridView';
import type { HarnessProps } from '../types';

const BASE_TILES: GridSessionTile[] = [
  { runtimeId: 'rt-1', sessionId: 's1', title: 'api server', attention: false },
  { runtimeId: 'rt-2', sessionId: 's2', title: 'web client', attention: true },
  { runtimeId: 'rt-3', sessionId: 's3', title: 'worker', attention: false },
];

const FIXED_EXTRA: GridSessionTile = { runtimeId: 'rt-4', sessionId: 's4', title: 'database', attention: false };

const CONTENT: Record<string, string> = {
  'rt-1': '\x1b[2J\x1b[H$ api server listening on :8080\r\n',
  'rt-2': '\x1b[2J\x1b[H> web client building...\r\n',
  'rt-3': '\x1b[2J\x1b[H# worker idle\r\n',
  'rt-4': '\x1b[2J\x1b[H% database ready\r\n',
};

export function GridViewHarness({ onReady }: HarnessProps) {
  // Fixed 2×2 over four tiles reproduces the no-dims-change removal path; the
  // default keeps the auto-style 1×N shape the original membership spec expects.
  const isFixed = new URLSearchParams(window.location.search).get('layout') === 'fixed';
  const allTiles = useMemo(() => (isFixed ? [...BASE_TILES, FIXED_EXTRA] : BASE_TILES), [isFixed]);

  const [excluded, setExcluded] = useState<Set<string>>(new Set());

  const members = useMemo(() => allTiles.filter((t) => !excluded.has(t.sessionId)), [allTiles, excluded]);
  const hidden = useMemo(
    () => allTiles.filter((t) => excluded.has(t.sessionId)).map((t) => ({ sessionId: t.sessionId, title: t.title })),
    [allTiles, excluded],
  );
  const layout = isFixed ? { rows: 2, cols: 2 } : { rows: 1, cols: Math.max(1, members.length) };

  useEffect(() => {
    const ready = setTimeout(() => onReady(), 400);
    // Feed each tile a line of content so the screenshots are meaningful and we
    // confirm the renderer actually paints. The dev-only global is installed by
    // the pty bridge in DEV.
    const feed = setTimeout(() => {
      const emit = window.__TEST_EMIT_PTY_DATA;
      if (emit) {
        for (const t of allTiles) emit(t.runtimeId, CONTENT[t.runtimeId] ?? '');
      }
    }, 700);
    return () => {
      clearTimeout(ready);
      clearTimeout(feed);
    };
  }, [onReady, allTiles]);

  return (
    <div style={{ position: 'fixed', inset: 0, background: '#0d0d0d' }}>
      <GridView
        tiles={members}
        layout={layout}
        hiddenSessions={hidden}
        resolvedTheme="dark"
        onRemoveTile={(id) => {
          window.__HARNESS__.recordCall('onRemove', [id]);
          setExcluded((prev) => new Set(prev).add(id));
        }}
        onRestoreTile={(id) => {
          window.__HARNESS__.recordCall('onRestore', [id]);
          setExcluded((prev) => {
            const next = new Set(prev);
            next.delete(id);
            return next;
          });
        }}
      />
    </div>
  );
}
