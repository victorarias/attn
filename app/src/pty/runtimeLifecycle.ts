import type { PtyAttachArgs, PtyAttachPolicy, PtySpawnArgs } from './bridge';

export interface RuntimeLifecycleSessionSnapshot {
  agent?: string;
  recoverable?: boolean;
}

export interface ExistingRuntimeAttachOptions {
  policy: Extract<PtyAttachPolicy, 'relaunch_restore' | 'same_app_remount'>;
  forceResizeBeforeAttach?: boolean;
  forceShellRedraw?: boolean;
}

export interface SpawnPtyRuntimeContext {
  existingSession?: RuntimeLifecycleSessionSnapshot;
  runtimeKnownToDaemon: boolean;
  alreadyAttached: boolean;
}

export interface SpawnPtyRuntimeOperations {
  attachExistingRuntime(
    args: Pick<PtyAttachArgs, 'id' | 'cols' | 'rows' | 'shell' | 'reason'>,
    options: ExistingRuntimeAttachOptions,
  ): Promise<unknown>;
  attachFreshRuntime(args: PtySpawnArgs): Promise<unknown>;
  spawnRuntime(args: PtySpawnArgs): Promise<unknown>;
  resizeRuntime(id: string, cols: number, rows: number, reason: string): void;
  redrawRuntime(id: string, cols: number, rows: number, reason: string): void;
  logClaudeResumeRecovery?(details: { id: string; recoverable: boolean }): void;
  logKnownWorkspaceRespawn?(details: { id: string; endpointId?: string; error: unknown }): void;
}

export function normalizeAttachPolicy(
  policy?: PtyAttachPolicy,
): Extract<PtyAttachPolicy, 'relaunch_restore' | 'same_app_remount'> {
  return policy === 'relaunch_restore'
    ? 'relaunch_restore'
    : 'same_app_remount';
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

  if (context.alreadyAttached && !forceRespawn) {
    operations.resizeRuntime(args.id, args.cols, args.rows, 'already_attached');
    return;
  }

  if (!context.runtimeKnownToDaemon) {
    operations.resizeRuntime(args.id, args.cols, args.rows, 'spawn_bootstrap');
  }

  if (!forceRespawn && context.runtimeKnownToDaemon) {
    try {
      await operations.attachExistingRuntime(args, {
        policy: 'relaunch_restore',
        forceShellRedraw: Boolean(args.shell && !context.alreadyAttached && !context.existingSession),
      });
      return;
    } catch (attachError) {
      if (context.existingSession?.agent === 'claude') {
        const resumeArgs: PtySpawnArgs = {
          ...args,
          resume_session_id: args.id,
          resume_picker: null,
          fork_session: null,
        };
        operations.logClaudeResumeRecovery?.({
          id: args.id,
          recoverable: Boolean(context.existingSession.recoverable),
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

      if (!context.existingSession) {
        operations.logKnownWorkspaceRespawn?.({
          id: args.id,
          endpointId: args.endpoint_id,
          error: attachError,
        });
      } else {
        throw new Error(
          'No live PTY found for this session. It likely ended when the daemon restarted. Close it and start a new session.',
        );
      }
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

  if (args.shell && !context.runtimeKnownToDaemon) {
    operations.redrawRuntime(args.id, args.cols, args.rows, 'fresh_shell_attach');
  }
}
