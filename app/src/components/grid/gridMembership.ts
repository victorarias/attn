// Grid membership: every live agent session is on the grid by default. The user
// can remove individual sessions, which excludes them until explicitly restored.
//
// Exclusions are keyed by the stable sessionId — NOT the runtimeId, which is
// reassigned when a session's worker is replaced (e.g. across a daemon restart) —
// so a removed session stays off the grid across app launches. The set is small
// (you only exclude the handful you don't want to watch); stale ids for closed
// sessions are harmless and simply never match again, so we don't prune.

const GRID_EXCLUDED_STORAGE_KEY = 'attn.grid.excluded';

export function readExcludedGridSessions(): Set<string> {
  try {
    const raw = window.localStorage.getItem(GRID_EXCLUDED_STORAGE_KEY);
    if (!raw) return new Set();
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.filter((id): id is string => typeof id === 'string'));
  } catch {
    return new Set();
  }
}

export function persistExcludedGridSessions(excluded: Set<string>): void {
  try {
    window.localStorage.setItem(GRID_EXCLUDED_STORAGE_KEY, JSON.stringify([...excluded]));
  } catch (err) {
    console.warn('[grid] Failed to persist excluded sessions:', err);
  }
}
