/**
 * Pane focus treatment test harness
 *
 * Reproduces the exact DOM shape SessionTerminalWorkspace renders for a
 * multi-leaf workspace with a bound ticket open. The selected leaf's
 * pseudo-elements paint the edge rail or spotlight corner marks, with a
 * `.workspace-pane-ticket-overlay` covering the pane body underneath. Loads the
 * real stylesheet so the actual cascade/stacking is under test, not a
 * re-description of it. GhosttyTerminal (WASM/canvas) plays no role in this
 * stacking question, so it is not mounted. An unselected docked tile witnesses
 * that both focus styles preserve the shared inactive opacity.
 */
import { useEffect } from 'react';
import '../../src/components/SessionTerminalWorkspace/SessionTerminalWorkspace.css';
import type { HarnessProps } from '../types';

export function PaneFocusRingHarness({ onReady, setTriggerRerender }: HarnessProps) {
  useEffect(() => {
    setTriggerRerender(() => () => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div
      className="session-terminal-workspace workspace-selection--rail multi-leaf"
      data-testid="workspace"
      style={{ position: 'relative', width: 400, height: 300 }}
    >
      <div
        className="workspace-pane active"
        data-testid="pane-active"
        style={{ position: 'absolute', inset: '0 50% 0 0' }}
      >
        <div className="workspace-pane-header workspace-pane-header--draggable">
          <span className="workspace-pane-title">shell</span>
        </div>
        <div className="workspace-pane-body">
          <div
            className="workspace-pane-ticket-overlay"
            data-testid="ticket-overlay"
            style={{ background: 'black' }}
          />
        </div>
      </div>
      <div
        className="workspace-pane workspace-pane--tile"
        data-testid="tile-inactive"
        style={{ position: 'absolute', inset: '0 0 0 50%' }}
      >
        <div className="workspace-dock-tile-header">
          <span className="workspace-dock-tile-title">notes.md</span>
        </div>
      </div>
    </div>
  );
}
