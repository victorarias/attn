/**
 * Module-level registry so the UI automation bridge can read annotation state
 * without a reference into a conditionally-mounted MarkdownReader (same
 * pattern as components/grid/gridAutomation.ts). The annotations hook
 * registers a handle while mounted and unregisters exactly its own handle on
 * unmount; the bridge reads through getMarkdownAnnotationsAutomationHandle().
 *
 * With multiple markdown tiles open the LAST mounted reader wins — a harness
 * affordance, not a product surface (harness scenarios drive one tile). The
 * registry is a stack, not a single slot, so closing one tile never blinds
 * the bridge to another tile that is still open.
 */

import type { OrphanReason } from '../anchoring';

export interface MarkdownAnnotationsAutomationState {
  available: boolean;
  /** Painter strategy: 'custom-highlight' (real WKWebView) or 'mark' (test DOMs). */
  mode: 'custom-highlight' | 'mark' | 'none';
  path: string;
  generation: number;
  hydrated: boolean;
  pendingSelection: boolean;
  selectedId: string | null;
  annotations: Array<{
    id: string;
    type: string;
    text: string | null;
    quickLabelId: string | null;
    orphaned: boolean;
    orphanReason: OrphanReason | 'non-paintable-block' | 'unpaintable' | null;
    exact: string | null;
    blockId: string | null;
    startLine: number | null;
    endLine: number | null;
    start: number | null;
    end: number | null;
  }>;
}

export interface MarkdownAnnotationsAutomationHandle {
  getState(): MarkdownAnnotationsAutomationState;
}

const handles: MarkdownAnnotationsAutomationHandle[] = [];

/** Register a handle; returns the unregister function (identity-scoped: it
    removes only THIS handle, never a sibling tile's). */
export function registerMarkdownAnnotationsAutomationHandle(
  handle: MarkdownAnnotationsAutomationHandle,
): () => void {
  handles.push(handle);
  return () => {
    const i = handles.lastIndexOf(handle);
    if (i !== -1) {
      handles.splice(i, 1);
    }
  };
}

/** Most recently registered live handle (last-mounted tile wins). */
export function getMarkdownAnnotationsAutomationHandle(): MarkdownAnnotationsAutomationHandle | null {
  return handles.length > 0 ? handles[handles.length - 1] : null;
}

export const INACTIVE_MARKDOWN_ANNOTATIONS_STATE: MarkdownAnnotationsAutomationState = {
  available: false,
  mode: 'none',
  path: '',
  generation: 0,
  hydrated: false,
  pendingSelection: false,
  selectedId: null,
  annotations: [],
};
