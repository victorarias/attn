import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { GhosttyTerminal, type GhosttyTerminalHandle } from '../GhosttyTerminal';
import { useShortcut } from '../../shortcuts';
import {
  getNormalizedPaneBounds,
  hasPane,
  findPaneInDirection,
  type TerminalNavigationDirection,
  type TerminalLayoutNode,
  type NormalizedPaneBounds,
  type TerminalSplitDirection,
  type TerminalWorkspaceState,
} from '../../types/workspace';
import type { SessionAgent } from '../../types/sessionAgent';
import type { PtySpawnArgs } from '../../pty/bridge';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import { recordTerminalRuntimeLog } from '../../utils/terminalRuntimeLog';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import './SessionTerminalWorkspace.css';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';
import type { ResolvedTheme } from '../../utils/terminalSizing';
import { WorkspaceLayoutRenderer } from './WorkspaceLayoutRenderer';

const ZOOM_PATH_RATIO = 0.76;

function zoomLayoutTowardPane(node: TerminalLayoutNode, paneId: string): TerminalLayoutNode {
  if (node.type === 'pane') {
    return node;
  }

  const firstContainsPane = hasPane(node.children[0], paneId);
  const secondContainsPane = hasPane(node.children[1], paneId);
  const nextChildren: [TerminalLayoutNode, TerminalLayoutNode] = [
    zoomLayoutTowardPane(node.children[0], paneId),
    zoomLayoutTowardPane(node.children[1], paneId),
  ];

  if (!firstContainsPane && !secondContainsPane) {
    return {
      ...node,
      children: nextChildren,
    };
  }

  return {
    ...node,
    ratio: firstContainsPane ? ZOOM_PATH_RATIO : 1 - ZOOM_PATH_RATIO,
    children: nextChildren,
  };
}

