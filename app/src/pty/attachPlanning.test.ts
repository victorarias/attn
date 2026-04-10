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
  it('applies relaunch replay only when policy and geometry are compatible', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'match',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
    expect(plan.replayAllowedByPolicy).toBe(true);
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

  it('skips replay for same-app remount even when payload geometry matches', () => {
    const plan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      screen_cols: 58,
      screen_rows: 46,
      screen_snapshot: 'match',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 58, rows: 46 }, 'same_app_remount'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.replayApplied).toBe(false);
    expect(plan.replaySkipped).toBe(true);
    expect(plan.replayAllowedByPolicy).toBe(false);
  });

  it('requests redraw for same-app remount non-shell attaches at matching geometry', () => {
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
    expect(plan.redrawRequired).toBe(true);
    expect(plan.strategy).toBe('redraw');
  });

  it('requests resize without treating skipped stale snapshot replay as an active redraw source', () => {
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

    expect(plan.resizeRequired).toBe(true);
    expect(plan.redrawRequired).toBe(false);
    expect(plan.strategy).toBe('resize');
    expect(plan.ptyGeometryMatches).toBe(false);
    expect(plan.replayGeometryMatches).toBe(false);
  });

  it('does not request redraw for raw scrollback relaunches that already match geometry', () => {
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
    expect(plan.redrawRequired).toBe(false);
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
    expect(plan.redrawRequired).toBe(false);
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
