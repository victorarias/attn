import type { PtyAttachArgs, PtyAttachPolicy, PtySpawnArgs } from './bridge';

export interface RuntimeLifecycleSessionSnapshot {
  agent?: string;
  state?: string;
}

export interface ExistingRuntimeAttachOptions {
  policy: Extract<
    PtyAttachPolicy,
    'relaunch_restore' | 'same_app_remount' | 'revive'
  >;
  forceResizeBeforeAttach?: boolean;
}

export interface SpawnPtyRuntimeContext {
  existingSession?: RuntimeLifecycleSessionSnapshot;
  runtimeKnownToDaemon: boolean;
  alreadyAttached: boolean;
}

export interface SpawnPtyRuntimeOperations {
  attachExistingRuntime(
    args: Pick<PtyAttachArgs, 'id' | 'cols' | 'rows' | 'shell' | 'agent' | 'reason'>,
    options: ExistingRuntimeAttachOptions,
  ): Promise<unknown>;
  attachFreshRuntime(args: PtySpawnArgs): Promise<unknown>;
  spawnRuntime(args: PtySpawnArgs): Promise<unknown>;
  resizeRuntime(id: string, cols: number, rows: number, reason: string): void;
  logResumeRecovery?(details: { id: string; agent?: string; state?: string }): void;
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
  const forceRespawn = args.reload === true;
  const freshCreate = args.intent === 'create';

  if (context.alreadyAttached && !forceRespawn) {
    operations.resizeRuntime(args.id, args.cols, args.rows, 'already_attached');
    return;
  }

  if (!context.runtimeKnownToDaemon) {
    operations.resizeRuntime(args.id, args.cols, args.rows, 'spawn_bootstrap');
  }

  if (!forceRespawn && !freshCreate && context.runtimeKnownToDaemon) {
    try {
      await operations.attachExistingRuntime(args, {
        policy: 'relaunch_restore',
      });
      return;
    } catch (attachError) {
      if (context.existingSession?.agent === 'claude' || context.existingSession?.state === 'recoverable') {
        const resumeArgs: PtySpawnArgs = {
          ...args,
          resume_session_id: args.id,
          resume_picker: null,
        };
        operations.logResumeRecovery?.({
          id: args.id,
          agent: context.existingSession.agent,
          state: context.existingSession.state,
        });
        try {
          await operations.spawnRuntime(resumeArgs);
        } catch (spawnError) {
          if (!isAlreadyExistsError(spawnError)) {
            throw new Error('Failed to recover session. Close it and start a new session.');
          }
        }
        await operations.attachExistingRuntime(args, {
          policy: 'relaunch_restore',
        });
        return;
      }

      throw new Error(
        'No live PTY found for this session. It likely ended when the daemon restarted. Close it and start a new session.',
      );
    }
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
