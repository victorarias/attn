import type { ReviewComment } from '../types/generated';

/**
 * Helpers for the review-comment line convention shared by the diff panel and
 * the diff viewer.
 *
 * Protocol convention: a comment anchors at `line_start`; a NEGATIVE `line_end`
 * encodes the original/deleted side. The anchored range is
 * `line_start`..`abs(line_end)`.
 */

/** Format a 1-based inclusive line range as `L<start>` or `L<start>-L<end>`. */
export function buildLineRef(start: number, end: number): string {
  return start === end ? `L${start}` : `L${start}-L${end}`;
}

/** True when the comment anchors on the original (deleted) side of the diff. */
export function isOriginalSideComment(comment: Pick<ReviewComment, 'line_end'>): boolean {
  return comment.line_end < 0;
}

/** The `L<start>[-L<end>]` reference for a stored comment's anchored range. */
export function commentLineRef(comment: Pick<ReviewComment, 'line_start' | 'line_end'>): string {
  return buildLineRef(comment.line_start, Math.abs(comment.line_end));
}
