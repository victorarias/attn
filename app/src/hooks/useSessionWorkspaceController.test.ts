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
    const getPaneVisibleContent = vi.fn(() => ({
      cols: 80,
      viewportY: 0,
      lineCount: 1,
      lines: ['pane text'],
      lineMetrics: [{ rowOffset: 0, text: 'pane text', occupiedColumns: 9, occupiedWidthRatio: 9 / 80, nonEmpty: true }],
      summary: {
        nonEmptyLineCount: 1,
        denseLineCount: 0,
        charCount: 9,
        maxLineLength: 9,
        maxOccupiedColumns: 9,
        maxOccupiedWidthRatio: 9 / 80,
        medianOccupiedWidthRatio: 9 / 80,
        meanOccupiedWidthRatio: 9 / 80,
        wideLineCount: 0,
        uniqueTrimmedLineCount: 1,
        firstNonEmptyLine: 'pane text',
        lastNonEmptyLine: 'pane text',
      },
    }));

    const { result } = renderHook(() => useSessionWorkspaceController([session], session.id));

    act(() => {
      result.current.setWorkspaceRef(session.id)({
        fitPane: vi.fn(),
        fitActivePane,
        focusPane: vi.fn(),
        focusActivePane: vi.fn(),
        typePaneTextViaUI: vi.fn(() => true),
        isPaneInputFocused: vi.fn(() => true),
        scrollPaneToTop: vi.fn(() => true),
        getPaneText,
        getPaneSize,
        getPaneVisibleContent,
        resetPaneTerminal: vi.fn(() => true),
        injectPaneBytes: vi.fn(async () => true),
        injectPaneBase64: vi.fn(async () => true),
        drainPaneTerminal: vi.fn(async () => true),
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
        typePaneTextViaUI: vi.fn(() => true),
        isPaneInputFocused: vi.fn(() => true),
        scrollPaneToTop: vi.fn(() => true),
        getPaneText: vi.fn(() => 'text'),
        getPaneSize: vi.fn(() => ({ cols: 80, rows: 24 })),
        getPaneVisibleContent: vi.fn(() => ({
          cols: 80,
          viewportY: 0,
          lineCount: 1,
          lines: ['text'],
          lineMetrics: [{ rowOffset: 0, text: 'text', occupiedColumns: 4, occupiedWidthRatio: 4 / 80, nonEmpty: true }],
          summary: {
            nonEmptyLineCount: 1,
            denseLineCount: 0,
            charCount: 4,
            maxLineLength: 4,
            maxOccupiedColumns: 4,
            maxOccupiedWidthRatio: 4 / 80,
            medianOccupiedWidthRatio: 4 / 80,
            meanOccupiedWidthRatio: 4 / 80,
            wideLineCount: 0,
            uniqueTrimmedLineCount: 1,
            firstNonEmptyLine: 'text',
            lastNonEmptyLine: 'text',
          },
        })),
        resetPaneTerminal: vi.fn(() => true),
        injectPaneBytes: vi.fn(async () => true),
        injectPaneBase64: vi.fn(async () => true),
        drainPaneTerminal: vi.fn(async () => true),
      });
    });

    act(() => {
      result.current.removeWorkspaceRef(session.id);
    });

    expect(result.current.getPaneText(session.id, MAIN_TERMINAL_PANE_ID)).toBe('');
    expect(result.current.getPaneSize(session.id, MAIN_TERMINAL_PANE_ID)).toBeNull();
  });
});
