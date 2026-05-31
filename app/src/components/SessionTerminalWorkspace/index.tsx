import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { GhosttyTerminal, type GhosttyTerminalHandle } from '../GhosttyTerminal';
import { useShortcut } from '../../shortcuts';
import {
  getNormalizedPaneBounds,
  getSplitDividers,
  applyRatioOverrides,
  collectSplitRatios,
  hasPane,
  findPaneInDirection,
  leafSlotId,
  type SplitDivider,
  type TerminalNavigationDirection,
  type TerminalLayoutNode,
  type PanelLeaf,
  type NormalizedPaneBounds,
  type TerminalSplitDirection,
  type TerminalDockEdge,
  type TerminalWorkspaceState,
} from '../../types/workspace';
import type { SessionAgent } from '../../types/sessionAgent';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import { lockTextSelection } from '../../utils/dragLock';
import './SessionTerminalWorkspace.css';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';
import type { ResolvedTheme } from '../../utils/terminalSizing';
import { WorkspaceLayoutRenderer } from './WorkspaceLayoutRenderer';
import { WorkspaceDockPanel } from './WorkspaceDockPanel';

const ZOOM_PATH_RATIO = 0.76;

// Fraction of the target pane the dock-preview band (and the resulting panel)
// occupies on the chosen edge.
const DOCK_BAND_FRACTION = 0.32;

interface DockNormalizedRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

interface DockTarget {
  anchorPaneId: string;
  edge: TerminalDockEdge;
  rect: DockNormalizedRect;
}

// computeDockTarget maps a pointer position to the pane it's over and the edge
// it's nearest, returning the band the docked panel would occupy. Only terminal
// panes are drop targets; a pointer outside every pane returns null (a no-op
// drop that leaves the panel where it is).
function computeDockTarget(
  clientX: number,
  clientY: number,
  containerRect: DOMRect,
  paneRects: Array<{ paneId: string; bounds: NormalizedPaneBounds }>,
): DockTarget | null {
  if (containerRect.width <= 0 || containerRect.height <= 0) {
    return null;
  }
  const nx = (clientX - containerRect.left) / containerRect.width;
  const ny = (clientY - containerRect.top) / containerRect.height;
  for (const { paneId, bounds } of paneRects) {
    if (nx < bounds.left || nx > bounds.right || ny < bounds.top || ny > bounds.bottom) {
      continue;
    }
    const px = (nx - bounds.left) / Math.max(bounds.width, 1e-6);
    const py = (ny - bounds.top) / Math.max(bounds.height, 1e-6);
    const distances: Array<{ edge: TerminalDockEdge; dist: number }> = [
      { edge: 'left', dist: px },
      { edge: 'right', dist: 1 - px },
      { edge: 'top', dist: py },
      { edge: 'bottom', dist: 1 - py },
    ];
    distances.sort((a, b) => a.dist - b.dist);
    const edge = distances[0].edge;
    const bandW = bounds.width * DOCK_BAND_FRACTION;
    const bandH = bounds.height * DOCK_BAND_FRACTION;
    let rect: DockNormalizedRect;
    switch (edge) {
      case 'left':
        rect = { left: bounds.left, top: bounds.top, width: bandW, height: bounds.height };
        break;
      case 'right':
        rect = { left: bounds.right - bandW, top: bounds.top, width: bandW, height: bounds.height };
        break;
      case 'top':
        rect = { left: bounds.left, top: bounds.top, width: bounds.width, height: bandH };
        break;
      default:
        rect = { left: bounds.left, top: bounds.bottom - bandH, width: bounds.width, height: bandH };
        break;
    }
    return { anchorPaneId: paneId, edge, rect };
  }
  return null;
}

