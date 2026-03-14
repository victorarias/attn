import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal, type ResolvedTheme } from '../Terminal';
import { useShortcut } from '../../shortcuts';
import {
  MAIN_TERMINAL_PANE_ID,
  findPaneInDirection,
  type TerminalNavigationDirection,
  type TerminalLayoutNode,
  type TerminalSplitDirection,
  type TerminalWorkspaceState,
} from '../../types/workspace';
import type { SessionAgent } from '../../types/sessionAgent';
import type { PtySpawnArgs } from '../../pty/bridge';
import { usePaneRuntimeBinder } from './usePaneRuntimeBinder';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import './SessionTerminalWorkspace.css';

export interface SessionTerminalWorkspaceHandle {
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  focusPane: (paneId: string, retries?: number) => void;
  focusActivePane: (retries?: number) => void;
  typePaneTextViaUI: (paneId: string, text: string) => boolean;
  isPaneInputFocused: (paneId: string) => boolean;
  getPaneText: (paneId: string) => string;
  getPaneSize: (paneId: string) => { cols: number; rows: number } | null;
}

interface SessionTerminalWorkspaceProps {
  sessionId: string;
  sessionLabel: string;
  sessionAgent: SessionAgent;
  cwd: string;
  workspace: TerminalWorkspaceState;
  activePaneId: string;
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
  isActiveSession: boolean;
  eventRouter: PaneRuntimeEventRouter;
  getMainPaneSpawnArgs: (cols: number, rows: number) => PtySpawnArgs | null;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
}

