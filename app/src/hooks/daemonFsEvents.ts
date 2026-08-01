/**
 * Filesystem daemon events: `fs_changed` and the `fs_*_result` answers to the
 * filesystem commands the notebook, the markdown opener, and the file pickers
 * issue. Kept out of `useDaemonSocket.ts` so that grepping an `fs_` wire name
 * lands in a file about the filesystem rather than in the socket hook.
 *
 * The hook still owns the transport; this module owns the event bodies.
 */

import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The event shapes this module reads, loosely typed off the wire union. */
type FsEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  origin?: unknown;
  paths?: unknown;
  root?: unknown;
  files?: unknown;
  truncated?: unknown;
  entries?: unknown;
  result?: Record<string, unknown>;
};

/** What the fs handlers need from the hook: the waiters and the fs-change callback. */
export interface FsEventContext {
  pending: PendingRequests;
  onFsChanged?: (origin: string, paths: string[], root: string) => void;
}

/**
 * Handle one filesystem event. Returns false when the event is not one of ours,
 * so the caller can keep its own dispatch exhaustive.
 */
export function handleFsDaemonEvent(event: FsEvent, ctx: FsEventContext): boolean {
  const { pending } = ctx;
  switch (event.event) {
    case 'fs_changed':
      ctx.onFsChanged?.(
        typeof event.origin === 'string' ? event.origin : '',
        Array.isArray(event.paths) ? event.paths : [],
        typeof event.root === 'string' ? event.root : '',
      );
      return true;

    case 'fs_list_result':
      settlePendingRequest(
        pending,
        'fs_list',
        event,
        // The wire says is_dir; the public FsEntry shape says isDir.
        (e) =>
          ((e.entries || []) as Array<{
            path: string;
            name: string;
            is_dir?: boolean;
            size?: number;
            modified?: string;
          }>).map((entry) => ({
            path: entry.path,
            name: entry.name,
            isDir: !!entry.is_dir,
            size: typeof entry.size === 'number' ? entry.size : 0,
            modified: entry.modified,
          })),
        'Filesystem list failed',
      );
      return true;

    case 'fs_read_result':
      settlePendingRequest(pending, 'fs_read', event, (e) => e.result, 'Filesystem read failed');
      return true;

    case 'fs_read_asset_result':
      settlePendingRequest(
        pending,
        'fs_read_asset',
        event,
        (e) =>
          e.result && {
            path: e.result.path,
            mimeType: e.result.mime_type,
            dataBase64: e.result.data_base64,
          },
        'Filesystem asset read failed',
      );
      return true;

    case 'fs_write_result':
      settlePendingRequest(
        pending,
        'fs_write',
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
        'Filesystem write failed',
      );
      return true;

    case 'fs_rename_result':
    case 'fs_delete_result':
      settlePendingRequest(
        pending,
        event.event === 'fs_rename_result' ? 'fs_rename' : 'fs_delete',
        event,
        (e) => e.result,
        'Filesystem action failed',
      );
      return true;

    case 'fs_exists_result':
      settlePendingRequest(
        pending,
        'fs_exists',
        event,
        (e) => e.result && { path: e.result.path, exists: !!e.result.exists },
        'Filesystem exists check failed',
      );
      return true;

    case 'fs_watch_result':
    case 'fs_unwatch_result':
      settlePendingRequest(
        pending,
        event.event === 'fs_watch_result' ? 'fs_watch' : 'fs_unwatch',
        event,
        (e) => (typeof e.root === 'string' && e.root ? { root: e.root } : undefined),
        'Filesystem watch action failed',
      );
      return true;

    case 'fs_index_result':
      settlePendingRequest(
        pending,
        'fs_index',
        event,
        (e) => ({
          root: e.root,
          files: e.files || [],
          truncated: !!e.truncated,
        }),
        'Filesystem index failed',
      );
      return true;

    default:
      return false;
  }
}
