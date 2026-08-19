// A quiet, self-clearing "Saved" mark for settings that apply on change or on
// blur. Auto-save without an affordance leaves the user guessing whether a
// blur-commit landed; a modal for a one-field write is worse. So a flag appears
// beside the field for a moment and takes itself away.
//
// One shared timer map rather than state per field: a settings section holds
// dozens of fields, and a hook per field would be a hook per row.

import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * How long a "Saved" mark stays up. Long enough to catch after the blur that
 * caused it, short enough that a second edit reads as a second save rather than
 * the first one still showing.
 */
const SAVED_FLASH_MS = 1600;

export interface SavedFlash {
  /** Raise the mark for one field. Call it after the write, not before. */
  flash: (key: string) => void;
  /** Whether that field's mark is up right now. */
  saved: (key: string) => boolean;
}

export function useSavedFlash(): SavedFlash {
  const [marks, setMarks] = useState<Record<string, number>>({});
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  useEffect(() => () => {
    // Nothing may fire into an unmounted modal: the settings panel is opened and
    // closed constantly, and a leaked timer per field adds up.
    for (const timer of timers.current.values()) clearTimeout(timer);
    timers.current.clear();
  }, []);

  const flash = useCallback((key: string) => {
    const existing = timers.current.get(key);
    if (existing) clearTimeout(existing);
    setMarks((prev) => ({ ...prev, [key]: (prev[key] ?? 0) + 1 }));
    timers.current.set(key, setTimeout(() => {
      timers.current.delete(key);
      setMarks((prev) => {
        if (!(key in prev)) return prev;
        const next = { ...prev };
        delete next[key];
        return next;
      });
    }, SAVED_FLASH_MS));
  }, []);

  const saved = useCallback((key: string) => key in marks, [marks]);

  return { flash, saved };
}

/** The mark itself, so every section renders the same thing. */
export function SavedMark({ shown, testID }: { shown: boolean; testID?: string }) {
  if (!shown) return null;
  return (
    <span className="settings-saved-mark" data-testid={testID} role="status">
      Saved
    </span>
  );
}
