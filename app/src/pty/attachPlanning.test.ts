import { describe, expect, it } from 'vitest';
import {
  classifyAttachReplay,
  createAttachRequestContext,
  enqueuePendingAttachOutput,
  planAttachResultEffects,
  planAttachedRuntimeGeometry,
  planLivePtyOutput,
} from './attachPlanning';

describe('attachPlanning', () => {
  it('applies relaunch replay at the daemon-owned attached geometry', () => {
    const plan = classifyAttachReplay({
      cols: 31,
      rows: 25,
      screen_cols: 31,
      screen_rows: 25,
      screen_snapshot: 'match',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
    expect(plan.attachedGeometryMismatch).toBe(true);
    expect(plan.replayAllowedByPolicy).toBe(true);
    expect(plan.respondToTerminalQueries).toBe(false);
  });

  it('prefers raw scrollback over screen snapshot for Codex relaunch restores', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
      scrollback: 'raw',
    }, createAttachRequestContext({ cols: 58, rows: 46, agent: 'codex' }, 'relaunch_restore'));

    expect(plan.replayKind).toBe('scrollback');
    expect(plan.replayApplied).toBe(true);
    expect(plan.availableScreenSnapshot).toBe(true);
    expect(plan.availableRawScrollback).toBe(true);
    expect(plan.screenSnapshotSuppressed).toBe(true);
    expect(plan.replayPreference).toBe('prefer_raw_scrollback');
  });

  it('keeps screen snapshot replay for Codex relaunch restores when raw scrollback is unavailable', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46, agent: 'codex' }, 'relaunch_restore'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.replayApplied).toBe(true);
    expect(plan.availableScreenSnapshot).toBe(true);
    expect(plan.availableRawScrollback).toBe(false);
    expect(plan.screenSnapshotSuppressed).toBe(false);
    expect(plan.replayPreference).toBe('prefer_raw_scrollback');
  });

  it('treats segmented raw replay as available raw replay for Codex relaunch restores', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      replay_segments: [
        { cols: 118, rows: 48, data: 'wide' },
        { cols: 58, rows: 46, data: 'narrow' },
      ],
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46, agent: 'codex' }, 'relaunch_restore'));

    expect(plan.replayKind).toBe('scrollback');
    expect(plan.replayApplied).toBe(true);
    expect(plan.availableRawScrollback).toBe(true);
    expect(plan.screenSnapshotSuppressed).toBe(true);
  });

  it('keeps screen snapshot replay for Claude relaunch restores', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
      scrollback: 'raw',
    }, createAttachRequestContext({ cols: 58, rows: 46, agent: 'claude' }, 'relaunch_restore'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.replayApplied).toBe(true);
    expect(plan.availableScreenSnapshot).toBe(true);
    expect(plan.availableRawScrollback).toBe(true);
    expect(plan.screenSnapshotSuppressed).toBe(false);
    expect(plan.replayPreference).toBe('default');
  });

  it('applies replay for same-app remount because the terminal model is new', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'match',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'same_app_remount'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
    expect(plan.replayAllowedByPolicy).toBe(true);
    expect(plan.respondToTerminalQueries).toBe(false);
  });

  it('applies raw scrollback for Codex fresh_spawn so the terminal can answer capability queries', () => {
    const plan = classifyAttachReplay({
      cols: 68,
      rows: 35,
      scrollback: 'raw',
    }, createAttachRequestContext({ cols: 68, rows: 35, agent: 'codex' }, 'fresh_spawn'));

    expect(plan.replayKind).toBe('scrollback');
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
    expect(plan.replayAllowedByPolicy).toBe(true);
    expect(plan.respondToTerminalQueries).toBe(true);
  });

  it('applies segmented raw replay for Codex same_app_remount', () => {
    const plan = classifyAttachReplay({
      cols: 68,
      rows: 35,
      replay_segments: [{ cols: 68, rows: 35, data: 'codex-startup' }],
    }, createAttachRequestContext({ cols: 68, rows: 35, agent: 'codex' }, 'same_app_remount'));

    expect(plan.replayKind).toBe('scrollback');
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
    expect(plan.replayAllowedByPolicy).toBe(true);
  });

  it('prefers raw scrollback for shell same-app remounts', () => {
    const plan = classifyAttachReplay({
      cols: 80,
      rows: 24,
      screen_cols: 80,
      screen_rows: 24,
      screen_snapshot: 'prompt-only',
      screen_snapshot_fresh: true,
      scrollback: 'shell-history',
    }, createAttachRequestContext({ cols: 80, rows: 24, agent: 'shell' }, 'same_app_remount'));

    expect(plan.replayKind).toBe('scrollback');
    expect(plan.screenSnapshotSuppressed).toBe(true);
    expect(plan.replayApplied).toBe(true);
    expect(plan.respondToTerminalQueries).toBe(false);
  });

  it('applies raw scrollback for same-app remounts even when the pane expands after a split closes', () => {
    const plan = classifyAttachReplay({
      cols: 31,
      rows: 12,
      replay_segments: [{ cols: 31, rows: 12, data: 'shell-history' }],
    }, createAttachRequestContext({ cols: 31, rows: 25, agent: 'shell' }, 'same_app_remount'));

    expect(plan.replayKind).toBe('scrollback');
    expect(plan.attachedGeometryMismatch).toBe(true);
    expect(plan.replayGeometryMismatch).toBe(true);
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
  });

  it('still skips fresh_spawn replay for non-Codex agents', () => {
    const plan = classifyAttachReplay({
      cols: 68,
      rows: 35,
      scrollback: 'raw',
    }, createAttachRequestContext({ cols: 68, rows: 35, agent: 'claude' }, 'fresh_spawn'));

    expect(plan.replaySkipped).toBe(true);
    expect(plan.replayApplied).toBe(false);
    expect(plan.replayAllowedByPolicy).toBe(false);
  });

  it('does not request PTY reconcile work for same-app remount attaches at matching geometry', () => {
    const plan = planAttachedRuntimeGeometry({
      cols: 58,
      rows: 46,
      shell: false,
    }, {
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'match',
    }, {
      attachPolicy: 'same_app_remount',
    });

    expect(plan.resizeRequired).toBe(false);
    expect(plan.strategy).toBe('none');
  });

  it('preserves daemon geometry during relaunch restore rather than resizing from bootstrap layout', () => {
    const attachContext = createAttachRequestContext({
      cols: 58,
      rows: 46,
      agent: 'claude',
    }, 'relaunch_restore');
    const plan = planAttachedRuntimeGeometry({
      cols: 58,
      rows: 46,
      shell: false,
    }, {
      cols: 37,
      rows: 46,
      screen_cols: 37,
      screen_rows: 46,
      screen_snapshot: 'stale',
    }, {
      attachPolicy: 'relaunch_restore',
      attachContext,
    });

    expect(plan.resizeRequired).toBe(false);
    expect(plan.strategy).toBe('preserve_attached');
    expect(plan.ptyGeometryMatches).toBe(false);
    expect(plan.replayGeometryMatches).toBe(false);
  });

  it('does not request PTY reconcile work for raw scrollback relaunches that already match geometry', () => {
    const plan = planAttachedRuntimeGeometry({
      cols: 58,
      rows: 46,
      shell: false,
    }, {
      cols: 58,
      rows: 46,
      scrollback: 'raw',
    }, {
      attachPolicy: 'relaunch_restore',
    });

    expect(plan.resizeRequired).toBe(false);
    expect(plan.strategy).toBe('none');
  });

  it('does not request PTY reconcile work for screen-snapshot relaunches that already match geometry', () => {
    const attachContext = createAttachRequestContext({
      cols: 58,
      rows: 46,
      agent: 'claude',
    }, 'relaunch_restore');
    const plan = planAttachedRuntimeGeometry({
      cols: 58,
      rows: 46,
      shell: false,
      agent: 'claude',
    }, {
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
    }, {
      attachPolicy: 'relaunch_restore',
      attachContext,
    });

    expect(plan.hasScreenSnapshotReplay).toBe(true);
    expect(plan.resizeRequired).toBe(false);
    expect(plan.strategy).toBe('none');
  });

  it('does not request redraw when Codex relaunch replay suppresses a matching snapshot in favor of raw scrollback', () => {
    const attachContext = createAttachRequestContext({
      cols: 58,
      rows: 46,
      agent: 'codex',
    }, 'relaunch_restore');
    const plan = planAttachedRuntimeGeometry({
      cols: 58,
      rows: 46,
      shell: false,
      agent: 'codex',
    }, {
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
      scrollback: 'raw',
    }, {
      attachPolicy: 'relaunch_restore',
      attachContext,
    });

    expect(plan.hasScreenSnapshotReplay).toBe(false);
    expect(plan.hasRawScrollbackReplay).toBe(true);
    expect(plan.screenSnapshotSuppressed).toBe(true);
    expect(plan.resizeRequired).toBe(false);
    expect(plan.strategy).toBe('none');
  });

  it('plans reset and snapshot replay for relaunch restores', () => {
    const replayPlan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'snapshot',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

    const effects = planAttachResultEffects({
      attachResult: {
        last_seq: 5,
        screen_snapshot: 'snapshot',
      },
      replayPlan,
      previousSeq: 4,
    });

    expect(effects.shouldReset).toBe(true);
    expect(effects.resetReason).toBe('snapshot_restore');
    expect(effects.replayAction).toEqual({
      kind: 'screen_snapshot',
      replayKind: 'screen_snapshot',
      data: 'snapshot',
    });
    expect(effects.nextSeq).toBe(5);
  });

  it('filters queued output older than the attach sequence watermark', () => {
    const replayPlan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      scrollback: 'raw',
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

    const effects = planAttachResultEffects({
      attachResult: {
        last_seq: 10,
        scrollback: 'raw',
      },
      replayPlan,
      queuedOutputs: [
        { data: 'old', seq: 9 },
        { data: 'equal', seq: 10 },
        { data: 'new', seq: 11 },
        { data: 'noseq' },
      ],
    });

    expect(effects.replayAction.kind).toBe('scrollback');
    expect(effects.queuedOutputsToEmit).toEqual([
      { data: 'equal', seq: 10 },
      { data: 'new', seq: 11 },
      { data: 'noseq' },
    ]);
    expect(effects.nextSeq).toBe(11);
  });

  it('plans segmented raw replay when replay segments are present', () => {
    const replayPlan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      replay_segments: [
        { cols: 118, rows: 48, data: 'wide' },
        { cols: 58, rows: 46, data: 'narrow' },
      ],
    }, createAttachRequestContext({ cols: 58, rows: 46, agent: 'codex' }, 'relaunch_restore'));

    const effects = planAttachResultEffects({
      attachResult: {
        last_seq: 97,
        replay_segments: [
          { cols: 118, rows: 48, data: 'wide' },
          { cols: 58, rows: 46, data: 'narrow' },
        ],
      },
      replayPlan,
      previousSeq: 96,
    });

    expect(effects.replayAction).toEqual({
      kind: 'scrollback_segments',
      replayKind: 'scrollback',
      segments: [
        { cols: 118, rows: 48, data: 'wide' },
        { cols: 58, rows: 46, data: 'narrow' },
      ],
    });
  });

  it('warns on truncated raw Codex restore without a screen snapshot', () => {
    const replayPlan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      scrollback: 'raw',
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

    const effects = planAttachResultEffects({
      attachResult: {
        scrollback: 'raw',
        scrollback_truncated: true,
      },
      replayPlan,
      sessionAgent: 'codex',
    });

    expect(effects.shouldWarnTruncatedRestore).toBe(true);
  });

  it('bounds queued attach output by dropping the oldest chunk first', () => {
    const queued = enqueuePendingAttachOutput(
      [
        { data: 'one', seq: 1 },
        { data: 'two', seq: 2 },
      ],
      { data: 'three', seq: 3 },
      2,
    );

    expect(queued).toEqual([
      { data: 'two', seq: 2 },
      { data: 'three', seq: 3 },
    ]);
  });

  it('drops live PTY output whose sequence does not advance past the last applied chunk', () => {
    expect(planLivePtyOutput({ incomingSeq: 10, lastSeq: 10 })).toEqual({
      shouldDropAsStale: true,
      nextSeq: 10,
    });
    expect(planLivePtyOutput({ incomingSeq: 9, lastSeq: 10 })).toEqual({
      shouldDropAsStale: true,
      nextSeq: 10,
    });
    expect(planLivePtyOutput({ incomingSeq: 11, lastSeq: 10 })).toEqual({
      shouldDropAsStale: false,
      nextSeq: 11,
    });
  });

  it('keeps seq-less live PTY output because it cannot be proven stale', () => {
    expect(planLivePtyOutput({ lastSeq: 10 })).toEqual({
      shouldDropAsStale: false,
      nextSeq: 10,
    });
  });
});
