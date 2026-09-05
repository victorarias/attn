import { useCallback, useEffect, useRef, useState } from 'react';
import type { DelegationPreferences } from '../types/generated';
import type { DelegationSettingsState } from './daemonDelegationEvents';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';

export function useDelegationPreferences(active: boolean, load: () => Promise<DelegationSettingsState>, save: (value: DelegationPreferences) => Promise<DelegationSettingsState>) {
  const [state, setState] = useState<DelegationSettingsState | null>(null);
  const [draft, setDraft] = useState<DelegationPreferences | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [changedElsewhere, setChangedElsewhere] = useState(false);
  const pushed = useDelegationPreferencesPush(s => s.version);
  const draftRef = useRef(draft);
  const savedRef = useRef(state?.preferences);
  const busyRef = useRef(false);
  const dirty = draft !== null && JSON.stringify(draft) !== JSON.stringify(state?.preferences);
  draftRef.current = draft;
  savedRef.current = state?.preferences;
  const request = useRef(0);

  const reload = useCallback(async (discard = false) => {
    const id = ++request.current;
    setError('');
    try {
      const next = await load();
      if (id !== request.current) return;
      const edited = draftRef.current !== null && JSON.stringify(draftRef.current) !== JSON.stringify(savedRef.current);
      if (edited && !discard) {
        if (next.preferences.revision !== savedRef.current?.revision) setChangedElsewhere(true);
        return;
      }
      setState(next); setDraft(structuredClone(next.preferences)); setChangedElsewhere(false);
    } catch (e) { if (id === request.current) setError(String(e instanceof Error ? e.message : e)); }
  }, [load]);

  useEffect(() => { if (active && !busyRef.current) void reload(); }, [active, pushed, reload]);
  useEffect(() => () => { request.current++; }, []);

  const persist = useCallback(async (value: DelegationPreferences) => {
    if (busyRef.current) return;
    busyRef.current = true; setBusy(true); setError(''); request.current++;
    try {
      const next = await save(value);
      setState(next); setDraft(structuredClone(next.preferences)); setChangedElsewhere(false);
    } catch (e) { setError(String(e instanceof Error ? e.message : e)); }
    finally { busyRef.current = false; setBusy(false); }
  }, [save]);

  return { state, draft, setDraft, busy, dirty, error, changedElsewhere, reload, persist };
}
export type DelegationPreferencesPolicy = ReturnType<typeof useDelegationPreferences>;
