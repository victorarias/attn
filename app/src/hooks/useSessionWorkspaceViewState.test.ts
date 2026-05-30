import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Session } from '../store/sessions';
const SESSION_PANE_ID = 'pane-session';
import { useSessionWorkspaceViewState } from './useSessionWorkspaceViewState';

function buildSession(overrides?: Partial<Session>): Session {
  return {
    id: 'session-1',
    label: 'Session 1',
    state: 'idle',
    cwd: '/tmp/repo',
    workspaceId: 'workspace-session-1',
    agent: 'claude',
    transcriptMatched: true,
    workspace: {
      agents: [],
      layoutTree: { type: 'pane', paneId: SESSION_PANE_ID },
    },
    daemonActivePaneId: SESSION_PANE_ID,
    ...overrides,
  };
}

describe('useSessionWorkspaceViewState', () => {
  it('prefers local active pane while topology is stable', () => {
    const session = buildSession({
      workspace: {
        agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: SESSION_PANE_ID },
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
      result.current.setActivePane(session.id, SESSION_PANE_ID);
    });

    expect(result.current.getActivePaneIdForSession(session)).toBe(SESSION_PANE_ID);
  });

  it('falls back to the daemon preferred pane when topology changes', () => {
    const initial = buildSession({
      workspace: {
        agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: SESSION_PANE_ID },
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
      result.current.setActivePane(initial.id, SESSION_PANE_ID);
    });

    const updated = buildSession({
      id: initial.id,
      workspace: {
        agents: [{ id: 'pane-b', runtimeId: 'runtime-b', title: "Session", sessionId: 'session-1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root-2',
          direction: 'horizontal',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: SESSION_PANE_ID },
            { type: 'pane', paneId: 'pane-b' },
          ],
        },
      },
      daemonActivePaneId: 'pane-b',
    });

    rerender({ sessions: [updated] });

    expect(result.current.getActivePaneIdForSession(updated)).toBe('pane-b');
  });

  it('restores focus to the previously active pane after closing the current pane', () => {
    const initial = buildSession({
      workspace: {
        agents: [
          { id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' },
          { id: 'pane-b', runtimeId: 'runtime-b', title: "Session", sessionId: 'session-1' },
        ],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 2 / 3,
          children: [
            {
              type: 'split',
              splitId: 'left',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: SESSION_PANE_ID },
                { type: 'pane', paneId: 'pane-a' },
              ],
            },
            { type: 'pane', paneId: 'pane-b' },
          ],
        },
      },
      daemonActivePaneId: 'pane-b',
    });

    const { result, rerender } = renderHook(({ sessions }) => useSessionWorkspaceViewState(sessions), {
      initialProps: { sessions: [initial] },
    });

    act(() => {
      result.current.setActivePane(initial.id, 'pane-a');
      result.current.setActivePane(initial.id, 'pane-b');
      result.current.prepareClosePaneFocus(initial, 'pane-b');
    });

    const updated = buildSession({
      id: initial.id,
      workspace: {
        agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: SESSION_PANE_ID },
            { type: 'pane', paneId: 'pane-a' },
          ],
        },
      },
      daemonActivePaneId: SESSION_PANE_ID,
    });

    rerender({ sessions: [updated] });

    expect(result.current.getActivePaneIdForSession(updated)).toBe('pane-a');
  });

  it('falls back to the left pane when closing the current pane without prior pane history', () => {
    const initial = buildSession({
      workspace: {
        agents: [{ id: 'pane-b', runtimeId: 'runtime-b', title: "Session", sessionId: 'session-1' }],
        layoutTree: {
          type: 'split',
          splitId: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: SESSION_PANE_ID },
            { type: 'pane', paneId: 'pane-b' },
          ],
        },
      },
      daemonActivePaneId: 'pane-b',
    });

    const { result, rerender } = renderHook(({ sessions }) => useSessionWorkspaceViewState(sessions), {
      initialProps: { sessions: [initial] },
    });

    act(() => {
      result.current.prepareClosePaneFocus(initial, 'pane-b');
    });

    const updated = buildSession({
      id: initial.id,
      workspace: {
        agents: [],
        layoutTree: { type: 'pane', paneId: SESSION_PANE_ID },
      },
      daemonActivePaneId: SESSION_PANE_ID,
    });

    rerender({ sessions: [updated] });

    expect(result.current.getActivePaneIdForSession(updated)).toBe(SESSION_PANE_ID);
  });
});
