import { Terminal as XTerm } from '@xterm/xterm';
import type { TerminalHandle } from '../Terminal';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import { recordTerminalRuntimeLog } from '../../utils/terminalRuntimeLog';
import {
  focusTerminalViewport,
  resetTerminalViewport,
  scrollTerminalViewportToTop,
} from '../../utils/terminalViewportActions';
import { resetTerminalScrollPin } from '../../utils/terminalScrollPin';
import { snapshotVisibleTerminalContent, type TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import { snapshotVisibleTerminalStyleSummary, type TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';
import { snapshotTerminalText } from './paneRuntimeTerminalUtils';
import type { PaneRuntimeSize } from './paneRuntimeLifecycleState';

interface PaneRuntimeControlPane {
  paneId: string;
  runtimeId?: string;
  sessionId?: string;
  testSessionId?: string;
}

interface PaneRuntimeControlsDependencies {
  activePaneId: string;
  getCurrentPane: (paneId: string) => PaneRuntimeControlPane | undefined;
  getTerminalHandle: (paneId: string) => TerminalHandle | undefined;
  getXterm: (paneId: string) => XTerm | undefined;
  clearPendingTerminalEvents: (paneId: string) => void;
  injectPanePayload: (paneId: string, payload: Uint8Array | string, encoding: 'bytes' | 'base64') => Promise<boolean>;
  drainPaneWriteChain: (paneId: string) => Promise<void> | undefined;
}

export function createPaneRuntimeControls({
  activePaneId,
  getCurrentPane,
  getTerminalHandle,
  getXterm,
  clearPendingTerminalEvents,
  injectPanePayload,
  drainPaneWriteChain,
}: PaneRuntimeControlsDependencies) {
  const focusPane = (paneId: string, retries = 20) => {
    const tryFocus = (remaining: number) => {
      const pane = getCurrentPane(paneId);
      const handle = getTerminalHandle(paneId);
      const xterm = getXterm(paneId);
      const focusResult = focusTerminalViewport(handle ?? null, xterm);
      if (focusResult === 'handle') {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus via terminal handle succeeded',
          details: activeElementSummary,
        });
        recordTerminalRuntimeLog({
          category: 'focus',
          event: 'focus.acquired',
          sessionId: pane?.sessionId ?? pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus acquired via terminal handle',
          details: activeElementSummary,
        });
        return;
      }
      if (focusResult === 'xterm') {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus via xterm fallback',
          details: activeElementSummary,
        });
        recordTerminalRuntimeLog({
          category: 'focus',
          event: 'focus.acquired',
          sessionId: pane?.sessionId ?? pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus acquired via xterm fallback',
          details: activeElementSummary,
        });
        return;
      }
      if (remaining <= 0) {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus retries exhausted',
          details: activeElementSummary,
        });
        return;
      }
      window.setTimeout(() => tryFocus(remaining - 1), 50);
    };
    tryFocus(retries);
  };

  const fitPane = (paneId: string) => {
    getTerminalHandle(paneId)?.fit();
  };

  const fitActivePane = () => {
    fitPane(activePaneId);
  };

  const typeTextViaPaneInput = (paneId: string, text: string) => {
    return getTerminalHandle(paneId)?.typeTextViaInput(text) || false;
  };

  const isPaneInputFocused = (paneId: string) => {
    return getTerminalHandle(paneId)?.isInputFocused() || false;
  };

  const scrollPaneToTop = (paneId: string) => {
    return scrollTerminalViewportToTop(getXterm(paneId), resetTerminalScrollPin);
  };

  const getPaneText = (paneId: string) => {
    return snapshotTerminalText(getXterm(paneId) || null);
  };

  const getPaneSize = (paneId: string): PaneRuntimeSize | null => {
    const xterm = getXterm(paneId);
    if (!xterm) {
      return null;
    }
    return { cols: xterm.cols, rows: xterm.rows };
  };

  const getPaneVisibleContent = (paneId: string): TerminalVisibleContentSnapshot => {
    return snapshotVisibleTerminalContent(getXterm(paneId) || null);
  };

  const getPaneVisibleStyleSummary = (paneId: string): TerminalVisibleStyleSnapshot => {
    return snapshotVisibleTerminalStyleSummary(getXterm(paneId) || null);
  };

  const resetPaneTerminal = (paneId: string) => {
    const xterm = getXterm(paneId);
    if (!resetTerminalViewport(xterm, resetTerminalScrollPin)) {
      return false;
    }
    clearPendingTerminalEvents(paneId);
    return true;
  };

  const injectPaneBytes = async (paneId: string, bytes: Uint8Array) => {
    return injectPanePayload(paneId, bytes, 'bytes');
  };

  const injectPaneBase64 = async (paneId: string, payload: string) => {
    return injectPanePayload(paneId, payload, 'base64');
  };

  const drainPaneTerminal = async (paneId: string) => {
    if (!getXterm(paneId)) {
      return false;
    }
    await drainPaneWriteChain(paneId);
    return true;
  };

  return {
    focusPane,
    fitPane,
    fitActivePane,
    typeTextViaPaneInput,
    isPaneInputFocused,
    scrollPaneToTop,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    getPaneVisibleStyleSummary,
    resetPaneTerminal,
    injectPaneBytes,
    injectPaneBase64,
    drainPaneTerminal,
  };
}
