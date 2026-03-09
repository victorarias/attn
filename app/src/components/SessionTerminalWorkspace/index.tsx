import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal, TerminalHandle, type ResolvedTheme } from '../Terminal';
import { useShortcut } from '../../shortcuts';
import { triggerShortcut } from '../../shortcuts/useShortcut';
import {
  MAIN_TERMINAL_PANE_ID,
  findPaneInDirection,
  type TerminalNavigationDirection,
  type TerminalLayoutNode,
  type TerminalSplitDirection,
  type TerminalPanelState,
} from '../../types/workspace';
import {
  listenPtyEvents,
  ptyResize,
  ptySpawn,
  ptyWrite,
  type PtyEventPayload,
} from '../../pty/bridge';
import './SessionTerminalWorkspace.css';

interface SessionTerminalWorkspaceProps {
  sessionId: string;
  cwd: string;
  panel: TerminalPanelState;
  fontSize: number;
  mainPane: React.ReactNode;
  focusMainPane: () => boolean;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
  isActiveSession: boolean;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
}

export function SessionTerminalWorkspace({
  sessionId,
  cwd,
  panel,
  fontSize,
  mainPane,
  focusMainPane,
  resolvedTheme,
  focusRequestToken,
  enabled,
  isActiveSession,
  onSplitPane,
  onClosePane,
  onFocusPane,
  onNavigateOutOfSession,
}: SessionTerminalWorkspaceProps) {
  const terminalRefs = useRef<Map<string, TerminalHandle>>(new Map());
  const xtermRefs = useRef<Map<string, XTerm>>(new Map());
  const inputSubscriptions = useRef<Map<string, { dispose: () => void }>>(new Map());
  const ptyIdToTerminalId = useRef<Map<string, string>>(new Map());
  const pendingTerminalEvents = useRef<Map<string, PtyEventPayload[]>>(new Map());
  const [maximizedPaneId, setMaximizedPaneId] = useState<string | null>(null);

  const paneIds = useMemo(() => {
    const ids: string[] = [];
    const collect = (node: TerminalLayoutNode) => {
      if (node.type === 'pane') {
        ids.push(node.paneId);
        return;
      }
      collect(node.children[0]);
      collect(node.children[1]);
    };
    collect(panel.layoutTree);
    return ids;
  }, [panel.layoutTree]);

  const splitLayoutActive = panel.layoutTree.type === 'split';
  const showMainHeader = paneIds.length > 1;
  const effectivePaneId = maximizedPaneId && paneIds.includes(maximizedPaneId) ? maximizedPaneId : null;

  useEffect(() => {
    if (!maximizedPaneId) {
      return;
    }
    if (!paneIds.includes(maximizedPaneId)) {
      setMaximizedPaneId(null);
    }
  }, [maximizedPaneId, paneIds]);

  const focusTerminalWithRetry = useCallback((terminalId: string, retries = 20) => {
    const tryFocus = (remaining: number) => {
      const xterm = xtermRefs.current.get(terminalId);
      if (xterm) {
        xterm.focus();
        return;
      }
      const handle = terminalRefs.current.get(terminalId);
      if (handle?.terminal) {
        handle.focus();
        return;
      }
      if (remaining <= 0) {
        return;
      }
      window.setTimeout(() => tryFocus(remaining - 1), 50);
    };
    tryFocus(retries);
  }, []);

  const focusMainPaneWithRetry = useCallback((retries = 20) => {
    const tryFocus = (remaining: number) => {
      if (focusMainPane()) {
        return;
      }
      if (remaining <= 0) {
        return;
      }
      window.setTimeout(() => tryFocus(remaining - 1), 50);
    };
    tryFocus(retries);
  }, [focusMainPane]);

  const focusActivePane = useCallback(() => {
    if (panel.activePaneId === MAIN_TERMINAL_PANE_ID) {
      focusMainPaneWithRetry(40);
      return;
    }
    focusTerminalWithRetry(panel.activePaneId, 40);
  }, [focusMainPaneWithRetry, focusTerminalWithRetry, panel.activePaneId]);

  useEffect(() => {
    const activeTerminalIds = new Set(panel.terminals.map((terminal) => terminal.id));

    for (const [terminalId, sub] of inputSubscriptions.current.entries()) {
      if (!activeTerminalIds.has(terminalId)) {
        sub.dispose();
        inputSubscriptions.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(xtermRefs.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        xtermRefs.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(terminalRefs.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        terminalRefs.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(pendingTerminalEvents.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        pendingTerminalEvents.current.delete(terminalId);
      }
    }

    ptyIdToTerminalId.current = new Map(panel.terminals.map((terminal) => [terminal.ptyId, terminal.id]));
  }, [panel.terminals]);

  useEffect(() => {
    if (!isActiveSession || !enabled) {
      return;
    }
    focusActivePane();
  }, [enabled, focusActivePane, focusRequestToken, isActiveSession, panel.activePaneId]);

  const decodePtyData = useCallback((payload: string) => {
    const binaryStr = atob(payload);
    const bytes = Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
    return new TextDecoder('utf-8').decode(bytes);
  }, []);

  const writeToTerminal = useCallback((xterm: XTerm, msg: PtyEventPayload) => {
    switch (msg.event) {
      case 'data':
        xterm.write(decodePtyData(msg.data));
        break;
      case 'reset':
        xterm.reset();
        break;
      case 'exit':
        xterm.write(`\r\n[Process exited with code ${msg.code}]\r\n`);
        break;
      case 'error':
        xterm.write(`\r\n[Error: ${msg.error}]\r\n`);
        break;
      default:
        break;
    }
  }, [decodePtyData]);

  const queueTerminalEvent = useCallback((terminalId: string, msg: PtyEventPayload) => {
    const events = pendingTerminalEvents.current.get(terminalId) || [];
    if (events.length >= 256) {
      events.shift();
    }
    events.push(msg);
    pendingTerminalEvents.current.set(terminalId, events);
  }, []);

  const flushPendingEvents = useCallback((terminalId: string, xterm: XTerm) => {
    const events = pendingTerminalEvents.current.get(terminalId);
    if (!events || events.length === 0) {
      return;
    }
    for (const event of events) {
      writeToTerminal(xterm, event);
    }
    pendingTerminalEvents.current.delete(terminalId);
  }, [writeToTerminal]);

  const handleSplit = useCallback((direction: TerminalSplitDirection) => {
    onSplitPane(panel.activePaneId, direction);
  }, [onSplitPane, panel.activePaneId]);

  const handleNewTerminal = useCallback(() => {
    onSplitPane(panel.activePaneId, 'vertical');
  }, [onSplitPane, panel.activePaneId]);

  const handleCloseTerminal = useCallback((terminalId: string) => {
    onClosePane(terminalId);
  }, [onClosePane]);

  const handleCloseActiveTerminal = useCallback(() => {
    if (panel.activePaneId === MAIN_TERMINAL_PANE_ID) {
      return;
    }
    handleCloseTerminal(panel.activePaneId);
  }, [handleCloseTerminal, panel.activePaneId]);

  const toggleMaximizeActivePane = useCallback(() => {
    setMaximizedPaneId((current) => (current ? null : panel.activePaneId));
  }, [panel.activePaneId]);

  useEffect(() => {
    const unlisten = listenPtyEvents((event) => {
      const msg = event.payload;
      const terminalId = ptyIdToTerminalId.current.get(msg.id);
      if (!terminalId) return;

      const xterm = xtermRefs.current.get(terminalId);
      if (!xterm) {
        queueTerminalEvent(terminalId, msg);
        return;
      }
      flushPendingEvents(terminalId, xterm);
      writeToTerminal(xterm, msg);
    });

    return () => {
      unlisten.then((fn) => fn());
    };
  }, [flushPendingEvents, handleCloseTerminal, queueTerminalEvent, writeToTerminal]);

  const handleMovePane = useCallback((direction: TerminalNavigationDirection) => {
    const visibleLayout: TerminalLayoutNode = effectivePaneId
      ? { type: 'pane', paneId: effectivePaneId }
      : panel.layoutTree;
    const nextPaneId = findPaneInDirection(visibleLayout, panel.activePaneId, direction);
    if (nextPaneId) {
      onFocusPane(nextPaneId);
      return;
    }
    onNavigateOutOfSession(direction);
  }, [effectivePaneId, onFocusPane, onNavigateOutOfSession, panel.activePaneId, panel.layoutTree]);

  const handleMainPaneMouseDown = useCallback(() => {
    onFocusPane(MAIN_TERMINAL_PANE_ID);
    focusMainPaneWithRetry();
  }, [focusMainPaneWithRetry, onFocusPane]);

  const wireTerminal = useCallback((terminalId: string, ptyId: string, xterm: XTerm) => {
    xtermRefs.current.set(terminalId, xterm);
    ptyIdToTerminalId.current.set(ptyId, terminalId);

    xterm.attachCustomKeyEventHandler((ev) => {
      const accel = ev.metaKey || ev.ctrlKey;
      if (ev.type !== 'keydown' || !accel || ev.altKey) {
        return true;
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 't') {
        return !triggerShortcut('terminal.new');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitVertical');
      }
      if (ev.shiftKey && ev.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitHorizontal');
      }
      if (ev.shiftKey && ev.key === 'Enter') {
        return !triggerShortcut('terminal.toggleMaximize');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'w') {
        return !triggerShortcut('terminal.close');
      }
      return true;
    });

    inputSubscriptions.current.get(terminalId)?.dispose();
    const sub = xterm.onData((data: string) => {
      ptyWrite({ id: ptyId, data }).catch(console.error);
    });
    inputSubscriptions.current.set(terminalId, sub);
    flushPendingEvents(terminalId, xterm);
  }, [flushPendingEvents]);

  const restoreTerminalOutput = useCallback(async (ptyId: string, xterm: XTerm) => {
    const cols = xterm.cols > 0 ? xterm.cols : 80;
    const rows = xterm.rows > 0 ? xterm.rows : 24;
    try {
      await ptySpawn({
        args: {
          id: ptyId,
          cwd,
          cols,
          rows,
          shell: true,
        },
      });
    } catch (e) {
      console.error('[SessionTerminalWorkspace] Restore attach failed:', e);
    }
  }, [cwd]);

  const handleTerminalInit = useCallback((terminalId: string, ptyId: string) => (xterm: XTerm) => {
    wireTerminal(terminalId, ptyId, xterm);
    if (isActiveSession && panel.activePaneId === terminalId) {
      xterm.focus();
    }
  }, [isActiveSession, panel.activePaneId, wireTerminal]);

  const handleTerminalReady = useCallback((terminalId: string, ptyId: string) => (xterm: XTerm) => {
    wireTerminal(terminalId, ptyId, xterm);
    void restoreTerminalOutput(ptyId, xterm);
    if (isActiveSession && panel.activePaneId === terminalId) {
      xterm.focus();
    }
  }, [isActiveSession, panel.activePaneId, restoreTerminalOutput, wireTerminal]);

  const handleTerminalResize = useCallback((ptyId: string) => (cols: number, rows: number) => {
    ptyResize({ id: ptyId, cols, rows }).catch(console.error);
  }, []);

  useShortcut('terminal.open', focusActivePane, enabled && isActiveSession);
  useShortcut('terminal.new', () => { void handleNewTerminal(); }, enabled && isActiveSession);
  useShortcut('terminal.splitVertical', () => { handleSplit('vertical'); }, enabled && isActiveSession);
  useShortcut('terminal.splitHorizontal', () => { handleSplit('horizontal'); }, enabled && isActiveSession);
  useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, enabled && isActiveSession);
  useShortcut('terminal.close', handleCloseActiveTerminal, enabled && isActiveSession && splitLayoutActive);
  useShortcut('terminal.focusLeft', () => handleMovePane('left'), enabled && isActiveSession);
  useShortcut('terminal.focusRight', () => handleMovePane('right'), enabled && isActiveSession);
  useShortcut('terminal.focusUp', () => handleMovePane('up'), enabled && isActiveSession);
  useShortcut('terminal.focusDown', () => handleMovePane('down'), enabled && isActiveSession);

  const renderPane = useCallback((node: TerminalLayoutNode): React.ReactNode => {
    if (node.type === 'split') {
      return (
        <div key={node.splitId} className={`workspace-split split-${node.direction}`}>
          {renderPane(node.children[0])}
          {renderPane(node.children[1])}
        </div>
      );
    }

    if (node.paneId === MAIN_TERMINAL_PANE_ID) {
      return (
        <div
          key={node.paneId}
          className={`workspace-pane main-pane ${panel.activePaneId === MAIN_TERMINAL_PANE_ID ? 'active' : ''}`}
          onMouseDown={handleMainPaneMouseDown}
        >
          {showMainHeader && (
            <div className="workspace-pane-header">
              <span className="workspace-pane-title">Session</span>
            </div>
          )}
          <div className="workspace-pane-body">
            {mainPane}
          </div>
        </div>
      );
    }

    const terminal = panel.terminals.find((entry) => entry.id === node.paneId);
    if (!terminal) {
      return null;
    }
    return (
      <div
        key={terminal.id}
        className={`workspace-pane utility-pane ${panel.activePaneId === terminal.id ? 'active' : ''}`}
        onMouseDown={() => onFocusPane(terminal.id)}
      >
        <div className="workspace-pane-header">
          <span className="workspace-pane-title">{terminal.title}</span>
          <button
            type="button"
            className="workspace-pane-close"
            onClick={(event) => {
              event.stopPropagation();
              handleCloseTerminal(terminal.id);
            }}
            title="Close panel (⌘W)"
          >
            ×
          </button>
        </div>
        <div className="workspace-pane-body">
          <Terminal
            ref={(ref) => {
              if (ref) {
                terminalRefs.current.set(terminal.id, ref);
              }
            }}
            fontSize={fontSize}
            resolvedTheme={resolvedTheme}
            debugName={`utility:${sessionId}:${terminal.title}:${terminal.id}`}
            onInit={handleTerminalInit(terminal.id, terminal.ptyId)}
            onReady={handleTerminalReady(terminal.id, terminal.ptyId)}
            onResize={handleTerminalResize(terminal.ptyId)}
          />
        </div>
      </div>
    );
  }, [
    fontSize,
    handleCloseTerminal,
    handleTerminalInit,
    handleTerminalReady,
    handleTerminalResize,
    mainPane,
    handleMainPaneMouseDown,
    onFocusPane,
    panel.activePaneId,
    panel.terminals,
    resolvedTheme,
    sessionId,
    splitLayoutActive,
    toggleMaximizeActivePane,
  ]);

  const focusModeTitle = useMemo(() => {
    if (!effectivePaneId) {
      return '';
    }
    if (effectivePaneId === MAIN_TERMINAL_PANE_ID) {
      return 'Session';
    }
    return panel.terminals.find((terminal) => terminal.id === effectivePaneId)?.title || 'Pane';
  }, [effectivePaneId, panel.terminals]);

  return (
    <div className={`session-terminal-workspace ${effectivePaneId ? 'focus-mode' : ''}`}>
      {effectivePaneId && (
        <div className="workspace-focus-bar">
          <div className="workspace-focus-label">
            <span className="workspace-focus-kicker">Focus Mode</span>
            <span className="workspace-focus-title">{focusModeTitle}</span>
          </div>
          <button
            type="button"
            className="workspace-focus-exit"
            onClick={() => setMaximizedPaneId(null)}
            title="Exit focus mode (⌘⇧Enter)"
          >
            Return to split
          </button>
        </div>
      )}
      <div className="session-terminal-panes">
        {renderPane(effectivePaneId ? { type: 'pane', paneId: effectivePaneId } : panel.layoutTree)}
      </div>
    </div>
  );
}
