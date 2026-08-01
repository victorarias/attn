/**
 * Notebook daemon events: `notebook_changed` and the `notebook_*_result` answers
 * to the notebook read/write/list commands. Kept out of `useDaemonSocket.ts` so
 * that grepping a `notebook_` wire name lands in a file about the notebook.
 *
 * See docs/glossary.md for what the notebook is; this module only moves its
 * events between the socket and the waiting promise.
 */

import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The event shapes this module reads, loosely typed off the wire union. */
type NotebookEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  origin?: unknown;
  paths?: unknown;
  entries?: unknown;
  result?: Record<string, unknown>;
};

/** What the notebook handlers need from the hook. */
export interface NotebookEventContext {
  pending: PendingRequests;
  onNotebookChanged?: (origin: string, paths: string[]) => void;
}

/**
 * Handle one notebook event. Returns false when the event is not one of ours,
 * so the caller can keep its own dispatch exhaustive.
 */
export function handleNotebookDaemonEvent(event: NotebookEvent, ctx: NotebookEventContext): boolean {
  const { pending } = ctx;
  switch (event.event) {
    case 'notebook_changed':
      ctx.onNotebookChanged?.(
        typeof event.origin === 'string' ? event.origin : '',
        Array.isArray(event.paths) ? event.paths : [],
      );
      return true;

    case 'notebook_list_result':
    case 'notebook_backlinks_result':
      settlePendingRequest(
        pending,
        event.event === 'notebook_list_result' ? 'notebook_list' : 'notebook_backlinks',
        event,
        (e) => e.entries || [],
        'Notebook request failed',
      );
      return true;

    case 'notebook_read_result':
      settlePendingRequest(pending, 'notebook_read', event, (e) => e.result, 'Notebook read failed');
      return true;

    case 'notebook_write_result':
      settlePendingRequest(
        pending,
        'notebook_write',
        event,
        // A conflict is a SUCCESSFUL result the editor reconciles, not a
        // rejection — only a transport/daemon error rejects.
        (e) =>
          e.result && {
            path: e.result.path,
            hash: e.result.hash,
            conflict: !!e.result.conflict,
            currentHash: e.result.current_hash,
          },
        'Notebook write failed',
      );
      return true;

    case 'notebook_send_to_chief_result':
      settlePendingRequest(
        pending,
        'notebook_send_to_chief',
        event,
        (e) => e.result && { path: e.result.path, nudged: !!e.result.nudged },
        'Send to chief failed',
      );
      return true;

    default:
      return false;
  }
}
