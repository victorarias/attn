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
  describe('server-authoritative Ghostty snapshot', () => {
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
    });

    it('applies the Ghostty snapshot even when its grid differs from the requested geometry', () => {
      // A geometry mismatch never skips it: the consumer resizes to the
      // snapshot grid before writing the dump.
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

    it('treats an empty Ghostty snapshot as no restore payload', () => {
      // The pure-Go stub (non-macOS) and a ghostty construction failure both
      // surface as an empty vt dump: nothing to restore.
      const plan = classifyAttachReplay({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: '' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasGhosttySnapshot).toBe(false);
      expect(plan.hasReplayPayload).toBe(false);
      expect(plan.replayKind).toBe('none');
      expect(plan.replayApplied).toBe(false);
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

  describe('snapshot-less reattach', () => {
    it('allows a reset for restore policies so live output dedups against last_seq', () => {
      // No snapshot (stub build / no serialization): there is no replay payload,
      // but a restore-policy reattach still resets and resumes at last_seq.
      const plan = classifyAttachReplay({
        cols: 80,
        rows: 24,
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasReplayPayload).toBe(false);
      expect(plan.replayApplied).toBe(false);
      expect(plan.replayAllowedByPolicy).toBe(true);

      const effects = planAttachResultEffects({
        attachResult: { last_seq: 12 },
        replayPlan: plan,
        previousSeq: 11,
      });

      expect(effects.shouldReset).toBe(true);
      expect(effects.resetReason).toBe('reattach');
      expect(effects.replayAction.kind).toBe('none');
      expect(effects.nextSeq).toBe(12);
    });

    it('does not restore a fresh spawn with no snapshot', () => {
      const plan = classifyAttachReplay({
        cols: 68,
        rows: 35,
      }, createAttachRequestContext({ cols: 68, rows: 35, agent: 'codex' }, 'fresh_spawn'));

      expect(plan.hasReplayPayload).toBe(false);
      expect(plan.replayApplied).toBe(false);
      expect(plan.replayAllowedByPolicy).toBe(false);
    });
  });

  describe('attached runtime geometry', () => {
    it('does not request PTY reconcile work for same-app remounts at matching geometry', () => {
      const plan = planAttachedRuntimeGeometry({
        cols: 58,
        rows: 46,
        shell: false,
      }, {
        cols: 58,
        rows: 46,
      }, {
        attachPolicy: 'same_app_remount',
      });

      expect(plan.resizeRequired).toBe(false);
      expect(plan.strategy).toBe('none');
    });

    // A pane mounted while its session is inactive attaches with the model's
    // construction default (e.g. 80x24). Forcing the live PTY to that size
    // SIGWINCH-churns the shell and width-bounces every attached model,
    // invalidating a freshly replayed grid.
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
      }, {
        attachPolicy: 'relaunch_restore',
        attachContext,
      });

      expect(plan.resizeRequired).toBe(false);
      expect(plan.strategy).toBe('preserve_attached');
      expect(plan.ptyGeometryMatches).toBe(false);
      expect(plan.replayGeometryMatches).toBe(false);
    });
  });

  describe('sequence dedup', () => {
    it('filters queued output already covered by the attach replay payload', () => {
      // last_seq names the LAST chunk inside the replay payload, so a queued
      // chunk with seq == last_seq is a duplicate of replayed bytes and must be
      // skipped; the first genuinely-live chunk is last_seq + 1.
      const replayPlan = classifyAttachReplay({
        cols: 58,
        rows: 46,
        snapshot: { cols: 58, rows: 46, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 10,
          snapshot: { cols: 58, rows: 46, vt_dump_b64: 'ZHVtcA==' },
        },
        replayPlan,
        queuedOutputs: [
          { data: 'old', seq: 9 },
          { data: 'equal', seq: 10 },
          { data: 'new', seq: 11 },
          { data: 'noseq' },
        ],
      });

      expect(effects.replayAction.kind).toBe('ghostty_snapshot');
      expect(effects.queuedOutputsToEmit).toEqual([
        { data: 'new', seq: 11 },
        { data: 'noseq' },
      ]);
      expect(effects.nextSeq).toBe(11);
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
});