export interface SessionTerminalWorkspaceHandle {
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  focusPane: (paneId: string, retries?: number) => void;
  focusActivePane: (retries?: number) => void;
  typePaneTextViaUI: (paneId: string, text: string) => boolean;
  isPaneInputFocused: (paneId: string) => boolean;
  scrollPaneToTop: (paneId: string) => boolean;
  getPaneText: (paneId: string) => string;
  getPaneSize: (paneId: string) => { cols: number; rows: number } | null;
  getPaneVisibleContent: (paneId: string) => TerminalVisibleContentSnapshot;
  getPaneVisibleStyleSummary: (paneId: string) => TerminalVisibleStyleSnapshot;
  resetPaneTerminal: (paneId: string) => boolean;
  injectPaneBytes: (paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectPaneBase64: (paneId: string, payload: string) => Promise<boolean>;
  drainPaneTerminal: (paneId: string) => Promise<boolean>;
}

interface SessionTerminalWorkspaceProps {
  sessionId: string;
  sessionLabel: string;
  sessionAgent: SessionAgent;
  sessionEndpointId?: string;
  workspaceSessions?: Array<{
    id: string;
    label: string;
    agent: SessionAgent;
    cwd: string;
    endpointId?: string;
  }>;
  cwd: string;
  workspace: TerminalWorkspaceState;
  activePaneId: string;
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
  isActiveSession: boolean;
  isSessionViewVisible?: boolean;
  eventRouter: PaneRuntimeEventRouter;
  getSessionPaneSpawnArgs: (sessionId: string, cols: number, rows: number) => PtySpawnArgs | null;
  getAgentPaneSpawnArgs?: (sessionId: string, cols: number, rows: number) => PtySpawnArgs | null;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onZoomModeChange?: (zoomed: boolean) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
}

export const SessionTerminalWorkspace = forwardRef<SessionTerminalWorkspaceHandle, SessionTerminalWorkspaceProps>(
  function SessionTerminalWorkspace({
    sessionId,
    sessionLabel,
    sessionAgent,
    sessionEndpointId,
    workspaceSessions = [],
    cwd,
    workspace,
    activePaneId,
    fontSize,
    resolvedTheme,
    focusRequestToken,
    enabled,
    isActiveSession,
    isSessionViewVisible = true,
    eventRouter,
    getSessionPaneSpawnArgs,
    getAgentPaneSpawnArgs,
    onSplitPane,
    onClosePane,
    onFocusPane,
    onZoomModeChange,
    onNavigateOutOfSession,
  }, ref) {
    const [maximizedPaneId, setMaximizedPaneId] = useState<string | null>(null);
    const [zoomedPaneId, setZoomedPaneId] = useState<string | null>(null);
    const activePaneIdRef = useRef(activePaneId);
    const isActiveSessionRef = useRef(isActiveSession);
    const sessionViewVisibleRef = useRef(isSessionViewVisible);

    activePaneIdRef.current = activePaneId;
    isActiveSessionRef.current = isActiveSession;
    sessionViewVisibleRef.current = isSessionViewVisible;

    const paneIds = useMemo(() => {
      const ids: string[] = [];
      if (!workspace.layoutTree) {
        return ids;
      }
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

    const sessionById = useMemo(() => new Map([
      [sessionId, { id: sessionId, label: sessionLabel, agent: sessionAgent, cwd, endpointId: sessionEndpointId }],
      ...workspaceSessions.map((entry) => [entry.id, entry] as const),
    ]), [cwd, sessionAgent, sessionEndpointId, sessionId, sessionLabel, workspaceSessions]);

    const agentPanes = useMemo(() => workspace.agents, [workspace.agents]);

    const agentPaneById = useMemo(
      () => new Map(agentPanes.map((pane) => [pane.id, pane])),
      [agentPanes],
    );

    const runtimePanes = useMemo(() => ([
      ...agentPanes.filter((pane) => !pane.status || pane.status === 'ready').map((pane) => {
        const paneSession = sessionById.get(pane.sessionId);
        return {
          paneId: pane.id,
          runtimeId: pane.runtimeId,
          paneKind: 'agent' as const,
          agent: paneSession?.agent ?? sessionAgent,
          sessionId: pane.sessionId,
          testSessionId: pane.sessionId,
          getSpawnArgs: ({ cols, rows }: { cols: number; rows: number }) => {
            if (pane.sessionId === sessionId) {
              return getSessionPaneSpawnArgs(pane.sessionId, cols, rows);
            }
            return getAgentPaneSpawnArgs?.(pane.sessionId, cols, rows) ?? null;
          },
        };
      }),
    ]), [agentPanes, getAgentPaneSpawnArgs, getSessionPaneSpawnArgs, sessionAgent, sessionById, sessionId]);

    const runtime = useGhosttyPaneRuntime(runtimePanes, activePaneId, eventRouter, isActiveSessionRef);
    const fitPane = runtime.fitPane;
    const getPaneSize = runtime.getPaneSize;
    const activeRuntimeId = useMemo(
      () => runtimePanes.find((pane) => pane.paneId === activePaneId)?.runtimeId,
      [activePaneId, runtimePanes],
    );
    const splitLayoutActive = workspace.layoutTree?.type === 'split';
    const showMainHeader = paneIds.length > 1;
    const effectivePaneId = maximizedPaneId && paneIds.includes(maximizedPaneId) ? maximizedPaneId : null;
    const effectiveZoomedPaneId = zoomedPaneId && paneIds.includes(zoomedPaneId) ? zoomedPaneId : null;
    const renderedLayoutTree = useMemo(() => {
      if (!workspace.layoutTree) {
        return null;
      }
      if (effectivePaneId) {
        return { type: 'pane', paneId: effectivePaneId } satisfies TerminalLayoutNode;
      }
      if (!effectiveZoomedPaneId) {
        return workspace.layoutTree;
      }
      return zoomLayoutTowardPane(workspace.layoutTree, effectiveZoomedPaneId);
    }, [effectivePaneId, effectiveZoomedPaneId, workspace.layoutTree]);
    const workspaceTopologyKey = JSON.stringify({
      layoutTree: renderedLayoutTree,
      activePaneId,
      effectivePaneId,
      effectiveZoomedPaneId,
    });

    // Track each pane's tree path so we can detect which panes moved after a topology change.
    const panePaths = useMemo(() => {
      const paths = new Map<string, string>();
      if (!renderedLayoutTree) {
        return paths;
      }
      const walk = (node: TerminalLayoutNode, path: string) => {
        if (node.type === 'pane') {
          paths.set(node.paneId, path);
          return;
        }
        walk(node.children[0], path + '/0');
        walk(node.children[1], path + '/1');
      };
      walk(renderedLayoutTree, 'root');
      return paths;
    }, [renderedLayoutTree]);
    const renderedPaneBounds = useMemo(() => (
      renderedLayoutTree ? getNormalizedPaneBounds(renderedLayoutTree) : new Map<string, NormalizedPaneBounds>()
    ), [renderedLayoutTree]);
    const renderedPaneIds = useMemo(() => Array.from(panePaths.keys()), [panePaths]);
    const renderedPaneIdsKey = renderedPaneIds.join('|');
    const prevPanePathsRef = useRef(panePaths);
    const sessionVisibleRef = useRef(false);
    const sessionVisible = enabled && isActiveSession && isSessionViewVisible;

    useImperativeHandle(ref, () => ({
      fitPane: runtime.fitPane,
      fitActivePane: runtime.fitActivePane,
      focusPane: runtime.focusPane,
      focusActivePane: runtime.focusPane.bind(null, activePaneId),
      typePaneTextViaUI: runtime.typeTextViaPaneInput,
      isPaneInputFocused: runtime.isPaneInputFocused,
      scrollPaneToTop: runtime.scrollPaneToTop,
      getPaneText: runtime.getPaneText,
      getPaneSize: runtime.getPaneSize,
      getPaneVisibleContent: runtime.getPaneVisibleContent,
      getPaneVisibleStyleSummary: runtime.getPaneVisibleStyleSummary,
      resetPaneTerminal: runtime.resetPaneTerminal,
      injectPaneBytes: runtime.injectPaneBytes,
      injectPaneBase64: runtime.injectPaneBase64,
      drainPaneTerminal: runtime.drainPaneTerminal,
    }), [activePaneId, runtime]);

    useEffect(() => {
      if (!maximizedPaneId) {
        return;
      }
      if (!paneIds.includes(maximizedPaneId)) {
        setMaximizedPaneId(null);
      }
    }, [maximizedPaneId, paneIds]);

    useEffect(() => {
      if (!zoomedPaneId) {
        return;
      }
      if (!paneIds.includes(zoomedPaneId)) {
        setZoomedPaneId(null);
      }
    }, [paneIds, zoomedPaneId]);

    useEffect(() => {
      if (!effectiveZoomedPaneId || effectivePaneId) {
        return;
      }
      if (effectiveZoomedPaneId !== activePaneId) {
        setZoomedPaneId(activePaneId);
      }
    }, [activePaneId, effectivePaneId, effectiveZoomedPaneId]);

    useEffect(() => {
      onZoomModeChange?.(Boolean(effectiveZoomedPaneId));
    }, [effectiveZoomedPaneId, onZoomModeChange]);

    useEffect(() => {
      recordTerminalRuntimeLog({
        category: 'workspace',
        sessionId,
        paneId: activePaneId,
        runtimeId: activeRuntimeId,
        message: 'workspace active pane state changed',
        details: {
          activePaneId,
          activeRuntimeId: activeRuntimeId ?? null,
          isActiveSession,
          enabled,
          paneCount: paneIds.length,
          paneIds,
        },
      });
    }, [activePaneId, activeRuntimeId, enabled, isActiveSession, paneIds, sessionId]);

    const focusActivePane = useCallback(() => {
      // Single attempt — terminal is already mounted in every case this fires.
      runtime.focusPane(activePaneId, 0);
    }, [activePaneId, runtime]);

    useEffect(() => {
      if (!sessionVisible) {
        return;
      }
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId: activePaneId,
        message: 'focus active pane effect',
        details: () => ({
          enabled,
          isActiveSession,
          isSessionViewVisible,
          focusRequestToken,
          ...activeElementSummary(),
        }),
      });
      focusActivePane();
    }, [activePaneId, focusActivePane, focusRequestToken, isActiveSession, isSessionViewVisible, sessionId, sessionVisible]);

    // After relaunch, first-show, or split topology changes, the terminal can briefly keep
    // stale narrow geometry from the previous layout. Re-fitting immediately and then
    // once more after layout settles preserves restored headers/content width.
    const refitPanesNowAndIfStillTiny = useCallback((targetPaneIds: string[]) => {
      const paneIdsToFit = Array.from(new Set(targetPaneIds));
      if (paneIdsToFit.length === 0) {
        return undefined;
      }

      for (const paneId of paneIdsToFit) {
        fitPane(paneId);
      }

      const lateRefitTimeout = window.setTimeout(() => {
        for (const paneId of paneIdsToFit) {
          const size = getPaneSize(paneId);
          if (size && isSuspiciousTerminalSize(size.cols, size.rows)) {
            fitPane(paneId);
          }
        }
      }, 75);

      return () => {
        window.clearTimeout(lateRefitTimeout);
      };
    }, [fitPane, getPaneSize]);

    useLayoutEffect(() => {
      if (!sessionVisible) {
        sessionVisibleRef.current = false;
        return;
      }
      if (sessionVisibleRef.current) {
        return;
      }
      sessionVisibleRef.current = true;
      return refitPanesNowAndIfStillTiny(renderedPaneIds);
    }, [refitPanesNowAndIfStillTiny, renderedPaneIds, renderedPaneIdsKey, sessionVisible]);

    useLayoutEffect(() => {
      if (!sessionVisible) {
        prevPanePathsRef.current = panePaths;
        return;
      }
      // Find panes whose tree position changed — only those need re-fitting
      // after a topology change (e.g. closing a split sibling).
      const prev = prevPanePathsRef.current;
      prevPanePathsRef.current = panePaths;
      const movedPanes: string[] = [];
      for (const [paneId, path] of panePaths) {
        if (prev.get(paneId) !== path) {
          movedPanes.push(paneId);
        }
      }
      if (movedPanes.length === 0) {
        return;
      }
      return refitPanesNowAndIfStillTiny(movedPanes);
    }, [panePaths, refitPanesNowAndIfStillTiny, sessionVisible, workspaceTopologyKey]);

    const handleSplit = useCallback((direction: TerminalSplitDirection) => {
      onSplitPane(activePaneId, direction);
    }, [activePaneId, onSplitPane]);

    const handleNewTerminal = useCallback(() => {
      onSplitPane(activePaneId, 'vertical');
    }, [activePaneId, onSplitPane]);

    const handleClosePane = useCallback((paneId: string) => {
      onClosePane(paneId);
    }, [onClosePane]);

    const handleCloseActivePane = useCallback(() => {
      handleClosePane(activePaneId);
    }, [activePaneId, handleClosePane]);

    const toggleMaximizeActivePane = useCallback(() => {
      setZoomedPaneId(null);
      setMaximizedPaneId((current) => (current ? null : activePaneId));
    }, [activePaneId]);

    const toggleZoomActivePane = useCallback(() => {
      setMaximizedPaneId(null);
      setZoomedPaneId((current) => (current === activePaneId ? null : activePaneId));
    }, [activePaneId]);

    const handleMovePane = useCallback((direction: TerminalNavigationDirection) => {
      const visibleLayout: TerminalLayoutNode | null = effectivePaneId
        ? { type: 'pane', paneId: effectivePaneId }
        : renderedLayoutTree;
      if (!visibleLayout) {
        return;
      }
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
        runtime.focusPane(nextPaneId);
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
    }, [activePaneId, effectivePaneId, onFocusPane, onNavigateOutOfSession, renderedLayoutTree, runtime, sessionId]);

    const handleAgentPaneMouseDown = useCallback((paneId: string) => {
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId,
        message: 'agent pane mouse down',
        details: activeElementSummary,
      });
      onFocusPane(paneId);
      runtime.focusPane(paneId);
    }, [onFocusPane, runtime, sessionId]);

