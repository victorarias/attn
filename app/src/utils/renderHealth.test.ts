import { describe, expect, it } from 'vitest';
import { buildPaneRenderHealth, buildSessionRenderHealth } from './renderHealth';

describe('renderHealth', () => {
  it('treats a pane with full terminal occupancy as healthy', () => {
    const pane = buildPaneRenderHealth({
      paneId: 'main',
      kind: 'main',
      active: true,
      inputFocused: true,
      size: { cols: 120, rows: 36 },
      paneBounds: { width: 800, height: 600 },
      projectedBounds: { width: 800, height: 600 },
      paneBodyBounds: { width: 800, height: 560 },
      terminalContainerBounds: { width: 800, height: 560 },
      xtermScreenBounds: { width: 800, height: 560 },
      canvasBounds: { width: 800, height: 560 },
      helperTextarea: {
        focused: true,
        disabled: false,
        readOnly: false,
        width: 0,
        height: 0,
      },
      terminal: {
        terminalName: 'main:test',
        sessionId: 'session-1',
        paneId: 'main',
        runtimeId: 'session-1',
        renderer: 'webgl',
        visible: true,
        ready: true,
        writeQueueChunks: 0,
        writeQueueBytes: 0,
        renderCount: 10,
        writeParsedCount: 10,
        lastRenderAt: 1000,
        lastWriteParsedAt: 1000,
        lastResize: null,
      },
    });

    expect(pane.warnings).toEqual([]);
    expect(pane.fill.xtermScreenVsPaneBody.width).toBe(1);
    expect(pane.fill.xtermScreenVsPaneBody.height).toBe(1);
  });

  it('flags underfilled panes and unfocused active input', () => {
    const pane = buildPaneRenderHealth({
      paneId: 'pane-1',
      kind: 'shell',
      active: true,
      inputFocused: false,
      size: { cols: 27, rows: 46 },
      paneBounds: { width: 420, height: 500 },
      projectedBounds: { width: 420, height: 500 },
      paneBodyBounds: { width: 400, height: 460 },
      terminalContainerBounds: { width: 200, height: 460 },
      xtermScreenBounds: { width: 180, height: 420 },
      canvasBounds: { width: 180, height: 420 },
      helperTextarea: {
        focused: false,
        disabled: false,
        readOnly: false,
        width: 0,
        height: 0,
      },
      terminal: {
        terminalName: 'shell:test',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        renderer: 'webgl',
        visible: true,
        ready: true,
        writeQueueChunks: 0,
        writeQueueBytes: 0,
        renderCount: 5,
        writeParsedCount: 5,
        lastRenderAt: 1000,
        lastWriteParsedAt: 1000,
        lastResize: null,
      },
    });

    expect(pane.warnings.map((warning) => warning.code)).toEqual(expect.arrayContaining([
      'terminal_container_underfills_width',
      'xterm_screen_underfills_width',
      'canvas_underfills_width',
      'active_pane_input_unfocused',
    ]));
    expect(pane.flags.suspiciousTerminalSize).toBe(false);
  });

  it('summarizes unhealthy panes per session', () => {
    const session = buildSessionRenderHealth({
      sessionId: 'session-1',
      label: 'demo',
      activePaneId: 'pane-1',
      selected: true,
      panes: [
        {
          paneId: 'main',
          kind: 'main',
          active: false,
          inputFocused: false,
          size: { cols: 120, rows: 36 },
          paneBounds: { width: 800, height: 600 },
          projectedBounds: { width: 800, height: 600 },
          paneBodyBounds: { width: 800, height: 560 },
          terminalContainerBounds: { width: 800, height: 560 },
          xtermScreenBounds: { width: 800, height: 560 },
          canvasBounds: { width: 800, height: 560 },
          helperTextarea: null,
          terminal: null,
        },
        {
          paneId: 'pane-1',
          kind: 'shell',
          active: true,
          inputFocused: false,
          size: { cols: 27, rows: 46 },
          paneBounds: { width: 420, height: 500 },
          projectedBounds: { width: 500, height: 500 },
          paneBodyBounds: { width: 400, height: 460 },
          terminalContainerBounds: { width: 200, height: 460 },
          xtermScreenBounds: { width: 180, height: 420 },
          canvasBounds: { width: 180, height: 420 },
          helperTextarea: {
            focused: false,
            disabled: false,
            readOnly: false,
            width: 0,
            height: 0,
          },
          terminal: {
            terminalName: 'shell:test',
            sessionId: 'session-1',
            paneId: 'pane-1',
            runtimeId: 'runtime-1',
            renderer: 'webgl',
            visible: true,
            ready: true,
            writeQueueChunks: 0,
            writeQueueBytes: 0,
            renderCount: 5,
            writeParsedCount: 5,
            lastRenderAt: 1000,
            lastWriteParsedAt: 1000,
            lastResize: null,
          },
        },
      ],
    });

    expect(session.summary.paneCount).toBe(2);
    expect(session.summary.warningPaneCount).toBe(1);
    expect(session.summary.unhealthyPaneIds).toEqual(['pane-1']);
  });
});
