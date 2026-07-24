import type { PtyAttachPolicy, PtySpawnArgs } from './bridge';

export interface ExistingRuntimeAttachOptions {
  policy: Extract<
    PtyAttachPolicy,
    'relaunch_restore' | 'same_app_remount' | 'revive'
  >;
  forceResizeBeforeAttach?: boolean;
}

export interface SpawnPtyRuntimeContext {
  runtimeKnownToDaemon: boolean;
  alreadyAttached: boolean;
}

export interface SpawnPtyRuntimeOperations {
  attachFreshRuntime(args: PtySpawnArgs): Promise<unknown>;
  spawnRuntime(args: PtySpawnArgs): Promise<unknown>;
  resizeRuntime(id: string, cols: number, rows: number, reason: string): void;
}

export function normalizeAttachPolicy(
  policy?: PtyAttachPolicy,
): Extract<PtyAttachPolicy, 'relaunch_restore' | 'same_app_remount' | 'revive'> {
  if (policy === 'relaunch_restore' || policy === 'revive') {
    return policy;
  }
  return 'same_app_remount';
}

export function isAlreadyExistsError(error: unknown): boolean {
  const message = error instanceof Error ? error.message.toLowerCase() : String(error).toLowerCase();
  return message.includes('already exists');
}

export async function spawnPtyRuntime(
  args: PtySpawnArgs,
  context: SpawnPtyRuntimeContext,
  operations: SpawnPtyRuntimeOperations,
): Promise<void> {
  if (context.alreadyAttached) {
    operations.resizeRuntime(args.id, args.cols, args.rows, 'already_attached');
    return;
  }

  if (!context.runtimeKnownToDaemon) {
    operations.resizeRuntime(args.id, args.cols, args.rows, 'spawn_bootstrap');
  }

  try {
    await operations.spawnRuntime(args);
  } catch (error) {
    if (!isAlreadyExistsError(error)) {
      throw error;
    }
  }

  await operations.attachFreshRuntime(args);
}
