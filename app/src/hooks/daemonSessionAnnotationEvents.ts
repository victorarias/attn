/**
 * Terminal-annotation daemon events: the annotatable message window
 * (`session_messages_get_result`) and the persisted annotation list
 * (`session_annotations_*_result`). Kept out of `useDaemonSocket.ts` so that
 * grepping a `session_annotations_` wire name lands in a file about
 * annotations.
 *
 * These use the shared `<kind>:<requestId>` correlation, not the keyed
 * last-writer-wins scheme in daemonMarkdownAnnotationEvents.ts. The difference
 * is deliberate: markdown drafts supersede each other client-side, whereas
 * these are ordered server-side by a generation, so every request here is an
 * ordinary one that deserves its own answer.
 */

import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The event shapes this module reads, loosely typed off the wire union. */
type SessionAnnotationEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  stale?: unknown;
  status?: unknown;
  messages?: unknown;
  truncated?: unknown;
  annotations?: unknown;
  note?: unknown;
  generation?: unknown;
};

/** One assistant message that can be annotated. */
export interface DaemonSessionMessage {
  key: string;
  markdown: string;
}

/** A persisted annotation, exactly as the store holds it. */
export interface DaemonSessionAnnotation {
  id: string;
  messageKey: string;
  start: number;
  end: number;
  quote: string;
  emoji: string;
  comment: string;
}

function toMessages(raw: unknown): DaemonSessionMessage[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((entry) => ({
    key: String((entry as { key?: unknown })?.key ?? ''),
    markdown: String((entry as { markdown?: unknown })?.markdown ?? ''),
  })).filter((message) => message.key !== '' && message.markdown !== '');
}

function toAnnotations(raw: unknown): DaemonSessionAnnotation[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((entry) => {
    const record = entry as Record<string, unknown>;
    return {
      id: String(record?.id ?? ''),
      messageKey: String(record?.message_key ?? ''),
      start: Number(record?.start ?? 0),
      end: Number(record?.end ?? 0),
      quote: String(record?.quote ?? ''),
      emoji: String(record?.emoji ?? ''),
      comment: String(record?.comment ?? ''),
    };
    // An entry with no id could never be addressed by a later save, so it is
    // dropped rather than shown as something the user can edit.
  }).filter((annotation) => annotation.id !== '' && annotation.messageKey !== '');
}

/** The wire shape of an annotation, as a save sends it. */
export function annotationToWire(annotation: DaemonSessionAnnotation): Record<string, unknown> {
  return {
    id: annotation.id,
    message_key: annotation.messageKey,
    start: annotation.start,
    end: annotation.end,
    quote: annotation.quote,
    emoji: annotation.emoji,
    comment: annotation.comment,
  };
}

/**
 * Handle one terminal-annotation event. Returns false when the event is not one
 * of ours, so the caller can keep its own dispatch exhaustive.
 */
export function handleSessionAnnotationDaemonEvent(
  event: SessionAnnotationEvent,
  pending: PendingRequests,
): boolean {
  switch (event.event) {
    case 'session_messages_get_result':
      settlePendingRequest(
        pending,
        'session_messages_get',
        event,
        (e) => ({ messages: toMessages(e.messages), truncated: e.truncated === true }),
        'Session message fetch failed',
      );
      return true;

    case 'session_annotations_get_result':
      settlePendingRequest(
        pending,
        'session_annotations_get',
        event,
        (e) => ({
          annotations: toAnnotations(e.annotations),
          note: typeof e.note === 'string' ? e.note : '',
          generation: typeof e.generation === 'number' ? e.generation : 0,
        }),
        'Session annotation fetch failed',
      );
      return true;

    case 'session_annotations_save_result': {
      // A stale save is a protocol outcome, not a failure: the client's list
      // lost to a newer one and it re-hydrates. Resolving it as an error would
      // put a routine race in front of the user.
      if (!event.success && event.stale === true) {
        settlePendingRequest(
          pending,
          'session_annotations_save',
          { ...event, success: true },
          () => ({ stale: true }),
          'Session annotation save failed',
        );
        return true;
      }
      settlePendingRequest(
        pending,
        'session_annotations_save',
        event,
        () => ({ stale: false }),
        'Session annotation save failed',
      );
      return true;
    }

    case 'session_annotations_clear_result':
      settlePendingRequest(
        pending,
        'session_annotations_clear',
        event,
        (e) => ({ generation: typeof e.generation === 'number' ? e.generation : 0 }),
        'Session annotation clear failed',
      );
      return true;

    case 'session_annotations_submit_result': {
      // A skip is an outcome to show, not a failure to report: the session is
      // sitting on an approval prompt where the submit's Enter would answer it,
      // so nothing was typed and the marks are still there to retry with.
      // Rejecting would file that alongside a dead socket and lose the reason.
      const skipped = !event.success && event.status === 'skipped_pending_approval';
      settlePendingRequest(
        pending,
        'session_annotations_submit',
        skipped ? { ...event, success: true } : event,
        (e) => ({ status: String(e.status ?? 'error') }),
        'Session annotation send failed',
      );
      return true;
    }

    default:
      return false;
  }
}
