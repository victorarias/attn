import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import { useSessionWorkspaceController } from './useSessionWorkspaceController';

vi.mock('../components/SessionTerminalWorkspace/paneRuntimeEventRouter', () => ({
  usePaneRuntimeEventRouter: () => ({
    registerBinding: vi.fn(() => () => {}),
  }),
}));

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

describe('useSessionWorkspaceController', () => {
  it('stores workspace handles and exposes imperative pane helpers', () => {
    const session = buildSession();
    const fitActivePane = vi.fn();
    const getPaneText = vi.fn(() => 'pane text');
    const getPaneSize = vi.fn(() => ({ cols: 80, rows: 24 }));

    const { result } = renderHook(() => useSessionWorkspaceController([session], session.id));

    act(() => {
      result.current.setWorkspaceRef(session.id)({
        fitPane: vi.fn(),
        fitActivePane,
        focusPane: vi.fn(),
        focusActivePane: vi.fn(),
        getPaneText,
        getPaneSize,
      });
    });

    act(() => {
      result.current.fitSessionActivePane(session.id);
    });

    expect(fitActivePane).toHaveBeenCalledOnce();
    expect(result.current.getPaneText(session.id, MAIN_TERMINAL_PANE_ID)).toBe('pane text');
    expect(result.current.getPaneSize(session.id, MAIN_TERMINAL_PANE_ID)).toEqual({ cols: 80, rows: 24 });
  });

  it('forgets workspace handles when removed', () => {
    const session = buildSession();
    const { result } = renderHook(() => useSessionWorkspaceController([session], session.id));

    act(() => {
      result.current.setWorkspaceRef(session.id)({
        fitPane: vi.fn(),
        fitActivePane: vi.fn(),
        focusPane: vi.fn(),
        focusActivePane: vi.fn(),
        getPaneText: vi.fn(() => 'text'),
        getPaneSize: vi.fn(() => ({ cols: 80, rows: 24 })),
      });
    });

    act(() => {
      result.current.removeWorkspaceRef(session.id);
    });

    expect(result.current.getPaneText(session.id, MAIN_TERMINAL_PANE_ID)).toBe('');
    expect(result.current.getPaneSize(session.id, MAIN_TERMINAL_PANE_ID)).toBeNull();
  });
});
