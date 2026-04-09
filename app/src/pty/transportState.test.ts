import { describe, expect, it } from 'vitest';
import { createPtyTransportState } from './transportState';

describe('transportState', () => {
  it('clears one runtime without disturbing others', () => {
    const state = createPtyTransportState<{ policy: string }>();
    state.markRuntimeAttached('a');
    state.markRuntimeAttached('b');
    state.setLastSeq('a', 3);
    state.setLastSeq('b', 7);
    state.setQueuedAttachOutputs('a', [{ data: 'a', seq: 3 }]);
    state.setAttachContext('a', { policy: 'relaunch_restore' });

    state.clearRuntime('a');

    expect(state.hasAttachedRuntime('a')).toBe(false);
    expect(state.getLastSeq('a')).toBeUndefined();
    expect(state.getQueuedAttachOutputs('a')).toBeUndefined();
    expect(state.getAttachContext('a')).toBeUndefined();
    expect(state.hasAttachedRuntime('b')).toBe(true);
    expect(state.getLastSeq('b')).toBe(7);
  });

  it('prunes detached runtimes but keeps attachable ones', () => {
    const state = createPtyTransportState<never>();
    state.markRuntimeAttached('keep');
    state.markRuntimeAttached('drop');
    state.setLastSeq('keep', 1);
    state.setLastSeq('drop', 2);
    state.setQueuedAttachOutputs('drop', [{ data: 'drop', seq: 2 }]);

    state.pruneDetachedRuntimes(new Set(['keep']));

    expect(state.listAttachedRuntimeIds()).toEqual(['keep']);
    expect(state.getLastSeq('keep')).toBe(1);
    expect(state.getLastSeq('drop')).toBeUndefined();
    expect(state.getQueuedAttachOutputs('drop')).toBeUndefined();
  });

  it('clears stream caches while preserving attachment ownership', () => {
    const state = createPtyTransportState<{ policy: string }>();
    state.markRuntimeAttached('runtime-1');
    state.setLastSeq('runtime-1', 9);
    state.setQueuedAttachOutputs('runtime-1', [{ data: 'chunk', seq: 9 }]);
    state.setAttachContext('runtime-1', { policy: 'relaunch_restore' });

    state.clearStreamCaches();

    expect(state.hasAttachedRuntime('runtime-1')).toBe(true);
    expect(state.getLastSeq('runtime-1')).toBeUndefined();
    expect(state.getQueuedAttachOutputs('runtime-1')).toBeUndefined();
    expect(state.getAttachContext('runtime-1')).toEqual({ policy: 'relaunch_restore' });
  });

  it('clears only stream state for desync recovery', () => {
    const state = createPtyTransportState<{ policy: string }>();
    state.markRuntimeAttached('runtime-1');
    state.setLastSeq('runtime-1', 11);
    state.setQueuedAttachOutputs('runtime-1', [{ data: 'chunk', seq: 11 }]);
    state.setAttachContext('runtime-1', { policy: 'same_app_remount' });

    state.clearRuntimeStream('runtime-1');

    expect(state.hasAttachedRuntime('runtime-1')).toBe(true);
    expect(state.getLastSeq('runtime-1')).toBeUndefined();
    expect(state.getQueuedAttachOutputs('runtime-1')).toBeUndefined();
    expect(state.getAttachContext('runtime-1')).toEqual({ policy: 'same_app_remount' });
  });
});