export const SessionTerminalWorkspace = forwardRef<SessionTerminalWorkspaceHandle, SessionTerminalWorkspaceProps>(
  function SessionTerminalWorkspace({
    sessionId,
    sessionLabel,
    sessionAgent,
    cwd,
    workspace,
    activePaneId,
    fontSize,
    resolvedTheme,
    focusRequestToken,
    enabled,
    isActiveSession,
    eventRouter,
    getMainPaneSpawnArgs,
    onSplitPane,
    onClosePane,
    onFocusPane,
    onNavigateOutOfSession,
  }, ref) {
    const [maximizedPaneId, setMaximizedPaneId] = useState<string | null>(null);
    const activePaneIdRef = useRef(activePaneId);
    const isActiveSessionRef = useRef(isActiveSession);

    activePaneIdRef.current = activePaneId;
    isActiveSessionRef.current = isActiveSession;

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
      collect(workspace.layoutTree);
      return ids;
    }, [workspace.layoutTree]);

    const runtimePanes = useMemo(() => ([
      {
        paneId: MAIN_TERMINAL_PANE_ID,
        runtimeId: sessionId,
        testSessionId: sessionId,
        getSpawnArgs: ({ cols, rows }: { cols: number; rows: number }) => getMainPaneSpawnArgs(cols, rows),
      },
      ...workspace.terminals.map((terminal) => ({
        paneId: terminal.id,
        runtimeId: terminal.ptyId,
        getSpawnArgs: ({ cols, rows }: { cols: number; rows: number }) => ({
          id: terminal.ptyId,
          cwd,
          cols,
          rows,
          shell: true,
        }),
      })),
    ]), [cwd, getMainPaneSpawnArgs, sessionId, workspace.terminals]);

    const binder = usePaneRuntimeBinder(runtimePanes, activePaneId, eventRouter);
    const splitLayoutActive = workspace.layoutTree.type === 'split';
    const showMainHeader = paneIds.length > 1;
    const effectivePaneId = maximizedPaneId && paneIds.includes(maximizedPaneId) ? maximizedPaneId : null;
    const workspaceTopologyKey = JSON.stringify({
      layoutTree: workspace.layoutTree,
      terminalIds: workspace.terminals.map((terminal) => terminal.id),
      activePaneId,
    });

    useImperativeHandle(ref, () => ({
      fitPane: binder.fitPane,
      fitActivePane: binder.fitActivePane,
      focusPane: binder.focusPaneWithRetry,
      focusActivePane: binder.focusPaneWithRetry.bind(null, activePaneId),
      typePaneTextViaUI: binder.typeTextViaPaneInput,
      isPaneInputFocused: binder.isPaneInputFocused,
      getPaneText: binder.getPaneText,
      getPaneSize: binder.getPaneSize,
    }), [activePaneId, binder]);

    useEffect(() => {
      if (!maximizedPaneId) {
        return;
      }
      if (!paneIds.includes(maximizedPaneId)) {
        setMaximizedPaneId(null);
      }
    }, [maximizedPaneId, paneIds]);

    const focusActivePane = useCallback(() => {
      binder.focusPaneWithRetry(activePaneId, 40);
    }, [activePaneId, binder]);

    useEffect(() => {
      if (!isActiveSession || !enabled) {
        return;
      }
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId: activePaneId,
        message: 'focus active pane effect',
        details: {
          enabled,
          isActiveSession,
          focusRequestToken,
          ...activeElementSummary(),
        },
      });
      focusActivePane();
    }, [activePaneId, enabled, focusActivePane, focusRequestToken, isActiveSession, sessionId]);

    useEffect(() => {
      if (!isActiveSession || !enabled) {
        return;
      }
      // Closing a split can leave the surviving main terminal visually stale until
      // a later resize. Re-fit after the topology change commits to flush xterm's renderer.
      const fitSoon = window.setTimeout(() => {
        binder.fitPane(activePaneId);
      }, 0);
      const fitAfterLayoutSettles = window.setTimeout(() => {
        binder.fitPane(activePaneId);
      }, 75);
      return () => {
        window.clearTimeout(fitSoon);
        window.clearTimeout(fitAfterLayoutSettles);
      };
    }, [activePaneId, binder, enabled, isActiveSession, workspaceTopologyKey]);

    const handleSplit = useCallback((direction: TerminalSplitDirection) => {
      onSplitPane(activePaneId, direction);
    }, [activePaneId, onSplitPane]);

    const handleNewTerminal = useCallback(() => {
      onSplitPane(activePaneId, 'vertical');
    }, [activePaneId, onSplitPane]);

    const handleCloseTerminal = useCallback((paneId: string) => {
      onClosePane(paneId);
    }, [onClosePane]);

    const handleCloseActiveTerminal = useCallback(() => {
      if (activePaneId === MAIN_TERMINAL_PANE_ID) {
        return;
      }
      handleCloseTerminal(activePaneId);
    }, [activePaneId, handleCloseTerminal]);

    const toggleMaximizeActivePane = useCallback(() => {
      setMaximizedPaneId((current) => (current ? null : activePaneId));
    }, [activePaneId]);

    const handleMovePane = useCallback((direction: TerminalNavigationDirection) => {
      const visibleLayout: TerminalLayoutNode = effectivePaneId
        ? { type: 'pane', paneId: effectivePaneId }
        : workspace.layoutTree;
      const nextPaneId = findPaneInDirection(visibleLayout, activePaneId, direction);
      if (nextPaneId) {
        recordPaneRuntimeDebugEvent({
          scope: 'workspace',
          sessionId,
          paneId: activePaneId,
          message: 'navigate to pane',
          details: { direction, nextPaneId },
        });
        onFocusPane(nextPaneId);
        binder.focusPaneWithRetry(nextPaneId);
        return;
      }
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId: activePaneId,
        message: 'navigate out of session',
        details: { direction },
      });
      onNavigateOutOfSession(direction);
    }, [activePaneId, binder, effectivePaneId, onFocusPane, onNavigateOutOfSession, sessionId, workspace.layoutTree]);

    const handleMainPaneMouseDown = useCallback(() => {
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId: MAIN_TERMINAL_PANE_ID,
        message: 'main pane mouse down',
        details: activeElementSummary(),
      });
      onFocusPane(MAIN_TERMINAL_PANE_ID);
      binder.focusPaneWithRetry(MAIN_TERMINAL_PANE_ID);
    }, [binder, onFocusPane, sessionId]);

    const handleUtilityPaneMouseDown = useCallback((paneId: string) => {
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId,
        message: 'utility pane mouse down',
        details: activeElementSummary(),
      });
      onFocusPane(paneId);
      binder.focusPaneWithRetry(paneId);
    }, [binder, onFocusPane, sessionId]);

    const focusPaneIfCurrentlyActive = useCallback((paneId: string, phase: 'init' | 'ready') => {
      if (!isActiveSessionRef.current || activePaneIdRef.current !== paneId) {
        return;
      }
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId,
        message: `focus pane from terminal ${phase}`,
        details: activeElementSummary(),
      });
      binder.focusPaneWithRetry(paneId);
    }, [binder, sessionId]);

    const handleTerminalInit = useCallback((paneId: string) => (xterm: XTerm) => {
      binder.handleTerminalInit(paneId)(xterm);
      focusPaneIfCurrentlyActive(paneId, 'init');
    }, [binder, focusPaneIfCurrentlyActive]);

    const handleTerminalReady = useCallback((paneId: string) => (xterm: XTerm) => {
      binder.handleTerminalReady(paneId)(xterm);
      focusPaneIfCurrentlyActive(paneId, 'ready');
    }, [binder, focusPaneIfCurrentlyActive]);

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
            className={`workspace-pane main-pane ${activePaneId === MAIN_TERMINAL_PANE_ID ? 'active' : ''}`}
            onMouseDown={handleMainPaneMouseDown}
            data-pane-session-id={sessionId}
            data-pane-id={MAIN_TERMINAL_PANE_ID}
            data-pane-kind="main"
          >
            {showMainHeader && (
              <div className="workspace-pane-header">
                <span className="workspace-pane-title">Session</span>
              </div>
            )}
            <div className="workspace-pane-body">
              <Terminal
                ref={(handle) => binder.setTerminalHandle(MAIN_TERMINAL_PANE_ID, handle)}
                fontSize={fontSize}
                resolvedTheme={resolvedTheme}
                debugName={`main:${sessionLabel}:${sessionAgent}:${sessionId}`}
                onInit={handleTerminalInit(MAIN_TERMINAL_PANE_ID)}
                onReady={handleTerminalReady(MAIN_TERMINAL_PANE_ID)}
                onResize={binder.handleTerminalResize(MAIN_TERMINAL_PANE_ID)}
              />
            </div>
          </div>
        );
      }

      const terminal = workspace.terminals.find((entry) => entry.id === node.paneId);
      if (!terminal) {
        return null;
      }
      return (
        <div
          key={terminal.id}
          className={`workspace-pane utility-pane ${activePaneId === terminal.id ? 'active' : ''}`}
          onMouseDown={() => handleUtilityPaneMouseDown(terminal.id)}
          data-pane-session-id={sessionId}
          data-pane-id={terminal.id}
          data-pane-kind="shell"
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
              ref={(handle) => binder.setTerminalHandle(terminal.id, handle)}
              fontSize={fontSize}
              resolvedTheme={resolvedTheme}
              debugName={`utility:${sessionId}:${terminal.title}:${terminal.id}`}
              onInit={handleTerminalInit(terminal.id)}
              onReady={handleTerminalReady(terminal.id)}
              onResize={binder.handleTerminalResize(terminal.id)}
            />
          </div>
        </div>
      );
    }, [
      activePaneId,
      binder,
      fontSize,
      handleCloseTerminal,
      handleMainPaneMouseDown,
      handleTerminalInit,
      handleTerminalReady,
      handleUtilityPaneMouseDown,
      workspace.terminals,
      resolvedTheme,
      sessionAgent,
      sessionId,
      sessionLabel,
      showMainHeader,
    ]);

    const focusModeTitle = useMemo(() => {
      if (!effectivePaneId) {
        return '';
      }
      if (effectivePaneId === MAIN_TERMINAL_PANE_ID) {
        return 'Session';
      }
      return workspace.terminals.find((terminal) => terminal.id === effectivePaneId)?.title || 'Pane';
    }, [effectivePaneId, workspace.terminals]);

    return (
      <div
        className={`session-terminal-workspace ${effectivePaneId ? 'focus-mode' : ''}`}
        data-session-terminal-workspace={sessionId}
      >
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
          {renderPane(effectivePaneId ? { type: 'pane', paneId: effectivePaneId } : workspace.layoutTree)}
        </div>
      </div>
    );
  }
);
