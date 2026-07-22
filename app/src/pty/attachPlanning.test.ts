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

  it('applies a same-app snapshot at its recorded geometry when the mounted pane size differs', () => {
    const plan = classifyAttachReplay({
      cols: 56,
      rows: 35,
      screen_cols: 56,
      screen_rows: 35,
      screen_snapshot: 'fresh-daemon-frame',
      screen_snapshot_fresh: true,
    }, createAttachRequestContext({ cols: 52, rows: 35, agent: 'codex' }, 'same_app_remount'));

    expect(plan.replayKind).toBe('screen_snapshot');
    expect(plan.attachedGeometryMismatch).toBe(true);
    expect(plan.replayGeometryMismatch).toBe(true);
    expect(plan.replayApplied).toBe(true);
    expect(plan.replaySkipped).toBe(false);
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

  // A pane mounted while its session is inactive attaches with the model's
  // construction default (e.g. 80x24). Forcing the live PTY to that size
  // SIGWINCH-churns the shell and width-bounces every attached model,
  // invalidating freshly replayed command blocks.
  it('preserves daemon geometry when same-app requested geometry was not measured', () => {
    const plan = planAttachedRuntimeGeometry({
      cols: 80,
      rows: 24,
      shell: false,
    }, {
      cols: 45,
      rows: 35,
    }, {
      attachPolicy: 'same_app_remount',
      requestedGeometryAuthoritative: false,
    });

    expect(plan.resizeRequired).toBe(false);
    expect(plan.strategy).toBe('preserve_attached');
  });

  it('reconciles an explicitly measured same-app remount geometry', () => {
    const plan = planAttachedRuntimeGeometry({
      cols: 58,
      rows: 46,
      shell: false,
    }, {
      cols: 80,
      rows: 24,
    }, {
      attachPolicy: 'same_app_remount',
      requestedGeometryAuthoritative: true,
    });

    expect(plan.resizeRequired).toBe(true);
    expect(plan.strategy).toBe('resize');
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

  it('filters queued output already covered by the attach replay payload', () => {
    // last_seq names the LAST chunk inside the replay payload, so a queued
    // chunk with seq == last_seq is a duplicate of replayed bytes and must be
    // skipped; the first genuinely-live chunk is last_seq + 1.
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

  it('does not warn for daemon-verified segmented replay when recovery context is absent', () => {
    const replayPlan = classifyAttachReplay({
      cols: 58,
      rows: 46,
      replay_segments: [
        { cols: 58, rows: 46, data: 'verified-tail' },
      ],
    });

    const effects = planAttachResultEffects({
      attachResult: {
        replay_segments: [
          { cols: 58, rows: 46, data: 'verified-tail' },
        ],
        scrollback_truncated: true,
      },
      replayPlan,
      sessionAgent: 'codex',
    });

    expect(effects.replayAction.kind).toBe('skipped');
    expect(effects.shouldWarnTruncatedRestore).toBe(false);
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

  describe('server-authoritative Ghostty snapshot (Phase 2)', () => {
    it('classifies a Ghostty snapshot as the authoritative restore payload', () => {
      const plan = classifyAttachReplay({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasGhosttySnapshot).toBe(true);
      expect(plan.replayKind).toBe('ghostty_snapshot');
      expect(plan.hasReplayPayload).toBe(true);
      expect(plan.replayApplied).toBe(true);
      expect(plan.replaySkipped).toBe(false);
      expect(plan.replayAllowedByPolicy).toBe(true);
      expect(plan.replayCols).toBe(80);
      expect(plan.replayRows).toBe(24);
      // The dump is written suppressed; the worker already answered queries.
      expect(plan.respondToTerminalQueries).toBe(false);
      // Snapshot supersedes the legacy screen-snapshot mechanism.
      expect(plan.hasScreenSnapshot).toBe(false);
    });

    it('applies the Ghostty snapshot even when its grid differs from the requested geometry', () => {
      // Unlike a screen snapshot, a geometry mismatch never skips it: the
      // consumer resizes to the snapshot grid before writing.
      const plan = classifyAttachReplay({
        cols: 100,
        rows: 40,
        snapshot: { cols: 100, rows: 40, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 58, rows: 46 }, 'same_app_remount'));

      expect(plan.replayApplied).toBe(true);
      expect(plan.replaySkipped).toBe(false);
      expect(plan.replayGeometryMismatch).toBe(true);
      expect(plan.replayCols).toBe(100);
      expect(plan.replayRows).toBe(40);
    });

    it('supersedes raw scrollback and screen snapshot when all are present', () => {
      const plan = classifyAttachReplay({
        cols: 80,
        rows: 24,
        screen_cols: 80,
        screen_rows: 24,
        screen_snapshot: 'legacy',
        screen_snapshot_fresh: true,
        scrollback: 'raw',
        snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 80, rows: 24, agent: 'codex' }, 'relaunch_restore'));

      expect(plan.replayKind).toBe('ghostty_snapshot');
      expect(plan.hasScreenSnapshot).toBe(false);
      expect(plan.screenSnapshotSuppressed).toBe(false);
    });

    it('ignores an empty Ghostty snapshot and falls back to the legacy path', () => {
      const plan = classifyAttachReplay({
        cols: 80,
        rows: 24,
        screen_cols: 80,
        screen_rows: 24,
        screen_snapshot: 'legacy',
        screen_snapshot_fresh: true,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: '' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasGhosttySnapshot).toBe(false);
      expect(plan.replayKind).toBe('screen_snapshot');
    });

    it('plans a snapshot_restore reset and emits the vt dump as the replay action', () => {
      const replayPlan = classifyAttachReplay({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 7,
          snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
        },
        replayPlan,
        previousSeq: 6,
      });

      expect(effects.shouldReset).toBe(true);
      expect(effects.resetReason).toBe('snapshot_restore');
      expect(effects.replayAction).toEqual({
        kind: 'ghostty_snapshot',
        replayKind: 'ghostty_snapshot',
        data: 'ZHVtcA==',
      });
      expect(effects.nextSeq).toBe(7);
      // The snapshot carries its own truncation flag; the legacy codex
      // raw-replay truncation warning must not fire.
      expect(effects.shouldWarnTruncatedRestore).toBe(false);
    });

    it('reports the snapshot grid as the authoritative replay geometry', () => {
      const attachContext = createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount');
      const plan = planAttachedRuntimeGeometry({
        cols: 80,
        rows: 24,
      }, {
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
      }, {
        attachPolicy: 'same_app_remount',
        attachContext,
      });

      expect(plan.hasGhosttySnapshotReplay).toBe(true);
      expect(plan.replayApplied).toBe(true);
      expect(plan.replayGeometryMatches).toBe(true);
      expect(plan.replayKind).toBe('ghostty_snapshot');
    });
  });
});
