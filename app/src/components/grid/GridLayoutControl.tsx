// Sidebar control for choosing the grid's shape. A small grid-glyph button opens
// a popover with an "Auto" chip and a square picker: hovering a cell highlights
// the top-left→cursor rectangle (rows × cols), and clicking commits that shape.
// Selecting any size also opens grid mode, so the picker doubles as the grid
// launcher (there is otherwise no grid button — only ⌘G).
import { useEffect, useRef, useState } from 'react';
import {
  AUTO_LAYOUT,
  MAX_GRID_COLS,
  MAX_GRID_ROWS,
  type GridLayout,
} from './gridLayout';

function GridGlyph() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2" y="2" width="5" height="5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <rect x="9" y="2" width="5" height="5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <rect x="2" y="9" width="5" height="5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <rect x="9" y="9" width="5" height="5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  );
}

interface GridLayoutControlProps {
  layout: GridLayout;
  // Commit a layout choice. Selecting from the picker both sets the shape and
  // opens grid mode (the control doubles as the grid launcher).
  onSelect: (layout: GridLayout) => void;
  maxRows?: number;
  maxCols?: number;
}

export function GridLayoutControl({
  layout,
  onSelect,
  maxRows = MAX_GRID_ROWS,
  maxCols = MAX_GRID_COLS,
}: GridLayoutControlProps) {
  const [open, setOpen] = useState(false);
  // 1-based hovered cell (rows × cols); null when not hovering a cell.
  const [hover, setHover] = useState<{ rows: number; cols: number } | null>(null);
  const anchorRef = useRef<HTMLDivElement | null>(null);

  // Close on outside click / Escape; reset hover when closing.
  useEffect(() => {
    if (!open) {
      setHover(null);
      return;
    }
    const onDown = (e: MouseEvent) => {
      if (anchorRef.current && !anchorRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('mousedown', onDown, true);
    window.addEventListener('keydown', onKey, true);
    return () => {
      window.removeEventListener('mousedown', onDown, true);
      window.removeEventListener('keydown', onKey, true);
    };
  }, [open]);

  const fixed = layout.mode === 'fixed' ? layout : null;
  // What the picker highlights: the hovered rectangle, else the saved selection.
  const active = hover ?? (fixed ? { rows: fixed.rows, cols: fixed.cols } : null);
  const label = hover
    ? `${hover.rows} × ${hover.cols}`
    : fixed
      ? `${fixed.rows} × ${fixed.cols}`
      : 'Auto';

  const commit = (next: GridLayout) => {
    onSelect(next);
    setOpen(false);
  };

  return (
    <div className="grid-layout-anchor" ref={anchorRef}>
      <button
        type="button"
        className={`sidebar-tool-btn grid-layout-btn ${open ? 'active' : ''}`}
        onClick={() => setOpen((v) => !v)}
        title="Grid layout"
        aria-label="Grid layout"
        aria-expanded={open}
      >
        <GridGlyph />
      </button>
      {open && (
        <div className="grid-layout-popover" role="dialog" aria-label="Grid layout">
          <button
            type="button"
            className={`grid-layout-auto ${layout.mode === 'auto' ? 'active' : ''}`}
            onMouseEnter={() => setHover(null)}
            onClick={() => commit(AUTO_LAYOUT)}
          >
            Auto
          </button>
          <div
            className="grid-layout-grid"
            role="group"
            aria-label="Pick grid size"
            style={{ gridTemplateColumns: `repeat(${maxCols}, 1fr)` }}
            onMouseLeave={() => setHover(null)}
          >
            {Array.from({ length: maxRows * maxCols }, (_, i) => {
              const r = Math.floor(i / maxCols) + 1;
              const c = (i % maxCols) + 1;
              const on = active ? r <= active.rows && c <= active.cols : false;
              return (
                <button
                  key={i}
                  type="button"
                  className={`grid-layout-cell ${on ? 'on' : ''}`}
                  aria-label={`${r} by ${c}`}
                  data-rc={`${r}x${c}`}
                  onMouseEnter={() => setHover({ rows: r, cols: c })}
                  onFocus={() => setHover({ rows: r, cols: c })}
                  onClick={() => commit({ mode: 'fixed', rows: r, cols: c })}
                />
              );
            })}
          </div>
          <div className="grid-layout-label">{label}</div>
        </div>
      )}
    </div>
  );
}
