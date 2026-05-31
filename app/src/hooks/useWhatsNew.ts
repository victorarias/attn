// app/src/hooks/useWhatsNew.ts
// One-time "what's new" gating. Shows the modal once per content release, then
// remembers it was seen. Bump WHATS_NEW_ID when there's a new story to tell.

import { useCallback, useEffect, useState } from 'react';

export const WHATS_NEW_ID = 'workspaces-2026-05';
export const WHATS_NEW_STORAGE_KEY = 'attn.whats_new.last_seen';

function readLastSeen(): string | null {
  try {
    return window.localStorage.getItem(WHATS_NEW_STORAGE_KEY);
  } catch (err) {
    console.warn('[whats-new] Failed to read last-seen id:', err);
    return null;
  }
}

function persistSeen(id: string): void {
  try {
    window.localStorage.setItem(WHATS_NEW_STORAGE_KEY, id);
  } catch (err) {
    console.warn('[whats-new] Failed to persist seen id:', err);
  }
}

export interface WhatsNewControls {
  isOpen: boolean;
  /** Re-open the modal on demand (e.g. from a Help entry). */
  open: () => void;
  /** Close and mark the current release as seen. */
  dismiss: () => void;
}

export function useWhatsNew(): WhatsNewControls {
  const [isOpen, setIsOpen] = useState(false);

  useEffect(() => {
    if (readLastSeen() !== WHATS_NEW_ID) {
      setIsOpen(true);
    }
  }, []);

  const open = useCallback(() => setIsOpen(true), []);

  const dismiss = useCallback(() => {
    setIsOpen(false);
    persistSeen(WHATS_NEW_ID);
  }, []);

  return { isOpen, open, dismiss };
}
