import { describe, expect, it, vi } from 'vitest';
import type { PtySpawnArgs } from './bridge';
import {
  isAlreadyExistsError,
  normalizeAttachPolicy,
  spawnPtyRuntime,
  type SpawnPtyRuntimeOperations,
} from './runtimeLifecycle';

function createSpawnArgs(overrides: Partial<PtySpawnArgs> = {}): PtySpawnArgs {
  return {
    id: 'runtime-1',
    cwd: '/tmp/repo',
    cols: 80,
    rows: 24,
    ...overrides,
  };
}

function createOperations(): SpawnPtyRuntimeOperations & {
  attachExistingRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['attachExistingRuntime']>>;
  attachFreshRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['attachFreshRuntime']>>;
  spawnRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['spawnRuntime']>>;
  resizeRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['resizeRuntime']>>;
  redrawRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['redrawRuntime']>>;
  logClaudeResumeRecovery: ReturnType<typeof vi.fn<NonNullable<SpawnPtyRuntimeOperations['logClaudeResumeRecovery']>>>;
  logKnownWorkspaceRespawn: ReturnType<typeof vi.fn<NonNullable<SpawnPtyRuntimeOperations['logKnownWorkspaceRespawn']>>>;
} {
  return {
    attachExistingRuntime: vi.fn<SpawnPtyRuntimeOperations['attachExistingRuntime']>(),
    attachFreshRuntime: vi.fn<SpawnPtyRuntimeOperations['attachFreshRuntime']>(),
    spawnRuntime: vi.fn<SpawnPtyRuntimeOperations['spawnRuntime']>(),
    resizeRuntime: vi.fn<SpawnPtyRuntimeOperations['resizeRuntime']>(),
    redrawRuntime: vi.fn<SpawnPtyRuntimeOperations['redrawRuntime']>(),
    logClaudeResumeRecovery: vi.fn<NonNullable<SpawnPtyRuntimeOperations['logClaudeResumeRecovery']>>(),
    logKnownWorkspaceRespawn: vi.fn<NonNullable<SpawnPtyRuntimeOperations['logKnownWorkspaceRespawn']>>(),
  };
}

describe('runtimeLifecycle', () => {
  it('normalizes non-relaunch attach policies to same_app_remount', () => {
    expect(normalizeAttachPolicy('relaunch_restore')).toBe('relaunch_restore');
    expect(normalizeAttachPolicy('same_app_remount')).toBe('same_app_remount');
    expect(normalizeAttachPolicy('fresh_spawn')).toBe('same_app_remount');
    expect(normalizeAttachPolicy(undefined)).toBe('same_app_remount');
  });

  it('detects already-exists spawn errors', () => {
    expect(isAlreadyExistsError(new Error('Session already exists'))).toBe(true);
    expect(isAlreadyExistsError('session ALREADY EXISTS')).toBe(true);
    expect(isAlreadyExistsError(new Error('session missing'))).toBe(false);
  });

  it('resizes and returns immediately when runtime is already attached', async () => {
    const operations = createOperations();

    await spawnPtyRuntime(
      createSpawnArgs(),
      {
        alreadyAttached: true,
        runtimeKnownToDaemon: true,
      },
      operations,
    );

    expect(operations.resizeRuntime).toHaveBeenCalledWith('runtime-1', 80, 24, 'already_attached');
    expect(operations.attachExistingRuntime).not.toHaveBeenCalled();
    expect(operations.spawnRuntime).not.toHaveBeenCalled();
    expect(operations.attachFreshRuntime).not.toHaveBeenCalled();
  });

  it('attaches daemon-known runtimes before trying to spawn them', async () => {
    const operations = createOperations();

    await spawnPtyRuntime(
      createSpawnArgs({ shell: true }),
      {
        alreadyAttached: false,
        runtimeKnownToDaemon: true,
      },
      operations,
    );

    expect(operations.attachExistingRuntime).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'runtime-1', shell: true }),
      { policy: 'relaunch_restore', forceShellRedraw: true },
    );
    expect(operations.spawnRuntime).not.toHaveBeenCalled();
    expect(operations.attachFreshRuntime).not.toHaveBeenCalled();
  });

  it('resumes Claude sessions after attach failure and re-attaches them', async () => {
    const operations = createOperations();
    operations.attachExistingRuntime
      .mockRejectedValueOnce(new Error('session not found'))
      .mockResolvedValueOnce(undefined);

    await spawnPtyRuntime(
      createSpawnArgs({ agent: 'claude' }),
      {
        alreadyAttached: false,
        runtimeKnownToDaemon: true,
        existingSession: {
          agent: 'claude',
          recoverable: true,
        },
      },
      operations,
    );

    expect(operations.logClaudeResumeRecovery).toHaveBeenCalledWith({
      id: 'runtime-1',
      recoverable: true,
    });
    expect(operations.spawnRuntime).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 'runtime-1',
        resume_session_id: 'runtime-1',
        resume_picker: null,
        fork_session: null,
      }),
    );
    expect(operations.attachExistingRuntime).toHaveBeenCalledTimes(2);
    expect(operations.attachFreshRuntime).not.toHaveBeenCalled();
  });

  it('recreates daemon-known workspace runtimes in place after attach failure', async () => {
    const operations = createOperations();
    operations.attachExistingRuntime.mockRejectedValueOnce(new Error('session not found'));

    await spawnPtyRuntime(
      createSpawnArgs({ endpoint_id: 'ep-remote', shell: true }),
      {
        alreadyAttached: false,
        runtimeKnownToDaemon: true,
      },
      operations,
    );

    expect(operations.logKnownWorkspaceRespawn).toHaveBeenCalledWith({
      id: 'runtime-1',
      endpointId: 'ep-remote',
      error: expect.any(Error),
    });
    expect(operations.spawnRuntime).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 'runtime-1',
        endpoint_id: 'ep-remote',
        shell: true,
      }),
    );
    expect(operations.attachFreshRuntime).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'runtime-1' }),
    );
  });

  it('throws a user-facing error when a daemon-known non-Claude session cannot be reattached', async () => {
    const operations = createOperations();
    operations.attachExistingRuntime.mockRejectedValueOnce(new Error('session not found'));

    await expect(
      spawnPtyRuntime(
        createSpawnArgs({ agent: 'codex' }),
        {
          alreadyAttached: false,
          runtimeKnownToDaemon: true,
          existingSession: { agent: 'codex' },
        },
        operations,
      ),
    ).rejects.toThrow('No live PTY found for this session. It likely ended when the daemon restarted. Close it and start a new session.');

    expect(operations.spawnRuntime).not.toHaveBeenCalled();
    expect(operations.attachFreshRuntime).not.toHaveBeenCalled();
  });

  it('bootstraps fresh shell spawns, tolerates already-exists races, and redraws after attach', async () => {
    const operations = createOperations();
    operations.spawnRuntime.mockRejectedValueOnce(new Error('session already exists'));

    await spawnPtyRuntime(
      createSpawnArgs({ shell: true }),
      {
        alreadyAttached: false,
        runtimeKnownToDaemon: false,
      },
      operations,
    );

    expect(operations.resizeRuntime).toHaveBeenCalledWith('runtime-1', 80, 24, 'spawn_bootstrap');
    expect(operations.attachExistingRuntime).not.toHaveBeenCalled();
    expect(operations.attachFreshRuntime).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'runtime-1', shell: true }),
    );
    expect(operations.redrawRuntime).toHaveBeenCalledWith('runtime-1', 80, 24, 'fresh_shell_attach');
  });
});
