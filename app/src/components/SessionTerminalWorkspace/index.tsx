import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal, type ResolvedTheme, type TerminalHandle } from '../Terminal';
import { useShortcut } from '../../shortcuts';
import {
  MAIN_TERMINAL_PANE_ID,
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
import { usePaneRuntimeBinder } from './usePaneRuntimeBinder';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import { recordTerminalRuntimeLog } from '../../utils/terminalRuntimeLog';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import './SessionTerminalWorkspace.css';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';

const ZOOM_PATH_RATIO = 0.76;

function clampSplitRatio(ratio: number): number {
  if (ratio > 0 && ratio < 1) {
    return ratio;
  }
  return 0.5;
}

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
  getMainPaneSpawnArgs: (cols: number, rows: number) => PtySpawnArgs | null;
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
    getMainPaneSpawnArgs,
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
        sessionId,
        testSessionId: sessionId,
        getSpawnArgs: ({ cols, rows }: { cols: number; rows: number }) => getMainPaneSpawnArgs(cols, rows),
      },
      ...workspace.terminals.map((terminal) => ({
        paneId: terminal.id,
        runtimeId: terminal.ptyId,
        sessionId,
        getSpawnArgs: ({ cols, rows }: { cols: number; rows: number }) => ({
          id: terminal.ptyId,
          cwd,
          ...(sessionEndpointId ? { endpoint_id: sessionEndpointId } : {}),
          cols,
          rows,
          shell: true,
        }),
      })),
    ]), [cwd, getMainPaneSpawnArgs, sessionEndpointId, sessionId, workspace.terminals]);

    const binder = usePaneRuntimeBinder(runtimePanes, activePaneId, eventRouter);
    const fitPane = binder.fitPane;
    const getPaneSize = binder.getPaneSize;
    const setTerminalHandle = binder.setTerminalHandle;
    const terminalHandleRefCallbacksRef = useRef(new Map<string, (handle: TerminalHandle | null) => void>());
    const activeRuntimeId = useMemo(
      () => runtimePanes.find((pane) => pane.paneId === activePaneId)?.runtimeId,
      [activePaneId, runtimePanes],
    );
    const splitLayoutActive = workspace.layoutTree.type === 'split';
    const showMainHeader = paneIds.length > 1;
    const effectivePaneId = maximizedPaneId && paneIds.includes(maximizedPaneId) ? maximizedPaneId : null;
    const effectiveZoomedPaneId = zoomedPaneId && paneIds.includes(zoomedPaneId) ? zoomedPaneId : null;
    const renderedLayoutTree = useMemo(() => {
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
      terminalIds: workspace.terminals.map((terminal) => terminal.id),
      activePaneId,
      effectivePaneId,
      effectiveZoomedPaneId,
    });

    // Track each pane's tree path so we can detect which panes moved after a topology change.
    const panePaths = useMemo(() => {
      const paths = new Map<string, string>();
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
    const renderedPaneBounds = useMemo(() => getNormalizedPaneBounds(renderedLayoutTree), [renderedLayoutTree]);
    const renderedPaneIds = useMemo(() => Array.from(panePaths.keys()), [panePaths]);
    const renderedPaneIdsKey = renderedPaneIds.join('|');
    const prevPanePathsRef = useRef(panePaths);
    const sessionVisibleRef = useRef(false);
    const sessionVisible = enabled && isActiveSession && isSessionViewVisible;

    useEffect(() => {
      const livePaneIDs = new Set(paneIds);
      for (const paneId of Array.from(terminalHandleRefCallbacksRef.current.keys())) {
        if (!livePaneIDs.has(paneId)) {
          terminalHandleRefCallbacksRef.current.delete(paneId);
        }
      }
    }, [paneIds]);

    const getTerminalHandleRef = useCallback((paneId: string) => {
      let callback = terminalHandleRefCallbacksRef.current.get(paneId);
      if (!callback) {
        callback = (handle) => {
          setTerminalHandle(paneId, handle);
        };
        terminalHandleRefCallbacksRef.current.set(paneId, callback);
      }
      return callback;
    }, [setTerminalHandle]);

    useImperativeHandle(ref, () => ({
      fitPane: binder.fitPane,
      fitActivePane: binder.fitActivePane,
      focusPane: binder.focusPaneWithRetry,
      focusActivePane: binder.focusPaneWithRetry.bind(null, activePaneId),
      typePaneTextViaUI: binder.typeTextViaPaneInput,
      isPaneInputFocused: binder.isPaneInputFocused,
      scrollPaneToTop: binder.scrollPaneToTop,
      getPaneText: binder.getPaneText,
      getPaneSize: binder.getPaneSize,
      getPaneVisibleContent: binder.getPaneVisibleContent,
      getPaneVisibleStyleSummary: binder.getPaneVisibleStyleSummary,
      resetPaneTerminal: binder.resetPaneTerminal,
      injectPaneBytes: binder.injectPaneBytes,
      injectPaneBase64: binder.injectPaneBase64,
      drainPaneTerminal: binder.drainPaneTerminal,
    }), [activePaneId, binder]);

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
      binder.focusPaneWithRetry(activePaneId, 40);
    }, [activePaneId, binder]);

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
      setZoomedPaneId(null);
      setMaximizedPaneId((current) => (current ? null : activePaneId));
    }, [activePaneId]);

    const toggleZoomActivePane = useCallback(() => {
      setMaximizedPaneId(null);
      setZoomedPaneId((current) => (current === activePaneId ? null : activePaneId));
    }, [activePaneId]);

    const handleMovePane = useCallback((direction: TerminalNavigationDirection) => {
      const visibleLayout: TerminalLayoutNode = effectivePaneId
        ? { type: 'pane', paneId: effectivePaneId }
        : renderedLayoutTree;
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
    }, [activePaneId, binder, effectivePaneId, onFocusPane, onNavigateOutOfSession, renderedLayoutTree, sessionId]);

    const handleMainPaneMouseDown = useCallback(() => {
      recordPaneRuntimeDebugEvent({
        scope: 'workspace',
        sessionId,
        paneId: MAIN_TERMINAL_PANE_ID,
        message: 'main pane mouse down',
        details: activeElementSummary,
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
        details: activeElementSummary,
      });
      onFocusPane(paneId);
      binder.focusPaneWithRetry(paneId);
    }, [binder, onFocusPane, sessionId]);

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

    useShortcut('terminal.open', focusActivePane, sessionVisible);
    useShortcut('terminal.new', () => { void handleNewTerminal(); }, sessionVisible);
    useShortcut('terminal.splitVertical', () => { handleSplit('vertical'); }, sessionVisible);
    useShortcut('terminal.splitHorizontal', () => { handleSplit('horizontal'); }, sessionVisible);
    useShortcut('terminal.toggleZoom', toggleZoomActivePane, sessionVisible);
    useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, sessionVisible);
    useShortcut('terminal.close', handleCloseActiveTerminal, sessionVisible && splitLayoutActive && activePaneId !== MAIN_TERMINAL_PANE_ID);
    useShortcut('terminal.focusLeft', () => handleMovePane('left'), sessionVisible);
    useShortcut('terminal.focusRight', () => handleMovePane('right'), sessionVisible);
    useShortcut('terminal.focusUp', () => handleMovePane('up'), sessionVisible);
    useShortcut('terminal.focusDown', () => handleMovePane('down'), sessionVisible);

    const renderLayoutMetadata = useCallback((node: TerminalLayoutNode, path = 'root'): React.ReactNode => {
      if (node.type === 'split') {
        const firstRatio = clampSplitRatio(node.ratio);
        const secondRatio = clampSplitRatio(1 - firstRatio);
        const firstPath = `${path}/0`;
        const secondPath = `${path}/1`;
        const splitStyle = node.direction === 'vertical'
          ? { gridTemplateColumns: `minmax(0, ${firstRatio}fr) minmax(0, ${secondRatio}fr)` }
          : { gridTemplateRows: `minmax(0, ${firstRatio}fr) minmax(0, ${secondRatio}fr)` };
        return (
          <div
            key={node.splitId}
            className={`workspace-split split-${node.direction}`}
            data-split-id={node.splitId}
            data-split-path={path}
            data-split-direction={node.direction}
            data-split-ratio={firstRatio.toFixed(3)}
            style={splitStyle}
          >
            <div
              className="workspace-split-child"
              data-split-child-index="0"
              data-split-child-path={firstPath}
            >
              {renderLayoutMetadata(node.children[0], firstPath)}
            </div>
            <div
              className="workspace-split-child"
              data-split-child-index="1"
              data-split-child-path={secondPath}
            >
              {renderLayoutMetadata(node.children[1], secondPath)}
            </div>
          </div>
        );
      }

      return null;
    }, []);

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

      if (paneId === MAIN_TERMINAL_PANE_ID) {
        return (
          <div
            key={MAIN_TERMINAL_PANE_ID}
            className={`workspace-pane main-pane ${activePaneId === MAIN_TERMINAL_PANE_ID ? 'active' : ''}`}
            onMouseDown={handleMainPaneMouseDown}
            data-pane-session-id={sessionId}
            data-pane-id={MAIN_TERMINAL_PANE_ID}
            data-pane-kind="main"
            data-pane-path={path}
            style={frameStyle}
          >
            <div
              className={`workspace-pane-header ${showMainHeader ? '' : 'workspace-pane-header-hidden'}`.trim()}
              aria-hidden={showMainHeader ? undefined : true}
            >
              {showMainHeader ? <span className="workspace-pane-title">Session</span> : null}
            </div>
            <div className="workspace-pane-body">
              <Terminal
                ref={getTerminalHandleRef(MAIN_TERMINAL_PANE_ID)}
                fontSize={fontSize}
                resolvedTheme={resolvedTheme}
                tuiCursor
                debugName={`main:${sessionLabel}:${sessionAgent}:${sessionId}`}
                runtimeLogMeta={{
                  sessionId,
                  paneId: MAIN_TERMINAL_PANE_ID,
                  runtimeId: sessionId,
                  paneKind: 'main',
                  isActivePane: activePaneId === MAIN_TERMINAL_PANE_ID,
                  isActiveSession,
                }}
                onInit={handleTerminalInit(MAIN_TERMINAL_PANE_ID)}
                onReady={handleTerminalReady(MAIN_TERMINAL_PANE_ID)}
                onResize={binder.handleTerminalResize(MAIN_TERMINAL_PANE_ID)}
              />
            </div>
          </div>
        );
      }

      const terminal = workspace.terminals.find((entry) => entry.id === paneId);
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
          data-pane-path={path}
          style={frameStyle}
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
              ref={getTerminalHandleRef(terminal.id)}
              fontSize={fontSize}
              resolvedTheme={resolvedTheme}
              debugName={`utility:${sessionId}:${terminal.title}:${terminal.id}`}
              runtimeLogMeta={{
                sessionId,
                paneId: terminal.id,
                runtimeId: terminal.ptyId,
                paneKind: 'shell',
                isActivePane: activePaneId === terminal.id,
                isActiveSession,
              }}
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
      getTerminalHandleRef,
      handleCloseTerminal,
      handleMainPaneMouseDown,
      handleTerminalInit,
      handleTerminalReady,
      handleUtilityPaneMouseDown,
      paneFrameStyle,
      panePaths,
      renderedPaneBounds,
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
        <div className="session-terminal-panes">
          <div className="workspace-layout-metadata" aria-hidden="true">
            {renderLayoutMetadata(renderedLayoutTree)}
          </div>
          {renderedPaneIds.map((paneId) => renderPaneSurface(paneId))}
        </div>
      </div>
    );
  }
);
