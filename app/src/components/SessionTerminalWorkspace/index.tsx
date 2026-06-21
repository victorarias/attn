import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { GhosttyTerminal, type BlockStateSnapshot, type GhosttyTerminalHandle } from '../GhosttyTerminal';
import { RenamePopover } from '../RenamePopover';
import { useShortcut } from '../../shortcuts';
import {
  getNormalizedPaneBounds,
  getSplitDividers,
  applyRatioOverrides,
  hasPane,
  findPaneInDirection,
  leafSlotId,
  tileContentKey,
  type SplitDivider,
  type TerminalNavigationDirection,
  type TerminalLayoutNode,
  type TileLeaf,
  type TileContentState,
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
import { WorkspaceDockTile } from './WorkspaceDockTile';
import { startLeafDrag, type LeafDropSnapshot } from './leafDrag';
import type { DockTarget } from './dockTarget';

const ZOOM_PATH_RATIO = 0.76;
const RESIZE_MOUSE_SUPPRESSION_MS = 1_500;
// After a drag ends the active-resize token is cleared, so suppression no longer
// rides on it; we only need to briefly swallow the trailing pointerup/synthetic
// click from the release itself. Keep this short so a deliberate click, select,
// or scroll right after resizing is not dropped.
const RESIZE_MOUSE_RELEASE_GUARD_MS = 150;

function suppressTerminalMouseDuringResize(durationMs = RESIZE_MOUSE_SUPPRESSION_MS): void {
  document.documentElement.dataset.attnWorkspaceMouseSuppressUntil = String(
    Date.now() + durationMs,
  );
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
  getPaneBlockState: (paneId: string) => BlockStateSnapshot | null;
  resetPaneTerminal: (paneId: string) => boolean;
  injectPaneBytes: (paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectPaneBase64: (paneId: string, payload: string) => Promise<boolean>;
  drainPaneTerminal: (paneId: string) => Promise<boolean>;
  getLeafDropSnapshot: () => LeafDropSnapshot | null;
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
  // When false, this workspace is virtualized: its terminals are not mounted
  // (freeing the Ghostty WASM model + WebGL renderer) and a placeholder is shown
  // instead. The terminal rehydrates from daemon replay when it remounts.
  terminalsLive?: boolean;
  eventRouter: PaneRuntimeEventRouter;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onRenameSession?: (sessionId: string, label: string) => Promise<void>;
  onZoomModeChange?: (zoomed: boolean) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
  onResizeSplit?: (splitId: string, ratio: number) => Promise<unknown> | void;
  // Move an existing leaf (terminal pane or docked tile) beside an anchor leaf,
  // or against the whole workspace when anchorId is ''. ratio is the moved leaf's
  // fraction of the new split.
  onMoveLeaf?: (leafId: string, anchorId: string, edge: TerminalDockEdge, ratio: number) => void;
  getActiveLeafDropSnapshot?: () => LeafDropSnapshot | null;
  onLeafDragStart?: (leafId: string) => void;
  onLeafDragGhostMove?: (clientX: number, clientY: number) => void;
  onLeafDragPreview?: (target: DockTarget | null) => void;
  onLeafDragEnd?: () => void;
  leafDragPreview?: {
    draggingLeafId: string | null;
    dockTarget: DockTarget | null;
    ghostPos: { x: number; y: number } | null;
  } | null;
  onUndockTile?: (tileId: string) => void;
  onUpdateTile?: (tileId: string, tileParams: string) => Promise<unknown> | void;
  tileContents?: Record<string, TileContentState>;
  allowLocalTileTargets?: boolean;
  onRequestTileContent?: (workspaceId: string, tileId: string) => void;
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
    terminalsLive = true,
    eventRouter,
    onSplitPane,
    onClosePane,
    onFocusPane,
    onRenameSession,
    onZoomModeChange,
    onNavigateOutOfSession,
    onResizeSplit,
    onMoveLeaf,
    getActiveLeafDropSnapshot,
    onLeafDragStart,
    onLeafDragGhostMove,
    onLeafDragPreview,
    onLeafDragEnd,
    leafDragPreview,
    onUndockTile,
    onUpdateTile,
    tileContents,
    allowLocalTileTargets = true,
    onRequestTileContent,
  }, ref) {
    const [maximizedPaneId, setMaximizedPaneId] = useState<string | null>(null);
    const [zoomedPaneId, setZoomedPaneId] = useState<string | null>(null);
    const [renamePane, setRenamePane] = useState<{
      sessionId: string;
      name: string;
      anchor: { top: number; left: number };
    } | null>(null);
    // Live, optimistic split ratios while dragging a divider. Reconciled against
    // the daemon layout once it echoes the persisted (locked) ratio back.
    const [ratioOverrides, setRatioOverrides] = useState<Map<string, number>>(() => new Map());
    const [resizingSplit, setResizingSplit] = useState<{ splitId: string; direction: TerminalSplitDirection } | null>(null);
    // Drag-to-dock state. The tile stays docked in the daemon tree throughout
    // the drag — this is a transient preview (ghost + target highlight) that
    // resolves to a single dock command on drop.
    const [draggingLeafId, setDraggingLeafId] = useState<string | null>(null);
    const [dockTarget, setDockTarget] = useState<DockTarget | null>(null);
    const [ghostPos, setGhostPos] = useState<{ x: number; y: number } | null>(null);
    const tileDragCleanupRef = useRef<(() => void) | null>(null);
    // Scrollable body of the first docked tile, focused on select when the
    // workspace has no terminal to own focus (see the focus effect below).
    const firstTileBodyRef = useRef<HTMLDivElement | null>(null);
    const panesContainerRef = useRef<HTMLDivElement | null>(null);
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

    // Docked tiles keyed by tile id, walked from the authoritative layout tree.
    const tileLeafById = useMemo(() => {
      const map = new Map<string, TileLeaf>();
      const walk = (node: TerminalLayoutNode | null) => {
        if (!node) {
          return;
        }
        if (node.type === 'split') {
          walk(node.children[0]);
          walk(node.children[1]);
          return;
        }
        if (node.type === 'tile') {
          map.set(node.tileId, node);
        }
      };
      walk(workspace.layoutTree);
      return map;
    }, [workspace.layoutTree]);

    // First tile in tree order (DFS-left). Used only to route the select-time
    // focus ref to a single tile when the workspace is fully tile-only.
    const firstTileId = useMemo(
      () => (tileLeafById.size > 0 ? tileLeafById.keys().next().value ?? null : null),
      [tileLeafById],
    );

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

    const runtime = useGhosttyPaneRuntime(
      runtimePanes,
      activePaneId,
      eventRouter,
      isActiveSessionRef,
      terminalsLive,
    );
    const setTerminalHandleRef = useRef(runtime.setTerminalHandle);
    const terminalRefCallbacksRef = useRef(new Map<
      string,
      (handle: GhosttyTerminalHandle | null) => void
    >());
    setTerminalHandleRef.current = runtime.setTerminalHandle;
    const terminalRefForPane = useCallback((paneId: string) => {
      const existing = terminalRefCallbacksRef.current.get(paneId);
      if (existing) return existing;
      const callback = (handle: GhosttyTerminalHandle | null) => {
        setTerminalHandleRef.current(paneId, handle);
      };
      terminalRefCallbacksRef.current.set(paneId, callback);
      return callback;
    }, []);
    const fitPane = runtime.fitPane;
    const scheduleTerminalFitAfterResize = useCallback(() => {
      window.requestAnimationFrame(() => {
        for (const pane of runtimePanes) {
          fitPane(pane.paneId);
        }
      });
    }, [fitPane, runtimePanes]);
    const getPaneSize = runtime.getPaneSize;
    const splitLayoutActive = workspace.layoutTree?.type === 'split';
    // Show the pane header (which doubles as the drag-to-move handle) whenever the
    // workspace holds more than one leaf — including tiles, so a lone pane sharing
    // space with a docked tile is still draggable.
    const showPaneHeader = paneIds.length + tileLeafById.size > 1;
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

    const clearRatioOverride = useCallback((splitId: string, expectedRatio?: number) => {
      setRatioOverrides((prev) => {
        const current = prev.get(splitId);
        if (current == null || (expectedRatio != null && Math.abs(current - expectedRatio) >= 0.005)) {
          return prev;
        }
        const next = new Map(prev);
        next.delete(splitId);
        return next;
      });
    }, []);

    // Any daemon layout broadcast is authoritative after a completed drag. Drop
    // stale local overrides whether persistence landed or another client won.
    // Never drop the split currently being dragged.
    useEffect(() => {
      setRatioOverrides((prev) => {
        if (prev.size === 0 || !workspace.layoutTree) {
          return prev;
        }
        let changed = false;
        const next = new Map(prev);
        for (const splitId of prev.keys()) {
          if (splitId === draggingSplitRef.current) {
            continue;
          }
          next.delete(splitId);
          changed = true;
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
        // Leaf: terminal panes and docked tiles both occupy a slot to render.
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
      getPaneBlockState: runtime.getPaneBlockState,
      resetPaneTerminal: runtime.resetPaneTerminal,
      injectPaneBytes: runtime.injectPaneBytes,
      injectPaneBase64: runtime.injectPaneBase64,
      drainPaneTerminal: runtime.drainPaneTerminal,
      getLeafDropSnapshot: () => (
        panesContainerRef.current
          ? { container: panesContainerRef.current, paneBounds: renderedPaneBounds }
          : null
      ),
    }), [activePaneId, renderedPaneBounds, runtime]);

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

    // A fully tile-only workspace has no terminal to own focus. Focus the first
    // tile's scrollable body so keyboard scrolling works the moment it is shown.
    // preventScroll keeps the body's own scroll position from jumping on focus.
    const focusFirstTile = useCallback(() => {
      firstTileBodyRef.current?.focus({ preventScroll: true });
    }, []);

    useEffect(() => {
      if (!sessionVisible) {
        return;
      }
      if (activePaneId) {
        focusActivePane();
        return;
      }
      focusFirstTile();
    }, [activePaneId, focusActivePane, focusFirstTile, focusRequestToken, isActiveSession, isSessionViewVisible, workspaceId, sessionVisible]);

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

    // Cmd+W closes the focused leaf. A docked tile (e.g. a notebook tile beside a
    // terminal) lives inside .session-terminal-workspace, so the global dispatcher
    // routes its Cmd+W to terminal.close — but the tile is not a terminal pane and
    // `activePaneId` still points at the terminal that was last focused. Without the
    // tile check below, Cmd+W from inside the notebook would kill that previous pane
    // (the reported bug). When focus is inside a tile, undock THAT tile instead.
    const handleCloseFocusedLeaf = useCallback(() => {
      const focused = document.activeElement;
      const tileEl = focused instanceof HTMLElement
        ? focused.closest('[data-pane-kind="tile"]')
        : null;
      const tileId = tileEl?.getAttribute('data-pane-id');
      if (tileId) {
        onUndockTile?.(tileId);
        return;
      }
      handleClosePane(activePaneId);
    }, [activePaneId, handleClosePane, onUndockTile]);

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
    useShortcut('terminal.find', () => { runtime.openFindInActivePane(); }, sessionVisible);
    useShortcut('terminal.splitVertical', () => { handleSplit('vertical'); }, sessionVisible);
    useShortcut('terminal.splitHorizontal', () => { handleSplit('horizontal'); }, sessionVisible);
    useShortcut('terminal.toggleZoom', toggleZoomActivePane, sessionVisible);
    useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, sessionVisible);
    useShortcut('terminal.close', handleCloseFocusedLeaf, sessionVisible && splitLayoutActive);
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

    // Drag any leaf's header (terminal pane or docked tile) to relocate it. The
    // daemon tree is untouched until drop, when a single move command sends it to
    // the previewed target. The dragged leaf is excluded from the drop targets so
    // hovering it is a no-op (self-drop).
    const beginLeafDrag = useCallback((leafId: string, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const container = (event.target as HTMLElement).closest('.session-terminal-panes') as HTMLElement | null;
      if (!container) {
        return;
      }
      // The press only becomes a drag once the pointer crosses the activation
      // threshold (see startLeafDrag). Defer every visual side effect to
      // onActivate so a plain header click leaves no trace and never docks.
      let releaseSelectionLock: (() => void) | null = null;
      const teardown = startLeafDrag(leafId, event.clientX, event.clientY, container, renderedPaneBounds, {
        onActivate: () => {
          releaseSelectionLock = lockTextSelection('grabbing');
          onLeafDragStart?.(leafId);
          setDraggingLeafId(leafId);
        },
        onGhostMove: (x, y) => {
          setGhostPos({ x, y });
          onLeafDragGhostMove?.(x, y);
        },
        onPreview: (target) => {
          setDockTarget(target);
          onLeafDragPreview?.(target);
        },
        onDrop: (id, target) => onMoveLeaf?.(id, target.anchorId, target.edge, target.ratio),
        onCleanup: () => {
          releaseSelectionLock?.();
          releaseSelectionLock = null;
          setDraggingLeafId(null);
          setDockTarget(null);
          setGhostPos(null);
          tileDragCleanupRef.current = null;
          onLeafDragEnd?.();
        },
      }, getActiveLeafDropSnapshot);
      tileDragCleanupRef.current = teardown;
    }, [getActiveLeafDropSnapshot, onLeafDragEnd, onLeafDragGhostMove, onLeafDragPreview, onLeafDragStart, renderedPaneBounds, onMoveLeaf]);

    const effectiveDraggingLeafId = leafDragPreview?.draggingLeafId ?? draggingLeafId;
    const effectiveDockTarget = leafDragPreview?.dockTarget ?? dockTarget;
    const effectiveGhostPos = leafDragPreview?.ghostPos ?? ghostPos;

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
            className={`workspace-pane ${activePaneId === agentPane.id ? 'active' : ''} ${effectiveDraggingLeafId === agentPane.id ? 'workspace-pane--dragging' : ''}`.trim()}
            onMouseDown={() => handleAgentPaneMouseDown(agentPane.id)}
            data-pane-session-id={agentPane.sessionId}
            data-pane-id={agentPane.id}
            data-pane-kind="agent"
            data-pane-path={path}
            style={frameStyle}
          >
            <div
              className={`workspace-pane-header ${showPaneHeader ? 'workspace-pane-header--draggable' : 'workspace-pane-header-hidden'}`.trim()}
              aria-hidden={showPaneHeader ? undefined : true}
              onPointerDown={showPaneHeader ? (event) => beginLeafDrag(agentPane.id, event) : undefined}
              title={showPaneHeader ? 'Drag to move' : undefined}
            >
              {showPaneHeader ? <span className="workspace-pane-title">{paneTitle}</span> : null}
              {showPaneHeader && onRenameSession ? (
                <button
                  type="button"
                  className="workspace-pane-rename-btn"
                  data-testid={`rename-pane-${agentPane.id}`}
                  // Stop the header's pointerdown drag from starting on the button.
                  onPointerDown={(event) => event.stopPropagation()}
                  onClick={(event) => {
                    event.stopPropagation();
                    const rect = event.currentTarget.getBoundingClientRect();
                    setRenamePane({
                      sessionId: agentPane.sessionId,
                      name: paneTitle,
                      anchor: { top: rect.bottom + 4, left: rect.left },
                    });
                  }}
                  title="Rename session"
                  aria-label={`Rename session ${paneTitle}`}
                >
                  ✎
                </button>
              ) : null}
            </div>
            <div className="workspace-pane-body">
              {isPaneStarting || isPaneFailed ? (
                <div className={`workspace-pane-status workspace-pane-status--${paneStatus}`}>
                  <span className="workspace-pane-status-spinner" aria-hidden="true" />
                  <span>{isPaneFailed ? (agentPane.error || 'Session failed to start') : `Starting ${paneTitle}...`}</span>
                </div>
              ) : !terminalsLive ? (
                // Virtualized: terminal unmounted to free WASM model + WebGL
                // renderer. Rehydrates from daemon replay when it remounts.
                <div className="workspace-pane-virtualized" aria-hidden="true" data-testid={`pane-virtualized-${agentPane.id}`} />
              ) : (
                <GhosttyTerminal
                  ref={terminalRefForPane(agentPane.id)}
                  fontSize={fontSize}
                  resolvedTheme={resolvedTheme}
                  cwd={paneSession?.cwd}
                  debugName={`agent:${paneTitle}:${paneSession?.agent ?? 'shell'}:${agentPane.sessionId}`}
                  runtimeLogMeta={{ sessionId: agentPane.sessionId, paneId: agentPane.id, runtimeId: agentPane.runtimeId, paneKind: 'agent', isActivePane: activePaneId === agentPane.id, isActiveSession, paneCount: paneIds.length }}
                  onInput={runtime.handleTerminalInput(agentPane.id)}
                  onReady={handleGhosttyTerminalReady(agentPane.id)}
                  onResize={runtime.handleTerminalResize(agentPane.id)}
                  onReplayInterrupted={runtime.handleReplayInterrupted(agentPane.id)}
                />
              )}
            </div>
          </div>
        );
      }

      const tileLeaf = tileLeafById.get(paneId);
      if (tileLeaf) {
        return (
          <div
            key={`tile:${tileLeaf.tileId}`}
            className="workspace-pane workspace-pane--tile"
            data-pane-id={tileLeaf.tileId}
            data-pane-kind="tile"
            data-tile-kind={tileLeaf.tileKind}
            data-pane-path={path}
            style={frameStyle}
          >
            <WorkspaceDockTile
              tile={tileLeaf}
              workspaceId={workspaceId}
              content={tileContents?.[tileContentKey(workspaceId, tileLeaf.tileId)]}
              allowLocalTargets={allowLocalTileTargets}
              dragging={effectiveDraggingLeafId === tileLeaf.tileId}
              visible={
                isActiveSession
                && isSessionViewVisible
                && enabled
                && renamePane === null
                && effectiveDraggingLeafId === null
              }
              onClose={() => onUndockTile?.(tileLeaf.tileId)}
              onUpdateParams={(tileParams) => onUpdateTile?.(tileLeaf.tileId, tileParams)}
              onHeaderPointerDown={(event) => beginLeafDrag(tileLeaf.tileId, event)}
              onRequestContent={onRequestTileContent ?? (() => {})}
              bodyRef={tileLeaf.tileId === firstTileId ? firstTileBodyRef : undefined}
            />
          </div>
        );
      }

      return null;
    }, [
      activePaneId,
      beginLeafDrag,
      effectiveDraggingLeafId,
      onUndockTile,
      onUpdateTile,
      tileLeafById,
      firstTileId,
      tileContents,
      allowLocalTileTargets,
      onRequestTileContent,
      workspaceId,
      renamePane,
      fontSize,
      isActiveSession,
      isSessionViewVisible,
      enabled,
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

    // Label shown in the drag ghost: the dragged leaf's session/title (pane) or
    // file name/kind (tile).
    const draggingLeafLabel = useMemo(() => {
      if (!effectiveDraggingLeafId) {
        return '';
      }
      const agentPane = agentPaneById.get(effectiveDraggingLeafId);
      if (agentPane) {
        return sessionById.get(agentPane.sessionId)?.label || agentPane.title || 'Pane';
      }
      const tile = tileLeafById.get(effectiveDraggingLeafId);
      if (tile) {
        const base = (tile.tileParams ?? '').split('/').filter(Boolean).pop();
        return base || tile.tileKind || 'Tile';
      }
      return 'Pane';
    }, [effectiveDraggingLeafId, agentPaneById, sessionById, tileLeafById]);

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
      event.stopPropagation();
      const container = (event.target as HTMLElement).closest('.session-terminal-panes') as HTMLElement | null;
      if (!container) {
        return;
      }
      const dividerElement = event.currentTarget;
      const pointerId = event.pointerId;
      if (typeof dividerElement.setPointerCapture === 'function') {
        try {
          dividerElement.setPointerCapture(pointerId);
        } catch {
          // The pointer can already be gone if the app loses focus mid-press.
        }
      }
      const rect = container.getBoundingClientRect();
      const { splitId, direction, left, top, right, bottom } = divider;
      const resizeToken = `${splitId}:${pointerId}`;
      suppressTerminalMouseDuringResize();
      container.dataset.resizingSplitId = splitId;
      container.dataset.resizingSplitDirection = direction;
      container.dataset.resizingSplitToken = resizeToken;
      document.documentElement.dataset.attnWorkspaceResizing = '1';
      document.documentElement.dataset.attnWorkspaceResizeToken = resizeToken;
      const spanNorm = direction === 'vertical' ? right - left : bottom - top;
      const axisPx = direction === 'vertical' ? rect.width : rect.height;
      const spanPx = spanNorm * axisPx;
      const minRatio = spanPx > 0 ? Math.min(0.45, 120 / spanPx) : 0.1;
      draggingSplitRef.current = splitId;
      setResizingSplit({ splitId, direction });
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
        ev.preventDefault();
        ev.stopPropagation();
        suppressTerminalMouseDuringResize();
        pendingRatioRef.current = { splitId, ratio: computeRatio(ev.clientX, ev.clientY) };
        if (ratioRafRef.current == null) {
          ratioRafRef.current = window.requestAnimationFrame(flushRatioOverride);
        }
      };
      const teardown = () => {
        window.removeEventListener('pointermove', onMove, true);
        window.removeEventListener('pointerup', onUp, true);
        window.removeEventListener('pointercancel', onCancel, true);
        window.removeEventListener('blur', onCancel);
        if (
          typeof dividerElement.hasPointerCapture === 'function'
          && typeof dividerElement.releasePointerCapture === 'function'
          && dividerElement.hasPointerCapture(pointerId)
        ) {
          try {
            dividerElement.releasePointerCapture(pointerId);
          } catch {
            // Nothing to release if the browser already cancelled capture.
          }
        }
        if (ratioRafRef.current != null) {
          window.cancelAnimationFrame(ratioRafRef.current);
          ratioRafRef.current = null;
        }
        if (container.dataset.resizingSplitToken === resizeToken) {
          delete container.dataset.resizingSplitId;
          delete container.dataset.resizingSplitDirection;
          delete container.dataset.resizingSplitToken;
        }
        if (document.documentElement.dataset.attnWorkspaceResizeToken === resizeToken) {
          delete document.documentElement.dataset.attnWorkspaceResizing;
          delete document.documentElement.dataset.attnWorkspaceResizeToken;
        }
        // Only a short trailing-event guard here — the long during-drag window
        // would otherwise outlive the drag and swallow normal interaction.
        suppressTerminalMouseDuringResize(RESIZE_MOUSE_RELEASE_GUARD_MS);
        releaseSelectionLock();
        setResizingSplit((current) => (current?.splitId === splitId ? null : current));
        dragCleanupRef.current = null;
      };
      const onCancel = () => {
        teardown();
        pendingRatioRef.current = null;
        draggingSplitRef.current = null;
        clearRatioOverride(splitId);
        scheduleTerminalFitAfterResize();
      };
      const onUp = (ev: PointerEvent) => {
        ev.preventDefault();
        ev.stopPropagation();
        const ratio = computeRatio(ev.clientX, ev.clientY);
        teardown();
        pendingRatioRef.current = { splitId, ratio };
        flushRatioOverride();
        draggingSplitRef.current = null;
        scheduleTerminalFitAfterResize();
        const resizeResult = onResizeSplit?.(splitId, ratio);
        if (resizeResult) {
          void resizeResult.catch(() => clearRatioOverride(splitId, ratio));
        }
      };
      dragCleanupRef.current = teardown;
      window.addEventListener('pointermove', onMove, true);
      window.addEventListener('pointerup', onUp, true);
      window.addEventListener('pointercancel', onCancel, true);
      window.addEventListener('blur', onCancel);
    }, [clearRatioOverride, flushRatioOverride, onResizeSplit, scheduleTerminalFitAfterResize]);

    useEffect(() => () => {
      dragCleanupRef.current?.();
      tileDragCleanupRef.current?.();
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
          containerRef={panesContainerRef}
          dividers={splitDividers}
          onDividerPointerDown={handleDividerPointerDown}
          overlay={(
            <>
              {effectiveDockTarget ? (
                <div
                  className="workspace-dock-target"
                  style={{
                    left: `${effectiveDockTarget.rect.left * 100}%`,
                    top: `${effectiveDockTarget.rect.top * 100}%`,
                    width: `${effectiveDockTarget.rect.width * 100}%`,
                    height: `${effectiveDockTarget.rect.height * 100}%`,
                  }}
                />
              ) : null}
              {resizingSplit ? (
                <div
                  className={`workspace-resize-shield workspace-resize-shield--${resizingSplit.direction}`}
                  data-resizing-split-id={resizingSplit.splitId}
                  onPointerDown={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onPointerMove={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onMouseDown={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onMouseMove={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onMouseUp={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                />
              ) : null}
            </>
          )}
        />
        {effectiveGhostPos && effectiveDraggingLeafId && (
          <div
            className="workspace-dock-ghost"
            style={{ left: effectiveGhostPos.x + 12, top: effectiveGhostPos.y + 12 }}
          >
            {draggingLeafLabel}
          </div>
        )}
        {renamePane && onRenameSession && (
          <RenamePopover
            key={renamePane.sessionId}
            initialValue={renamePane.name}
            label="Rename session"
            anchor={renamePane.anchor}
            onSubmit={(value) => onRenameSession(renamePane.sessionId, value)}
            onClose={() => setRenamePane(null)}
          />
        )}
      </div>
    );
  }
);
