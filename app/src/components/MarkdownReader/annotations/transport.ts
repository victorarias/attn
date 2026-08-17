/**
 * Draft-persistence transport seam for markdown annotations. App startup
 * registers the daemon-socket helpers here; useAnnotations reads it at call
 * time, never reactively, and a null transport means local-only annotations.
 * Calls carry an opaque URI for identity plus typed file/seed fields. The
 * daemon acts on those typed fields and never parses authority from the URI.
 *
 * Generation contract, mirroring internal/store/markdown_annotations.go: saves
 * carry a pre-incremented generation; clear(generation) tombstones, so a later
 * save with generation <= tombstone resolves `{stale: true}` and the client
 * must re-hydrate rather than retry; get returns the floor even with no draft.
 */

import type { WireAnnotation } from './types';
import type { MarkdownDocumentSource } from '../documentSource';

/**
 * Submit result. `delivered` types into a session; `noted` appends to a seed.
 * Both tombstone-clear drafts (`generation` is the client's new floor;
 * `error` may still report a failed clear). `skipped_pending_approval` keeps
 * the draft. An error status rejects the promise instead.
 */
export interface MarkdownAnnotationsSubmitResult {
  status: string;
  generation?: number;
  error?: string;
}

export type MarkdownAnnotationsDestination =
  | { kind: 'session'; sessionId: string }
  | { kind: 'seed'; seedId: string };

export interface MarkdownAnnotationsTransport {
  getMarkdownAnnotations(
    source: MarkdownDocumentSource,
  ): Promise<{ annotations: WireAnnotation[]; generation: number }>;
  saveMarkdownAnnotations(
    source: MarkdownDocumentSource,
    annotations: WireAnnotation[],
    generation: number,
  ): Promise<{ stale: boolean }>;
  clearMarkdownAnnotations(
    source: MarkdownDocumentSource,
    generation: number,
  ): Promise<{ generation: number }>;
  /**
   * Routed by the typed destination. `orphanedIds` is client-derived and not
   * persisted; the document URI remains opaque identity.
   */
  submitMarkdownAnnotations(
    source: MarkdownDocumentSource,
    destination: MarkdownAnnotationsDestination,
    orphanedIds: string[],
  ): Promise<MarkdownAnnotationsSubmitResult>;
}

let currentTransport: MarkdownAnnotationsTransport | null = null;

export function setMarkdownAnnotationsTransport(
  transport: MarkdownAnnotationsTransport | null,
): void {
  currentTransport = transport;
}

export function getMarkdownAnnotationsTransport(): MarkdownAnnotationsTransport | null {
  return currentTransport;
}
