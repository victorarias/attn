import { describe, expect, it } from 'vitest';
import {
  classifyAttachRestore,
  createAttachRequestContext,
  enqueuePendingAttachOutput,
  planAttachResultEffects,
  planAttachedRuntimeGeometry,
  planLivePtyOutput,
} from './attachPlanning';

describe('attachPlanning', () => {
  describe('server-authoritative Ghostty snapshot', () => {
    it('classifies a Ghostty snapshot as the authoritative restore payload', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasSnapshot).toBe(true);
      expect(plan.restoreCols).toBe(80);
      expect(plan.restoreRows).toBe(24);
    });

    it('restores at the snapshot grid even when it differs from the requested geometry', () => {
      // A geometry mismatch never skips the snapshot: the consumer resizes to
      // the snapshot grid before writing the dump.
      const plan = classifyAttachRestore({
        cols: 100,
        rows: 40,
        snapshot: { cols: 100, rows: 40, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 58, rows: 46 }, 'same_app_remount'));

      expect(plan.hasSnapshot).toBe(true);
      expect(plan.restoreCols).toBe(100);
      expect(plan.restoreRows).toBe(40);
    });

    it('treats an empty Ghostty snapshot as no restore payload', () => {
      // The pure-Go stub (non-macOS) and a ghostty construction failure both
      // surface as an empty vt dump: nothing to restore.
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: '' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasSnapshot).toBe(false);
    });

    it('plans a snapshot_restore reset and emits the vt dump as the restore action', () => {
      const restorePlan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 7,
          snapshot: { cols: 80, rows: 24, vt_dump_b64: 'ZHVtcA==' },
        },
        restorePlan,
        previousSeq: 6,
      });

      expect(effects.shouldReset).toBe(true);
      expect(effects.resetReason).toBe('snapshot_restore');
      expect(effects.restoreAction).toEqual({
        kind: 'ghostty_snapshot',
        data: 'ZHVtcA==',
      });
      expect(effects.nextSeq).toBe(7);
    });

    it('reports the snapshot grid as the authoritative restore geometry', () => {
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

      expect(plan.hasSnapshot).toBe(true);
      expect(plan.restoreGeometryMatches).toBe(true);
    });
  });

  describe('snapshot-less reattach', () => {
    it('keeps client state and its own watermark on a snapshot-less restore reattach', () => {
      // No snapshot (stub build / ghostty construction failure): there is no
      // restore payload. Per AGENTS.md the client keeps whatever it has rendered
      // and dedups the live stream against its OWN last_seq. It must not reset
      // the model (nothing would repaint it) nor jump the watermark to the
      // server's last_seq, which would drop the unrendered chunk between them.
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasSnapshot).toBe(false);

      const effects = planAttachResultEffects({
        attachResult: { last_seq: 12 },
        restorePlan: plan,
        previousSeq: 11,
        queuedOutputs: [{ data: 'live-12', seq: 12 }],
      });

      expect(effects.shouldReset).toBe(false);
      expect(effects.resetReason).toBe(null);
      expect(effects.restoreAction.kind).toBe('none');
      // Baseline is the client watermark (11), so seq 12 flows through instead
      // of being dropped as "already in the dump" — there is no dump.
      expect(effects.queuedOutputsToEmit).toEqual([{ data: 'live-12', seq: 12 }]);
      expect(effects.nextSeq).toBe(12);
    });

    it('does not reset or drop queued output on a snapshot-less same-app remount', () => {
      // Regression (PR #642 review): ghostty construction failed on the worker,
      // so the attach carries no snapshot. The client's existing model is the
      // ONLY rendered terminal and the queued chunks (emitted while briefly
      // detached) were never painted into it. Resetting would blank the idle
      // shell forever; advancing the watermark to the server's last_seq would
      // silently discard those queued chunks. Neither may happen.
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount'));

      expect(plan.hasSnapshot).toBe(false);

      const effects = planAttachResultEffects({
        attachResult: { last_seq: 20 },
        restorePlan: plan,
        previousSeq: 15,
        queuedOutputs: [
          { data: 'queued-16', seq: 16 },
          { data: 'queued-20', seq: 20 },
        ],
      });

      expect(effects.shouldReset).toBe(false);
      expect(effects.resetReason).toBe(null);
      expect(effects.restoreAction.kind).toBe('none');
      // Dedup against the client's own watermark (15), NOT the server's
      // last_seq (20): both queued chunks are past what the client rendered.
      expect(effects.queuedOutputsToEmit).toEqual([
        { data: 'queued-16', seq: 16 },
        { data: 'queued-20', seq: 20 },
      ]);
      expect(effects.nextSeq).toBe(20);
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
    // invalidating a freshly restored grid.
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
      expect(plan.restoreGeometryMatches).toBe(false);
    });
  });

  describe('sequence dedup', () => {
    it('filters queued output already covered by the attach restore payload', () => {
      // last_seq names the LAST chunk inside the restore payload, so a queued
      // chunk with seq == last_seq is a duplicate of restored bytes and must be
      // skipped; the first genuinely-live chunk is last_seq + 1.
      const restorePlan = classifyAttachRestore({
        cols: 58,
        rows: 46,
        snapshot: { cols: 58, rows: 46, vt_dump_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 10,
          snapshot: { cols: 58, rows: 46, vt_dump_b64: 'ZHVtcA==' },
        },
        restorePlan,
        queuedOutputs: [
          { data: 'old', seq: 9 },
          { data: 'equal', seq: 10 },
          { data: 'new', seq: 11 },
          { data: 'noseq' },
        ],
      });

      expect(effects.restoreAction.kind).toBe('ghostty_snapshot');
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
