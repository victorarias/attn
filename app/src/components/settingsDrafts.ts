// The two draft shapes the settings page repeats. Auto-save is the house rule
// there, so nearly every field lives the same life: seed from what the daemon
// says when the modal opens, edit locally, write on blur or on change, raise a
// quiet saved mark. Written out per field that was thirty-odd useState calls
// and a wall of near-identical commit handlers at the top of SettingsModal.
//
// A field reseeds when its OWN persisted value changes, not when any setting
// does — a broadcast about the projects directory has no business wiping a
// half-typed reviewer model.

import { useCallback, useState } from 'react';
import type { SavedFlash } from './useSavedFlash';
import type { SessionAgent } from '../types/sessionAgent';

type SetSetting = (key: string, value: string) => void;

interface DraftDeps {
  /** Reseed from the persisted value while this is true (the modal being open). */
  active: boolean;
  onSetSetting: SetSetting;
  savedFlash: SavedFlash;
}

export interface SettingDraftOptions extends DraftDeps {
  /** The persisted value, as the daemon reports it. */
  actual: string;
  settingKey: string;
  /**
   * Whether surrounding whitespace counts as an edit. It does for a path — a
   * trailing space is a different path — and does not for a number.
   */
  trim?: boolean;
}

export interface SettingDraft {
  value: string;
  /** Edit locally; nothing is written until commit. */
  set: (next: string) => void;
  /** Write if the value actually moved, and say so. */
  commit: () => void;
  onChange: (event: { target: { value: string } }) => void;
  /** Enter commits, the same as leaving the field. */
  onKeyDown: (event: { key: string }) => void;
}

export function useSettingDraft({
  actual,
  settingKey,
  trim = false,
  active,
  onSetSetting,
  savedFlash,
}: SettingDraftOptions): SettingDraft {
  const [value, setValue] = useState(actual);
  const [seededFrom, setSeededFrom] = useState<string | null>(active ? actual : null);

  // Reseeding during render rather than from an effect: React re-runs the
  // component before painting, so the field never shows a stale value for a
  // frame. Closing sets the mark to null, which is what makes reopening always
  // reseed and drop a half-typed draft.
  if (active && seededFrom !== actual) {
    setSeededFrom(actual);
    setValue(actual);
  } else if (!active && seededFrom !== null) {
    setSeededFrom(null);
  }

  const commit = useCallback(() => {
    const next = trim ? value.trim() : value;
    const current = trim ? actual.trim() : actual;
    if (next === current) return;
    onSetSetting(settingKey, next);
    savedFlash.flash(settingKey);
  }, [actual, onSetSetting, savedFlash, settingKey, trim, value]);

  const onChange = useCallback(
    (event: { target: { value: string } }) => setValue(event.target.value),
    [],
  );

  const onKeyDown = useCallback((event: { key: string }) => {
    if (event.key === 'Enter') commit();
  }, [commit]);

  return { value, set: setValue, commit, onChange, onKeyDown };
}

type AgentValues = Partial<Record<SessionAgent, string>>;

export interface AgentSettingDraftOptions extends DraftDeps {
  /**
   * Must be a stable object — the reseed guard compares it by reference during
   * render, so a fresh literal per render reseeds forever. Every caller today
   * hands over a useMemo'd record.
   */
  actual: AgentValues;
  settingKey: (agent: SessionAgent) => string;
  /**
   * Which saved mark this field raises, when it is not its own. The effort
   * <select>s share a row with their model input and borrow its mark rather
   * than putting a second "Saved" beside the first.
   */
  flashKey?: (agent: SessionAgent) => string;
  trim?: boolean;
}

export interface AgentSettingDraft {
  value: (agent: SessionAgent) => string;
  set: (agent: SessionAgent, next: string) => void;
  commit: (agent: SessionAgent) => void;
  /** Set and write in one go, for a control that is whole the moment it changes. */
  apply: (agent: SessionAgent, next: string) => void;
}

export function useAgentSettingDrafts({
  actual,
  settingKey,
  flashKey = settingKey,
  trim = false,
  active,
  onSetSetting,
  savedFlash,
}: AgentSettingDraftOptions): AgentSettingDraft {
  const [values, setValues] = useState<AgentValues>(actual);
  const [seededFrom, setSeededFrom] = useState<AgentValues | null>(active ? actual : null);

  // Same reseed rule as useSettingDraft, keyed on the memoized per-agent object
  // the caller passes rather than on a string.
  if (active && seededFrom !== actual) {
    setSeededFrom(actual);
    setValues(actual);
  } else if (!active && seededFrom !== null) {
    setSeededFrom(null);
  }

  const value = useCallback((agent: SessionAgent) => values[agent] || '', [values]);

  const set = useCallback((agent: SessionAgent, next: string) => {
    setValues((prev) => ({ ...prev, [agent]: next }));
  }, []);

  const commit = useCallback((agent: SessionAgent) => {
    const raw = values[agent] || '';
    const current = actual[agent] || '';
    const next = trim ? raw.trim() : raw;
    if (next === (trim ? current.trim() : current)) return;
    onSetSetting(settingKey(agent), next);
    savedFlash.flash(flashKey(agent));
  }, [actual, flashKey, onSetSetting, savedFlash, settingKey, trim, values]);

  const apply = useCallback((agent: SessionAgent, next: string) => {
    setValues((prev) => ({ ...prev, [agent]: next }));
    onSetSetting(settingKey(agent), next);
    savedFlash.flash(flashKey(agent));
  }, [flashKey, onSetSetting, savedFlash, settingKey]);

  return { value, set, commit, apply };
}
