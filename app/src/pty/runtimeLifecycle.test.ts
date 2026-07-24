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
    workspace_id: 'workspace-runtime-1',
    cols: 80,
    rows: 24,
    ...overrides,
  };
}

function createOperations(): SpawnPtyRuntimeOperations & {
  attachFreshRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['attachFreshRuntime']>>;
  spawnRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['spawnRuntime']>>;
  resizeRuntime: ReturnType<typeof vi.fn<SpawnPtyRuntimeOperations['resizeRuntime']>>;
} {
  return {
    attachFreshRuntime: vi.fn<SpawnPtyRuntimeOperations['attachFreshRuntime']>(),
    spawnRuntime: vi.fn<SpawnPtyRuntimeOperations['spawnRuntime']>(),
    resizeRuntime: vi.fn<SpawnPtyRuntimeOperations['resizeRuntime']>(),
  };
}

describe('runtimeLifecycle', () => {
  it('normalizes non-relaunch attach policies to same_app_remount', () => {
    expect(normalizeAttachPolicy('relaunch_restore')).toBe('relaunch_restore');
    expect(normalizeAttachPolicy('same_app_remount')).toBe('same_app_remount');
    expect(normalizeAttachPolicy('revive')).toBe('revive');
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
    expect(operations.spawnRuntime).not.toHaveBeenCalled();
    expect(operations.attachFreshRuntime).not.toHaveBeenCalled();
  });

  it('bootstraps fresh spawns and attaches after an already-exists race', async () => {
    const operations = createOperations();
    operations.spawnRuntime.mockRejectedValueOnce(new Error('session already exists'));

    await spawnPtyRuntime(
      createSpawnArgs({ intent: 'create', shell: true }),
      {
        alreadyAttached: false,
        runtimeKnownToDaemon: false,
      },
      operations,
    );

    expect(operations.resizeRuntime).toHaveBeenCalledWith('runtime-1', 80, 24, 'spawn_bootstrap');
    expect(operations.attachFreshRuntime).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'runtime-1', shell: true }),
    );
  });
});
