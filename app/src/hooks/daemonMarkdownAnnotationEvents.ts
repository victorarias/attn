/** Markdown-annotation daemon event decoding (`markdown_annotations_*_result`). */

import { takeKeyedRequest, type PendingKeyedRequests } from './daemonPendingRequests';

/** `<op>:<uri>` — one in-flight request per document per operation. */
export function markdownAnnotationKey(op: string, documentUri: string): string {
  return `${op}:${documentUri}`;
}

/** The event shapes this module reads, loosely typed off the wire union. */
type MarkdownAnnotationEvent = {
  event: string;
  request_id?: unknown;
  document_uri?: unknown;
  success?: boolean;
  error?: string;
  stale?: unknown;
  status?: unknown;
  generation?: unknown;
  annotations?: unknown;
};

/**
 * Handle one markdown-annotation event. Returns false when the event is not one
 * of ours, so the caller can keep its own dispatch exhaustive.
 */
export function handleMarkdownAnnotationDaemonEvent(
  event: MarkdownAnnotationEvent,
  pending: PendingKeyedRequests,
): boolean {
  switch (event.event) {
    case 'markdown_annotations_get_result':
    case 'markdown_annotations_save_result':
    case 'markdown_annotations_clear_result': {
      const op =
        event.event === 'markdown_annotations_get_result'
          ? 'get'
          : event.event === 'markdown_annotations_save_result'
            ? 'save'
            : 'clear';
      const key = markdownAnnotationKey(op, String(event.document_uri ?? ''));
      const waiter = takeKeyedRequest(pending, key, event.request_id);
      if (!waiter) {
        return true; // superseded or timed out — drop the late result
      }
      if (op === 'save' && !event.success && event.stale) {
        // Stale generation (tombstone / newer writer) is a protocol outcome the
        // client handles, not an error.
        waiter.resolve({ stale: true });
      } else if (!event.success) {
        waiter.reject(new Error(event.error || `markdown_annotations_${op} failed`));
      } else if (op === 'get') {
        waiter.resolve({
          annotations: Array.isArray(event.annotations) ? event.annotations : [],
          generation: typeof event.generation === 'number' ? event.generation : 0,
        });
      } else if (op === 'save') {
        waiter.resolve({ stale: false });
      } else {
        waiter.resolve({
          generation: typeof event.generation === 'number' ? event.generation : 0,
        });
      }
      return true;
    }

    case 'markdown_annotations_submit_result': {
      const key = markdownAnnotationKey('submit', String(event.document_uri ?? ''));
      const waiter = takeKeyedRequest(pending, key, event.request_id);
      if (!waiter) {
        return true; // superseded or timed out — drop the late result
      }
      if (event.success || event.status === 'skipped_pending_approval') {
        // skipped_pending_approval resolves (success:false) so the UI can
        // message it distinctly from a hard failure; a delivered result may
        // still carry `error` (delivery succeeded, draft clear failed) — never
        // re-deliver in that case.
        waiter.resolve({
          status: typeof event.status === 'string' && event.status !== '' ? event.status : 'delivered',
          ...(typeof event.generation === 'number' ? { generation: event.generation } : {}),
          ...(typeof event.error === 'string' && event.error !== '' ? { error: event.error } : {}),
        });
      } else {
        waiter.reject(new Error(event.error || 'markdown_annotations_submit failed'));
      }
      return true;
    }

    default:
      return false;
  }
}
