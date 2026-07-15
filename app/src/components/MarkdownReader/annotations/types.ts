/**
 * Annotation model — the frontend shape plus converters to/from the protocol
 * wire shape (snake_case `MarkdownAnnotation` in types/generated.ts).
 *
 * A quick-label annotation is `type: 'comment'` + `quickLabelId` (with the
 * label's tip text snapshotted at creation so the PR6 payload survives label
 * set edits); `text` stays empty for pure quick-labels. `anchor` is absent
 * ONLY for `type: 'global'`.
 */

import type {
  MarkdownAnnotation as WireAnnotation,
  MarkdownAnnotationAnchor as WireAnchor,
} from '../../../types/generated';
import type { AnchorRecord } from '../anchoring';

export type { WireAnnotation, WireAnchor };

export type AnnotationType = 'comment' | 'deletion' | 'global';

export interface Annotation {
  id: string; // crypto.randomUUID()
  type: AnnotationType;
  /** Comment body (comment/global). Absent for deletion and pure quick-labels. */
  text?: string;
  /** Anchor into the document. Absent ONLY for type 'global'. */
  anchor?: AnchorRecord;
  /** Structured quick-label reference (NOT baked into text). */
  quickLabelId?: string;
  /** Tip text snapshotted at creation so the payload survives label-set edits. */
  quickLabelTip?: string;
  createdAt: number; // epoch ms
}

export function anchorToWire(anchor: AnchorRecord): WireAnchor {
  return {
    block_id: anchor.blockId,
    start_line: anchor.startLine,
    end_line: anchor.endLine,
    exact: anchor.exact,
    prefix: anchor.prefix,
    suffix: anchor.suffix,
    start: anchor.start,
    end: anchor.end,
    content_hash: anchor.contentHash,
  };
}

export function anchorFromWire(wire: WireAnchor): AnchorRecord {
  return {
    blockId: wire.block_id,
    startLine: wire.start_line,
    endLine: wire.end_line,
    exact: wire.exact,
    prefix: wire.prefix,
    suffix: wire.suffix,
    start: wire.start,
    end: wire.end,
    contentHash: wire.content_hash,
  };
}

export function annotationToWire(annotation: Annotation): WireAnnotation {
  return {
    id: annotation.id,
    type: annotation.type,
    ...(annotation.text !== undefined ? { text: annotation.text } : {}),
    ...(annotation.anchor ? { anchor: anchorToWire(annotation.anchor) } : {}),
    ...(annotation.quickLabelId !== undefined ? { quick_label_id: annotation.quickLabelId } : {}),
    ...(annotation.quickLabelTip !== undefined ? { quick_label_tip: annotation.quickLabelTip } : {}),
    created_at: annotation.createdAt,
  };
}

function isAnnotationType(value: string): value is AnnotationType {
  return value === 'comment' || value === 'deletion' || value === 'global';
}

/**
 * Wire → frontend. Returns null for records this build cannot represent
 * (unknown type, or a non-global annotation without an anchor) — hydration
 * drops them instead of crashing on forward-version drafts.
 */
export function annotationFromWire(wire: WireAnnotation): Annotation | null {
  if (!isAnnotationType(wire.type)) {
    return null;
  }
  if (wire.type !== 'global' && !wire.anchor) {
    return null;
  }
  return {
    id: wire.id,
    type: wire.type,
    ...(typeof wire.text === 'string' ? { text: wire.text } : {}),
    ...(wire.anchor ? { anchor: anchorFromWire(wire.anchor) } : {}),
    ...(typeof wire.quick_label_id === 'string' ? { quickLabelId: wire.quick_label_id } : {}),
    ...(typeof wire.quick_label_tip === 'string' ? { quickLabelTip: wire.quick_label_tip } : {}),
    createdAt: wire.created_at,
  };
}
