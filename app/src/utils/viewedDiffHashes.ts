/**
 * Durable, per-review persistence for the diff panel's "changed since viewed"
 * hashes.
 *
 * The panel keeps a hash of each file's diff in memory so it can notice when a
 * file changed after you viewed it. Backend review state restores *which* files
 * were viewed, but not their hashes — so without a durable baseline, a reopened
 * panel has nothing to compare against and silently stops flagging changes for
 * every restored file.
 *
 * localStorage gives us a frontend-only durable baseline keyed by review id,
 * which keeps this change-detection working across panel close/reopen and app
 * restarts without touching the daemon protocol.
 */
const KEY_PREFIX = 'attn:viewedDiffHashes:';

export function loadViewedDiffHashes(reviewId: string): Map<string, string> {
  try {
    const raw = localStorage.getItem(KEY_PREFIX + reviewId);
    if (!raw) return new Map();
    const parsed = JSON.parse(raw) as Record<string, string>;
    return new Map(Object.entries(parsed));
  } catch {
    return new Map();
  }
}

export function saveViewedDiffHashes(reviewId: string, hashes: Map<string, string>): void {
  try {
    localStorage.setItem(KEY_PREFIX + reviewId, JSON.stringify(Object.fromEntries(hashes)));
  } catch {
    // localStorage may be unavailable or full; change detection degrades to
    // in-memory only, which is no worse than before this baseline existed.
  }
}

/** Drop every persisted diff-hash entry (used to reset state in tests). */
export function clearAllViewedDiffHashes(): void {
  try {
    const keys: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key && key.startsWith(KEY_PREFIX)) keys.push(key);
    }
    for (const key of keys) localStorage.removeItem(key);
  } catch {
    // ignore
  }
}
