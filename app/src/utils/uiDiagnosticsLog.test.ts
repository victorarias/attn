import { afterEach, describe, expect, it, vi } from 'vitest';
import { captureUiSnapshot, probeUiAfterSwitch } from './uiDiagnosticsLog';

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = '';
});

describe('UI diagnostics', () => {
  it('captures the app shell and active terminal wrapper', () => {
    document.body.innerHTML = `
      <div id="root">
        <div class="app">
          <div class="terminal-wrapper active">terminal</div>
        </div>
      </div>
    `;
    Object.defineProperty(document, 'elementFromPoint', {
      configurable: true,
      value: () => document.querySelector('.terminal-wrapper'),
    });

    const snapshot = captureUiSnapshot();

    expect(snapshot.rootChildren).toBe(1);
    expect(snapshot.activeWrapperCount).toBe(1);
    expect(snapshot.app).toMatchObject({ tag: 'div', classes: 'app' });
    expect(snapshot.center).toMatchObject({ classes: 'terminal-wrapper active' });
  });

  it('records delayed probes for the latest agent switch', () => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div id="root"><div class="app" /></div>';
    Object.defineProperty(document, 'elementFromPoint', {
      configurable: true,
      value: () => document.querySelector('.app'),
    });

    probeUiAfterSwitch({ sessionId: 'chief-session', workspaceId: 'chief-workspace', view: 'session' });
    vi.advanceTimersByTime(1500);

    const events = (window.__ATTN_UI_DIAG_DUMP?.() ?? [])
      .filter((event) => event.sessionId === 'chief-session');
    expect(events.map((event) => event.kind)).toEqual([
      'session_switch',
      'switch_probe',
      'switch_probe',
      'switch_probe',
    ]);
    expect(events.slice(1).map((event) => event.delayMs)).toEqual([0, 250, 1500]);
  });

  it('cancels stale delayed probes when another agent is selected', () => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div id="root"><div class="app" /></div>';
    Object.defineProperty(document, 'elementFromPoint', {
      configurable: true,
      value: () => document.querySelector('.app'),
    });

    probeUiAfterSwitch({ sessionId: 'first', workspaceId: 'one', view: 'session' });
    probeUiAfterSwitch({ sessionId: 'second', workspaceId: 'two', view: 'session' });
    vi.advanceTimersByTime(1500);

    const events = window.__ATTN_UI_DIAG_DUMP?.() ?? [];
    expect(events.filter((event) => event.sessionId === 'first' && event.kind === 'switch_probe')).toHaveLength(0);
    expect(events.filter((event) => event.sessionId === 'second' && event.kind === 'switch_probe')).toHaveLength(3);
  });
});

