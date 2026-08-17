// Auto mode's promoted policy and the proposals waiting on a human, read once
// for the two surfaces that need it: the settings section that resolves them,
// and the nav badge that says how many are waiting.
//
// One hook rather than a fetch inside the section, because the badge has to
// know the count before the section mounts. `enabled` is what keeps a closed
// panel holding nothing — no read, no state, no timer.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { AutoModePromotion, AutoModeState } from './daemonAutoModeEvents';

export interface AutoModePolicy {
  state: AutoModeState | null;
  /** The last failure, kept beside a good snapshot rather than replacing it. */
  error: string | null;
  loading: boolean;
  /** The proposal a promote or discard is in flight for. */
  resolvingID: number | null;
  /** How many proposals are waiting on a human. */
  pendingCount: number;
  refresh: () => Promise<void>;
  promote: (id: number) => Promise<void>;
  discard: (id: number) => Promise<void>;
}

interface AutoModePolicyOptions {
  /** False while the surface is closed: nothing is read and nothing is held. */
  enabled: boolean;
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
}

const message = (err: unknown, fallback: string): string =>
  err instanceof Error ? err.message : fallback;

export function useAutoModePolicy(options: AutoModePolicyOptions): AutoModePolicy {
  const { enabled, getState, promoteProposal, discardProposal } = options;
  const [state, setState] = useState<AutoModeState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [resolvingID, setResolvingID] = useState<number | null>(null);
  // Guards against an older answer landing after a newer one.
  const seqRef = useRef(0);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      const next = await getState();
      if (seqRef.current !== seq) return;
      setState(next);
      setError(null);
    } catch (err) {
      if (seqRef.current !== seq) return;
      setError(message(err, 'Could not read auto mode'));
    } finally {
      if (seqRef.current === seq) setLoading(false);
    }
  }, [getState]);

  const resolve = useCallback(async (
    id: number,
    action: (id: number) => Promise<AutoModePromotion>,
    fallback: string,
  ) => {
    setResolvingID(id);
    try {
      await action(id);
      // The daemon is authoritative: the list reflects a re-read, never a
      // local guess about what promoting did to the config.
      await refresh();
    } catch (err) {
      setError(message(err, fallback));
    } finally {
      setResolvingID(null);
    }
  }, [refresh]);

  const promote = useCallback(
    (id: number) => resolve(id, promoteProposal, 'Could not promote the proposal'),
    [resolve, promoteProposal],
  );
  const discard = useCallback(
    (id: number) => resolve(id, discardProposal, 'Could not discard the proposal'),
    [resolve, discardProposal],
  );

  useEffect(() => {
    if (!enabled) {
      // Drop the snapshot on close: reopening reads a fresh one, and a stale
      // proposal list is worse than none.
      seqRef.current++;
      setState(null);
      setError(null);
      setLoading(false);
      return;
    }
    void refresh();
  }, [enabled, refresh]);

  return {
    state,
    error,
    loading,
    resolvingID,
    pendingCount: state?.proposals.length ?? 0,
    refresh,
    promote,
    discard,
  };
}
