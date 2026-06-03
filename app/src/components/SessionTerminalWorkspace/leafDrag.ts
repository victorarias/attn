import type { NormalizedPaneBounds } from '../../types/workspace';
import { computeDockTarget, computeContainerSides, type DockTarget } from './dockTarget';

// startLeafDrag owns the pointerdown → pointermove → pointerup gesture for moving
// a leaf. It snapshots drop targets from the container once (the layout is static
// during a drag), drives a live preview, and reports the final drop. Side effects
// (selection lock, React state, the actual move command) are injected as handlers
// so the gesture is testable with synthetic pointer events.

// The pointer must travel this far from the press point before the gesture counts
// as a drag. A plain click (press + release in place, or a tiny jitter) stays
// below the threshold and resolves as a no-op — no activation, no preview, no
// drop. Without this, clicking a pane header (which sits in the workspace's top
// perimeter band) computes a drop target at the press position and relocates the
// leaf into a 50% container split.
const DRAG_ACTIVATION_PX = 4;

export interface LeafDragHandlers {
  // Fires once, when the pointer first crosses the activation threshold. Visual
  // drag side effects (selection lock, dragging styles, parent overlay) belong
  // here so a plain click leaves no trace.
  onActivate: () => void;
  // Live dock preview as the pointer moves (null = no valid target right now).
  onPreview: (target: DockTarget | null) => void;
  // Ghost label position follows the pointer.
  onGhostMove: (clientX: number, clientY: number) => void;
  // The gesture resolved over a target: relocate leafId there.
  onDrop: (leafId: string, target: DockTarget) => void;
  // Always runs when the gesture ends (drop, cancel, click, or blur): release
  // locks and clear transient state.
  onCleanup: () => void;
}

export interface LeafDropSnapshot {
  container: HTMLElement;
  paneBounds: Map<string, NormalizedPaneBounds>;
}

// startLeafDrag registers window listeners for one drag and returns a teardown
// fn (also invoked internally on drop/cancel). The dragged leaf is excluded from
// the drop targets, so hovering it previews nothing — a self-drop no-op.
export function startLeafDrag(
  leafId: string,
  clientX: number,
  clientY: number,
  container: HTMLElement,
  paneBounds: Map<string, NormalizedPaneBounds>,
  handlers: LeafDragHandlers,
  getDropSnapshot?: () => LeafDropSnapshot | null,
): () => void {
  const fallbackSnapshot = { container, paneBounds };

  const computeTarget = (x: number, y: number): DockTarget | null => {
    const snapshot = getDropSnapshot?.() ?? fallbackSnapshot;
    const containerRect = snapshot.container.getBoundingClientRect();
    const leafRects: Array<{ leafId: string; bounds: NormalizedPaneBounds }> = [];
    const allBounds: NormalizedPaneBounds[] = [];
    for (const [slotId, bounds] of snapshot.paneBounds) {
      allBounds.push(bounds);
      if (slotId !== leafId) {
        leafRects.push({ leafId: slotId, bounds });
      }
    }
    const containerSides = computeContainerSides(allBounds);
    return computeDockTarget(x, y, containerRect, leafRects, containerSides);
  };

  const pastThreshold = (x: number, y: number): boolean =>
    Math.hypot(x - clientX, y - clientY) >= DRAG_ACTIVATION_PX;

  // The gesture stays pending until the pointer moves past the threshold; only
  // then does it activate and start previewing/dropping.
  let activated = false;

  const onMove = (ev: PointerEvent) => {
    if (!activated) {
      if (!pastThreshold(ev.clientX, ev.clientY)) {
        return;
      }
      activated = true;
      handlers.onActivate();
    }
    handlers.onGhostMove(ev.clientX, ev.clientY);
    handlers.onPreview(computeTarget(ev.clientX, ev.clientY));
  };
  const teardown = () => {
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointercancel', onCancel);
    window.removeEventListener('blur', onCancel);
    handlers.onCleanup();
  };
  const onUp = (ev: PointerEvent) => {
    // Resolve a drop only if the gesture became a drag — either it already
    // activated, or the pointer ended up past the threshold (a fast drag whose
    // move events were coalesced). A press-and-release in place stays a click.
    const isDrag = activated || pastThreshold(ev.clientX, ev.clientY);
    const finalTarget = isDrag ? computeTarget(ev.clientX, ev.clientY) : null;
    teardown();
    if (finalTarget) {
      handlers.onDrop(leafId, finalTarget);
    }
  };
  const onCancel = () => {
    teardown();
  };

  window.addEventListener('pointermove', onMove);
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', onCancel);
  window.addEventListener('blur', onCancel);
  return teardown;
}
