/**
 * Draft-persistence transport seam (plannotator's DraftTransport pattern,
 * translated to the daemon websocket).
 *
 * App startup registers the three useDaemonSocket helpers here; useAnnotations
 * reads the transport at call time (never reactively — it is set once, before
 * any markdown tile can mount). A null transport degrades the hook to
 * local-only annotations: hydration resolves empty and saves no-op. Tests
 * pass a transport directly into the hook instead of using this registry.
 *
 * CONTRACT (mirrors the daemon store's generation tombstoning — see
 * internal/store/markdown_annotations.go):
 * - every save carries a client generation, pre-incremented before each save;
 * - clear(generation) is a generation-gated tombstone; any later save with
 *   generation <= tombstone resolves `{stale: true}` — the client must drop
 *   its pending list and re-hydrate rather than retry;
 * - get returns the generation floor even when there is no draft, so a
 *   re-mounting client seeds its counter past the tombstone.
 */

import type { WireAnnotation } from './types';

export interface MarkdownAnnotationsTransport {
  getMarkdownAnnotations(
    path: string,
  ): Promise<{ annotations: WireAnnotation[]; generation: number }>;
  saveMarkdownAnnotations(
    path: string,
    annotations: WireAnnotation[],
    generation: number,
  ): Promise<{ stale: boolean }>;
  clearMarkdownAnnotations(path: string, generation: number): Promise<{ generation: number }>;
}

let currentTransport: MarkdownAnnotationsTransport | null = null;

/** Register (or clear, with null) the app-wide transport. Called from App. */
export function setMarkdownAnnotationsTransport(
  transport: MarkdownAnnotationsTransport | null,
): void {
  currentTransport = transport;
}

/** Read the active transport at call time. Null when no daemon socket exists. */
export function getMarkdownAnnotationsTransport(): MarkdownAnnotationsTransport | null {
  return currentTransport;
}
