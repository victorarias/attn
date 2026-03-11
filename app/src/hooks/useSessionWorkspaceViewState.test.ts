import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import { useSessionWorkspaceViewState } from './useSessionWorkspaceViewState';

function buildSession(overrides?: Partial<Session>): Session {
  return {
    id: 'session-1',
    label: 'Session 1',
    state: 'idle',
    cwd: '/tmp/repo',
    agent: 'claude',
    transcriptMatched: true,
    workspace: {
      terminals: [],
      layoutTree: { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
    },
    daemonActivePaneId: MAIN_TERMINAL_PANE_ID,
    ...overrides,
  };
}

describe('useSessionWorkspaceViewState', () => {
  it('prefers local active pane while topology is stable', () => {
    const session = buildSession({
      workspace: {
        terminals: [{ id: 'pane-a', ptyId: 'runtime-a', title: 'Shell 1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
            { type: 'pane', paneId: 'pane-a' },
          ],
        },
      },
      daemonActivePaneId: 'pane-a',
    });

    const { result } = renderHook(({ sessions }) => useSessionWorkspaceViewState(sessions), {
      initialProps: { sessions: [session] },
    });

    act(() => {
      result.current.setActivePane(session.id, MAIN_TERMINAL_PANE_ID);
    });

    expect(result.current.getActivePaneIdForSession(session)).toBe(MAIN_TERMINAL_PANE_ID);
  });

  it('falls back to the daemon preferred pane when topology changes', () => {
    const initial = buildSession({
      workspace: {
        terminals: [{ id: 'pane-a', ptyId: 'runtime-a', title: 'Shell 1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
            { type: 'pane', paneId: 'pane-a' },
          ],
        },
      },
      daemonActivePaneId: 'pane-a',
    });

    const { result, rerender } = renderHook(({ sessions }) => useSessionWorkspaceViewState(sessions), {
      initialProps: { sessions: [initial] },
    });

    act(() => {
      result.current.setActivePane(initial.id, MAIN_TERMINAL_PANE_ID);
    });

    const updated = buildSession({
      id: initial.id,
      workspace: {
        terminals: [{ id: 'pane-b', ptyId: 'runtime-b', title: 'Shell 2' }],
        layoutTree: {
          type: 'split',
          splitId: 'root-2',
          direction: 'horizontal',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
            { type: 'pane', paneId: 'pane-b' },
          ],
        },
      },
      daemonActivePaneId: 'pane-b',
    });

    rerender({ sessions: [updated] });

    expect(result.current.getActivePaneIdForSession(updated)).toBe('pane-b');
  });
});
