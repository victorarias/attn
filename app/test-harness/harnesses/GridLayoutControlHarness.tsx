/**
 * GridLayoutControl Test Harness
 *
 * Renders the sidebar grid layout picker in isolation (real CSS, no daemon) so
 * Playwright can screenshot the popover, the hover highlight, and the active
 * states. ?mode=fixed starts with a saved 2×3 selection; default starts on Auto.
 */
import { useEffect, useState } from 'react';
import { GridLayoutControl } from '../../src/components/grid/GridLayoutControl';
import { AUTO_LAYOUT, type GridLayout } from '../../src/components/grid/gridLayout';
import type { HarnessProps } from '../types';
import '../../src/App.css';
import '../../src/components/Sidebar.css';

export function GridLayoutControlHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const initial: GridLayout =
    params.get('mode') === 'fixed' ? { mode: 'fixed', rows: 2, cols: 3 } : AUTO_LAYOUT;
  const [layout, setLayout] = useState<GridLayout>(initial);

  useEffect(() => {
    setTriggerRerender(() => () => setLayout((l) => ({ ...l })));
  }, [setTriggerRerender]);

  useEffect(() => {
    const timer = setTimeout(() => onReady(), 100);
    return () => clearTimeout(timer);
  }, [onReady]);

  return (
    <div
      className="sidebar sidebar--display-boxed"
      style={{ width: 280, minHeight: 360, padding: 16, background: 'var(--color-bg-panel)' }}
    >
      <div className="sidebar-header">
        <div className="sidebar-tool-row">
          <GridLayoutControl
            layout={layout}
            onSelect={(next) => {
              window.__HARNESS__.recordCall('onSelect', [next]);
              setLayout(next);
            }}
          />
        </div>
      </div>
    </div>
  );
}
