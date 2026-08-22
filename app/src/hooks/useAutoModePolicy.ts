import { useCallback, useEffect, useRef, useState } from 'react';
import type { AutoModePatternEdit, AutoModePromotion, AutoModeState } from './daemonAutoModeEvents';

export interface AutoModePolicy {
  state: AutoModeState | null;

  error: string | null;
  loading: boolean;

  resolvingID: number | null;

  pendingCount: number;
  refresh: () => Promise<void>;
  promote: (id: number) => Promise<void>;
  discard: (id: number) => Promise<void>;

  addPattern: (list: AutoModePatternList, pattern: string) => Promise<void>;
  removePattern: (list: AutoModePatternList, pattern: string) => Promise<void>;

  editingList: AutoModePatternList | null;
}

export type AutoModePatternList = 'allow' | 'hard_deny';

interface AutoModePolicyOptions {
  enabled: boolean;
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  addPattern: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  removePattern: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
}

const message = (err: unknown, fallback: string): string =>
  err instanceof Error ? err.message : fallback;

export function useAutoModePolicy(options: AutoModePolicyOptions): AutoModePolicy {
  const {
    enabled, getState, promoteProposal, discardProposal, addPattern, removePattern,
  } = options;
  const [state, setState] = useState<AutoModeState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [resolvingID, setResolvingID] = useState<number | null>(null);
  const [editingList, setEditingList] = useState<AutoModePatternList | null>(null);

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

  const edit = useCallback(async (
    list: AutoModePatternList,
    pattern: string,
    action: (list: string, pattern: string) => Promise<AutoModePatternEdit>,
  ) => {
    setEditingList(list);
    try {
      await action(list, pattern);

      await refresh();
    } finally {
      setEditingList(null);
    }
  }, [refresh]);

  const add = useCallback(
    (list: AutoModePatternList, pattern: string) => edit(list, pattern, addPattern),
    [edit, addPattern],
  );
  const remove = useCallback(
    (list: AutoModePatternList, pattern: string) => edit(list, pattern, removePattern),
    [edit, removePattern],
  );

  useEffect(() => {
    if (!enabled) {
      seqRef.current++;
      setState(null);
      setError(null);
      setLoading(false);
      setEditingList(null);
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
    addPattern: add,
    removePattern: remove,
    editingList,
  };
}
