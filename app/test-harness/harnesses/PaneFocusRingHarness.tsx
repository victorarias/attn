/**
 * Pane focus treatment test harness
 *
 * Reproduces the exact DOM shape SessionTerminalWorkspace renders for a
 * multi-leaf pane with a bound ticket open. The selected leaf's pseudo-elements
 * paint the edge rail or spotlight corner marks, with a
 * `.workspace-pane-ticket-overlay` covering the pane body underneath. Loads the
 * real stylesheet so the actual cascade/stacking is under test, not a
 * re-description of it. GhosttyTerminal (WASM/canvas) plays no role in this
 * stacking question, so it is not mounted.
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
        style={{ position: 'absolute', inset: 0 }}
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
    </div>
  );
}
