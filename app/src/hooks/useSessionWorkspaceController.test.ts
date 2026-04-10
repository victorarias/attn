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

function buildVisibleContent(text: string) {
  return {
    cols: 80,
    viewportY: 0,
    lineCount: 1,
    lines: [text],
    lineMetrics: [{ rowOffset: 0, text, occupiedColumns: text.length, occupiedWidthRatio: text.length / 80, nonEmpty: true }],
    summary: {
      nonEmptyLineCount: 1,
      denseLineCount: 0,
      charCount: text.length,
      maxLineLength: text.length,
      maxOccupiedColumns: text.length,
      maxOccupiedWidthRatio: text.length / 80,
      medianOccupiedWidthRatio: text.length / 80,
      meanOccupiedWidthRatio: text.length / 80,
      wideLineCount: 0,
      uniqueTrimmedLineCount: 1,
      firstNonEmptyLine: text,
      lastNonEmptyLine: text,
    },
  };
}

function buildVisibleStyleSummary({
  text = '',
  styledCellCount = 0,
  boldCellCount = 0,
  fgPaletteCellCount = 0,
}: {
  text?: string;
  styledCellCount?: number;
  boldCellCount?: number;
  fgPaletteCellCount?: number;
} = {}) {
  return {
    cols: 80,
    rows: 24,
    viewportY: 0,
    lineCount: 1,
    lines: text ? [{
      rowOffset: 0,
      text,
      styledCellCount,
      boldCellCount,
      italicCellCount: 0,
      underlineCellCount: 0,
      inverseCellCount: 0,
      fgPaletteCellCount,
      fgRgbCellCount: 0,
      bgPaletteCellCount: 0,
      bgRgbCellCount: 0,
    }] : [],
    summary: {
      styledCellCount,
      styledLineCount: text ? 1 : 0,
      boldCellCount,
      italicCellCount: 0,
      underlineCellCount: 0,
      inverseCellCount: 0,
      fgPaletteCellCount,
      fgRgbCellCount: 0,
      bgPaletteCellCount: 0,
      bgRgbCellCount: 0,
      uniqueStyleCount: styledCellCount > 0 ? 1 : 0,
    },
  };
}

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
    const getPaneVisibleContent = vi.fn(() => buildVisibleContent('pane text'));
    const getPaneVisibleStyleSummary = vi.fn(() => buildVisibleStyleSummary({
      text: 'pane text',
      styledCellCount: 4,
      boldCellCount: 4,
      fgPaletteCellCount: 4,
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
        getPaneVisibleStyleSummary,
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
        getPaneVisibleContent: vi.fn(() => buildVisibleContent('text')),
        getPaneVisibleStyleSummary: vi.fn(() => buildVisibleStyleSummary()),
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