    const focusPaneIfCurrentlyActive = useCallback((paneId: string, phase: 'init' | 'ready') => {
      if (!isActiveSessionRef.current || !sessionViewVisibleRef.current || activePaneIdRef.current !== paneId) {
        return;
      }
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId,
        message: `focus pane from terminal ${phase}`,
        details: activeElementSummary,
      });
      runtime.focusPane(paneId);
    }, [runtime, sessionId]);

    const handleGhosttyTerminalReady = useCallback((paneId: string) => (terminal: GhosttyTerminalHandle) => {
      void runtime.handleTerminalReady(paneId)(terminal);
      focusPaneIfCurrentlyActive(paneId, 'ready');
    }, [focusPaneIfCurrentlyActive, runtime]);

    useShortcut('terminal.open', focusActivePane, sessionVisible);
    useShortcut('terminal.new', () => { void handleNewTerminal(); }, sessionVisible);
    useShortcut('terminal.splitVertical', () => { handleSplit('vertical'); }, sessionVisible);
    useShortcut('terminal.splitHorizontal', () => { handleSplit('horizontal'); }, sessionVisible);
    useShortcut('terminal.toggleZoom', toggleZoomActivePane, sessionVisible);
    useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, sessionVisible);
    useShortcut('terminal.close', handleCloseActivePane, sessionVisible && splitLayoutActive);
    useShortcut('terminal.focusLeft', () => handleMovePane('left'), sessionVisible);
    useShortcut('terminal.focusRight', () => handleMovePane('right'), sessionVisible);
    useShortcut('terminal.focusUp', () => handleMovePane('up'), sessionVisible);
    useShortcut('terminal.focusDown', () => handleMovePane('down'), sessionVisible);

    const paneFrameStyle = useCallback((bounds: NormalizedPaneBounds) => ({
      left: `${bounds.left * 100}%`,
      top: `${bounds.top * 100}%`,
      width: `${bounds.width * 100}%`,
      height: `${bounds.height * 100}%`,
    }), []);

    const renderPaneSurface = useCallback((paneId: string): React.ReactNode => {
      const path = panePaths.get(paneId) || 'root';
      const bounds = renderedPaneBounds.get(paneId);
      if (!bounds) {
        return null;
      }
      const frameStyle = paneFrameStyle(bounds);

      const agentPane = agentPaneById.get(paneId);
      if (agentPane) {
        const paneSession = sessionById.get(agentPane.sessionId);
        const paneTitle = paneSession?.label || agentPane.title || 'Session';
        const paneStatus = agentPane.status || 'ready';
        const isPaneStarting = paneStatus === 'spawning';
        const isPaneFailed = paneStatus === 'failed';
        return (
          <div
            key={agentPane.id}
            className={`workspace-pane main-pane ${activePaneId === agentPane.id ? 'active' : ''}`}
            onMouseDown={() => handleAgentPaneMouseDown(agentPane.id)}
            data-pane-session-id={agentPane.sessionId}
            data-pane-id={agentPane.id}
            data-pane-kind="agent"
            data-pane-path={path}
            style={frameStyle}
          >
            <div
              className={`workspace-pane-header ${showMainHeader ? '' : 'workspace-pane-header-hidden'}`.trim()}
              aria-hidden={showMainHeader ? undefined : true}
            >
              {showMainHeader ? <span className="workspace-pane-title">{paneTitle}</span> : null}
            </div>
            <div className="workspace-pane-body">
              {isPaneStarting || isPaneFailed ? (
                <div className={`workspace-pane-status workspace-pane-status--${paneStatus}`}>
                  <span className="workspace-pane-status-spinner" aria-hidden="true" />
                  <span>{isPaneFailed ? (agentPane.error || 'Session failed to start') : `Starting ${paneTitle}...`}</span>
                </div>
              ) : (
                <GhosttyTerminal
                  ref={(handle) => runtime.setTerminalHandle(agentPane.id, handle)}
                  fontSize={fontSize}
                  resolvedTheme={resolvedTheme}
                  debugName={`agent:${paneTitle}:${paneSession?.agent ?? sessionAgent}:${agentPane.sessionId}`}
                  runtimeLogMeta={{ sessionId: agentPane.sessionId, paneId: agentPane.id, runtimeId: agentPane.runtimeId, paneKind: 'agent', isActivePane: activePaneId === agentPane.id, isActiveSession, paneCount: paneIds.length }}
                  onInput={runtime.handleTerminalInput(agentPane.id)}
                  onReady={handleGhosttyTerminalReady(agentPane.id)}
                  onResize={runtime.handleTerminalResize(agentPane.id)}
                />
              )}
            </div>
          </div>
        );
      }

      return null;
    }, [
      activePaneId,
      fontSize,
      handleAgentPaneMouseDown,
      handleGhosttyTerminalReady,
      paneFrameStyle,
      panePaths,
      renderedPaneBounds,
      agentPaneById,
      sessionById,
      resolvedTheme,
      runtime,
      sessionAgent,
      sessionId,
      sessionLabel,
      showMainHeader,
    ]);

    const focusModeTitle = useMemo(() => {
      if (!effectivePaneId) {
        return '';
      }
      const agentPane = agentPaneById.get(effectivePaneId);
      if (agentPane) {
        return sessionById.get(agentPane.sessionId)?.label || agentPane.title || 'Session';
      }
      return 'Pane';
    }, [agentPaneById, effectivePaneId, sessionById]);

    if (!renderedLayoutTree) {
      return (
        <div
          className="session-terminal-workspace"
          data-session-terminal-workspace={sessionId}
          data-active-pane-id=""
          data-maximized-pane-id=""
          data-session-visible={sessionVisible ? '1' : '0'}
          data-zoomed-pane-id=""
        />
      );
    }

    return (
      <div
        className={`session-terminal-workspace ${effectivePaneId ? 'focus-mode' : ''} ${effectiveZoomedPaneId && !effectivePaneId ? 'zoom-mode' : ''}`.trim()}
        data-session-terminal-workspace={sessionId}
        data-active-pane-id={activePaneId}
        data-maximized-pane-id={effectivePaneId || ''}
        data-session-visible={sessionVisible ? '1' : '0'}
        data-zoomed-pane-id={effectiveZoomedPaneId || ''}
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
        <WorkspaceLayoutRenderer
          layoutTree={renderedLayoutTree}
          paneIds={renderedPaneIds}
          renderPane={renderPaneSurface}
        />
      </div>
    );
  }
);