function zoomLayoutTowardPane(node: TerminalLayoutNode, paneId: string): TerminalLayoutNode {
  if (node.type !== 'split') {
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
  workspaceId: string;
  workspaceSessions?: Array<{
    id: string;
    label: string;
    agent: SessionAgent;
    cwd: string;
    endpointId?: string;
  }>;
  workspace: TerminalWorkspaceState;
  activePaneId: string;
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
  isActiveSession: boolean;
  isSessionViewVisible?: boolean;
  eventRouter: PaneRuntimeEventRouter;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onZoomModeChange?: (zoomed: boolean) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
  onResizeSplit?: (splitId: string, ratio: number) => void;
  onDockPanel?: (panelId: string, panelKind: string, anchorPaneId: string, edge: TerminalDockEdge) => void;
  onUndockPanel?: (panelId: string) => void;
}

export const SessionTerminalWorkspace = forwardRef<SessionTerminalWorkspaceHandle, SessionTerminalWorkspaceProps>(
  function SessionTerminalWorkspace({
    workspaceId,
    workspaceSessions = [],
    workspace,
    activePaneId,
    fontSize,
    resolvedTheme,
    focusRequestToken,
    enabled,
    isActiveSession,
    isSessionViewVisible = true,
    eventRouter,
    onSplitPane,
    onClosePane,
    onFocusPane,
    onZoomModeChange,
    onNavigateOutOfSession,
    onResizeSplit,
    onDockPanel,
    onUndockPanel,
  }, ref) {
    const [maximizedPaneId, setMaximizedPaneId] = useState<string | null>(null);
    const [zoomedPaneId, setZoomedPaneId] = useState<string | null>(null);
    // Live, optimistic split ratios while dragging a divider. Reconciled against
    // the daemon layout once it echoes the persisted (locked) ratio back.
    const [ratioOverrides, setRatioOverrides] = useState<Map<string, number>>(() => new Map());
    // Drag-to-dock state. The panel stays docked in the daemon tree throughout
    // the drag — this is a transient preview (ghost + target highlight) that
    // resolves to a single dock command on drop.
    const [draggingPanelId, setDraggingPanelId] = useState<string | null>(null);
    const [dockTarget, setDockTarget] = useState<DockTarget | null>(null);
    const [ghostPos, setGhostPos] = useState<{ x: number; y: number } | null>(null);
    const panelDragCleanupRef = useRef<(() => void) | null>(null);
    const draggingSplitRef = useRef<string | null>(null);
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
        if (node.type === 'split') {
          collect(node.children[0]);
          collect(node.children[1]);
          return;
        }
        if (node.type === 'pane') {
          ids.push(node.paneId);
        }
      };
      collect(workspace.layoutTree);
      return ids;
    }, [workspace.layoutTree]);

    const sessionById = useMemo(() => new Map(
      workspaceSessions.map((entry) => [entry.id, entry] as const),
    ), [workspaceSessions]);

    const agentPanes = useMemo(() => workspace.agents, [workspace.agents]);

    const agentPaneById = useMemo(
      () => new Map(agentPanes.map((pane) => [pane.id, pane])),
      [agentPanes],
    );

    // Docked panels keyed by panel id, walked from the authoritative layout tree.
    const panelLeafById = useMemo(() => {
      const map = new Map<string, PanelLeaf>();
      const walk = (node: TerminalLayoutNode | null) => {
        if (!node) {
          return;
        }
        if (node.type === 'split') {
          walk(node.children[0]);
          walk(node.children[1]);
          return;
        }
        if (node.type === 'panel') {
          map.set(node.panelId, node);
        }
      };
      walk(workspace.layoutTree);
      return map;
    }, [workspace.layoutTree]);

    const runtimePanes = useMemo(() => ([
      ...agentPanes.filter((pane) => !pane.status || pane.status === 'ready').map((pane) => {
        const paneSession = sessionById.get(pane.sessionId);
        return {
          paneId: pane.id,
          runtimeId: pane.runtimeId,
          paneKind: 'agent' as const,
          agent: paneSession?.agent ?? 'shell',
          sessionId: pane.sessionId,
          testSessionId: pane.sessionId,
        };
      }),
    ]), [agentPanes, sessionById]);

    const runtime = useGhosttyPaneRuntime(runtimePanes, activePaneId, eventRouter, isActiveSessionRef);
    const fitPane = runtime.fitPane;
    const getPaneSize = runtime.getPaneSize;
    const splitLayoutActive = workspace.layoutTree?.type === 'split';
    const showPaneHeader = paneIds.length > 1;
    const effectivePaneId = maximizedPaneId && paneIds.includes(maximizedPaneId) ? maximizedPaneId : null;
    const effectiveZoomedPaneId = zoomedPaneId && paneIds.includes(zoomedPaneId) ? zoomedPaneId : null;
    const baseLayoutTree = useMemo(() => (
      workspace.layoutTree ? applyRatioOverrides(workspace.layoutTree, ratioOverrides) : null
    ), [workspace.layoutTree, ratioOverrides]);
    const renderedLayoutTree = useMemo(() => {
      if (!baseLayoutTree) {
        return null;
      }
      if (effectivePaneId) {
        return { type: 'pane', paneId: effectivePaneId } satisfies TerminalLayoutNode;
      }
      if (!effectiveZoomedPaneId) {
        return baseLayoutTree;
      }
      return zoomLayoutTowardPane(baseLayoutTree, effectiveZoomedPaneId);
    }, [effectivePaneId, effectiveZoomedPaneId, baseLayoutTree]);

    // Reconcile optimistic overrides with the authoritative daemon layout: drop
    // an override once the daemon's ratio matches it (persistence landed) or the
    // split disappears. Never drop the split currently being dragged.
    useEffect(() => {
      setRatioOverrides((prev) => {
        if (prev.size === 0 || !workspace.layoutTree) {
          return prev;
        }
        const ratios = collectSplitRatios(workspace.layoutTree);
        let changed = false;
        const next = new Map(prev);
        for (const [splitId, overrideRatio] of prev) {
          if (splitId === draggingSplitRef.current) {
            continue;
          }
          const daemonRatio = ratios.get(splitId);
          if (daemonRatio == null || Math.abs(daemonRatio - overrideRatio) < 0.005) {
            next.delete(splitId);
            changed = true;
          }
        }
        return changed ? next : prev;
      });
    }, [workspace.layoutTree]);
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
        if (node.type === 'split') {
          walk(node.children[0], path + '/0');
          walk(node.children[1], path + '/1');
          return;
        }
        // Leaf: terminal panes and docked panels both occupy a slot to render.
        paths.set(leafSlotId(node), path);
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

    const focusActivePane = useCallback(() => {
      // Single attempt — terminal is already mounted in every case this fires.
      runtime.focusPane(activePaneId, 0);
    }, [activePaneId, runtime]);

    useEffect(() => {
      if (!sessionVisible) {
        return;
      }
      focusActivePane();
    }, [activePaneId, focusActivePane, focusRequestToken, isActiveSession, isSessionViewVisible, workspaceId, sessionVisible]);

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
        onFocusPane(nextPaneId);
        runtime.focusPane(nextPaneId);
        return;
      }
      onNavigateOutOfSession(direction);
    }, [activePaneId, effectivePaneId, onFocusPane, onNavigateOutOfSession, renderedLayoutTree, runtime]);

    const handleAgentPaneMouseDown = useCallback((paneId: string) => {
      onFocusPane(paneId);
      runtime.focusPane(paneId);
    }, [onFocusPane, runtime]);

    const focusPaneIfCurrentlyActive = useCallback((paneId: string) => {
      if (!isActiveSessionRef.current || !sessionViewVisibleRef.current || activePaneIdRef.current !== paneId) {
        return;
      }
      runtime.focusPane(paneId);
    }, [runtime]);

    const handleGhosttyTerminalReady = useCallback((paneId: string) => (terminal: GhosttyTerminalHandle) => {
      void runtime.handleTerminalReady(paneId)(terminal);
      focusPaneIfCurrentlyActive(paneId);
    }, [focusPaneIfCurrentlyActive, runtime]);

    useShortcut('terminal.open', focusActivePane, sessionVisible);
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

    // Drag a docked panel's header to relocate it. The daemon tree is untouched
    // until drop, when a single dock command moves it to the previewed target.
    const beginPanelDrag = useCallback((panelId: string, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const container = (event.target as HTMLElement).closest('.session-terminal-panes') as HTMLElement | null;
      if (!container) {
        return;
      }
      const containerRect = container.getBoundingClientRect();
      // Snapshot terminal-pane targets: the layout is static during the drag.
      const paneRects: Array<{ paneId: string; bounds: NormalizedPaneBounds }> = [];
      for (const [slotId, bounds] of renderedPaneBounds) {
        if (agentPaneById.has(slotId)) {
          paneRects.push({ paneId: slotId, bounds });
        }
      }
      const releaseSelectionLock = lockTextSelection('grabbing');
      setDraggingPanelId(panelId);
      setGhostPos({ x: event.clientX, y: event.clientY });

      const onMove = (ev: PointerEvent) => {
        setGhostPos({ x: ev.clientX, y: ev.clientY });
        setDockTarget(computeDockTarget(ev.clientX, ev.clientY, containerRect, paneRects));
      };
      const teardown = () => {
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        releaseSelectionLock();
        setDraggingPanelId(null);
        setDockTarget(null);
        setGhostPos(null);
        panelDragCleanupRef.current = null;
      };
      const onUp = (ev: PointerEvent) => {
        const finalTarget = computeDockTarget(ev.clientX, ev.clientY, containerRect, paneRects);
        teardown();
        if (finalTarget) {
          const panelKind = panelLeafById.get(panelId)?.panelKind ?? '';
          onDockPanel?.(panelId, panelKind, finalTarget.anchorPaneId, finalTarget.edge);
        }
      };
      panelDragCleanupRef.current = teardown;
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
    }, [renderedPaneBounds, agentPaneById, panelLeafById, onDockPanel]);

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
            className={`workspace-pane ${activePaneId === agentPane.id ? 'active' : ''}`}
            onMouseDown={() => handleAgentPaneMouseDown(agentPane.id)}
            data-pane-session-id={agentPane.sessionId}
            data-pane-id={agentPane.id}
            data-pane-kind="agent"
            data-pane-path={path}
            style={frameStyle}
          >
            <div
              className={`workspace-pane-header ${showPaneHeader ? '' : 'workspace-pane-header-hidden'}`.trim()}
              aria-hidden={showPaneHeader ? undefined : true}
            >
              {showPaneHeader ? <span className="workspace-pane-title">{paneTitle}</span> : null}
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
                  debugName={`agent:${paneTitle}:${paneSession?.agent ?? 'shell'}:${agentPane.sessionId}`}
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

      const panelLeaf = panelLeafById.get(paneId);
      if (panelLeaf) {
        return (
          <div
            key={`panel:${panelLeaf.panelId}`}
            className="workspace-pane workspace-pane--panel"
            data-pane-id={panelLeaf.panelId}
            data-pane-kind="panel"
            data-panel-kind={panelLeaf.panelKind}
            data-pane-path={path}
            style={frameStyle}
          >
            <WorkspaceDockPanel
              panel={panelLeaf}
              dragging={draggingPanelId === panelLeaf.panelId}
              onClose={() => onUndockPanel?.(panelLeaf.panelId)}
              onHeaderPointerDown={(event) => beginPanelDrag(panelLeaf.panelId, event)}
            />
          </div>
        );
      }

      return null;
    }, [
      activePaneId,
      beginPanelDrag,
      draggingPanelId,
      onUndockPanel,
      panelLeafById,
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
      showPaneHeader,
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

    // Draggable dividers, one per split. Hidden in focus/zoom mode where there
    // is no real split to resize.
    const splitDividers = useMemo<SplitDivider[]>(() => {
      if (!renderedLayoutTree || effectivePaneId || effectiveZoomedPaneId) {
        return [];
      }
      return getSplitDividers(renderedLayoutTree);
    }, [renderedLayoutTree, effectivePaneId, effectiveZoomedPaneId]);

    // rAF-coalesced override updates keep terminal re-fits to ~one per frame
    // while dragging.
    const ratioRafRef = useRef<number | null>(null);
    const pendingRatioRef = useRef<{ splitId: string; ratio: number } | null>(null);
    const dragCleanupRef = useRef<(() => void) | null>(null);

    const flushRatioOverride = useCallback(() => {
      ratioRafRef.current = null;
      const pending = pendingRatioRef.current;
      if (!pending) {
        return;
      }
      setRatioOverrides((prev) => {
        if (prev.get(pending.splitId) === pending.ratio) {
          return prev;
        }
        const next = new Map(prev);
        next.set(pending.splitId, pending.ratio);
        return next;
      });
    }, []);

    const handleDividerPointerDown = useCallback((divider: SplitDivider, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const container = (event.target as HTMLElement).closest('.session-terminal-panes') as HTMLElement | null;
      if (!container) {
        return;
      }
      const rect = container.getBoundingClientRect();
      const { splitId, direction, left, top, right, bottom } = divider;
      const spanNorm = direction === 'vertical' ? right - left : bottom - top;
      const axisPx = direction === 'vertical' ? rect.width : rect.height;
      const spanPx = spanNorm * axisPx;
      const minRatio = spanPx > 0 ? Math.min(0.45, 120 / spanPx) : 0.1;
      draggingSplitRef.current = splitId;
      const releaseSelectionLock = lockTextSelection(
        direction === 'vertical' ? 'col-resize' : 'row-resize',
      );

      const computeRatio = (clientX: number, clientY: number): number => {
        let ratio = 0.5;
        if (spanNorm > 0) {
          if (direction === 'vertical') {
            ratio = ((clientX - rect.left) / rect.width - left) / spanNorm;
          } else {
            ratio = ((clientY - rect.top) / rect.height - top) / spanNorm;
          }
        }
        return Math.min(1 - minRatio, Math.max(minRatio, ratio));
      };

      const onMove = (ev: PointerEvent) => {
        pendingRatioRef.current = { splitId, ratio: computeRatio(ev.clientX, ev.clientY) };
        if (ratioRafRef.current == null) {
          ratioRafRef.current = window.requestAnimationFrame(flushRatioOverride);
        }
      };
      const teardown = () => {
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        if (ratioRafRef.current != null) {
          window.cancelAnimationFrame(ratioRafRef.current);
          ratioRafRef.current = null;
        }
        releaseSelectionLock();
        dragCleanupRef.current = null;
      };
      const onUp = (ev: PointerEvent) => {
        const ratio = computeRatio(ev.clientX, ev.clientY);
        teardown();
        pendingRatioRef.current = { splitId, ratio };
        flushRatioOverride();
        draggingSplitRef.current = null;
        onResizeSplit?.(splitId, ratio);
      };
      dragCleanupRef.current = teardown;
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
    }, [flushRatioOverride, onResizeSplit]);

    useEffect(() => () => {
      dragCleanupRef.current?.();
      panelDragCleanupRef.current?.();
    }, []);

    if (!renderedLayoutTree) {
      return (
        <div
          className="session-terminal-workspace"
          data-session-terminal-workspace={workspaceId}
          data-workspace-id={workspaceId}
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
        data-session-terminal-workspace={workspaceId}
        data-workspace-id={workspaceId}
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
          dividers={splitDividers}
          onDividerPointerDown={handleDividerPointerDown}
          overlay={dockTarget ? (
            <div
              className="workspace-dock-target"
              style={{
                left: `${dockTarget.rect.left * 100}%`,
                top: `${dockTarget.rect.top * 100}%`,
                width: `${dockTarget.rect.width * 100}%`,
                height: `${dockTarget.rect.height * 100}%`,
              }}
            />
          ) : null}
        />
        {ghostPos && draggingPanelId && (
          <div
            className="workspace-dock-ghost"
            style={{ left: ghostPos.x + 12, top: ghostPos.y + 12 }}
          >
            {panelLeafById.get(draggingPanelId)?.panelKind === 'markdown' ? 'README.md' : 'Panel'}
          </div>
        )}
      </div>
    );
  }
);
