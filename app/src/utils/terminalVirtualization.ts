// Runtime-configurable virtualization of off-screen terminal workspaces.
//
// Each live workspace keeps a Ghostty WASM model + WebGL renderer (~32 MiB of
// atlas + GPU texture per pane, plus scrollback) mounted. attn keeps every
// workspace mounted at once, so with many workspaces this dominates the app's
// memory. We keep only the active workspace plus the N most-recently-used
// workspaces "warm" (terminals mounted); the rest render a placeholder and
// rehydrate from daemon replay (same_app_remount) when they next become visible.
//
// N is configurable at runtime so the memory-vs-instant-switching tradeoff can
// be tuned without a rebuild:
//   - localStorage key `attn.perf.warmWorkspaceLimit`
//   - window.attnSetWarmWorkspaces(n) — applies live (no reload) and persists
//   - n = recent workspaces kept warm BESIDES the active one (default 3)
//   - n = 0  -> only the active workspace is live (maximum memory savings)
//   - n < 0  -> keep all workspaces live (virtualization disabled)

export const WARM_WORKSPACE_LIMIT_STORAGE_KEY = 'attn.perf.warmWorkspaceLimit';
export const DEFAULT_WARM_WORKSPACE_LIMIT = 3;

export function readWarmWorkspaceLimit(): number {
  try {
    const raw = window.localStorage.getItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY);
    if (raw === null) return DEFAULT_WARM_WORKSPACE_LIMIT;
    const parsed = Number.parseInt(raw, 10);
    return Number.isFinite(parsed) ? parsed : DEFAULT_WARM_WORKSPACE_LIMIT;
  } catch {
    return DEFAULT_WARM_WORKSPACE_LIMIT;
  }
}

export function writeWarmWorkspaceLimit(limit: number): void {
  try {
    window.localStorage.setItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY, String(limit));
  } catch {
    // localStorage may be unavailable (private mode); the in-memory value still applies.
  }
}

// Returns the set of workspace ids whose terminals should stay mounted, or
// null meaning "all workspaces live" (no virtualization). `allWorkspaceIds` is
// every currently-rendered workspace; `recentWorkspaceIds` is most-recent-first.
//
// Virtualization only engages when there are MORE workspaces than the warm
// budget (active + `limit` recent). With no more workspaces than the budget
// there is nothing to reclaim, so we keep them all live — this both matches the
// pre-virtualization behavior for the common case (a handful of workspaces) and
// avoids tearing terminals down before an active workspace is established (e.g.
// first paint, when activeWorkspaceId is still null), which would otherwise drop
// freshly-streamed PTY output across the remount.
export function computeWarmWorkspaceIds(
  allWorkspaceIds: string[],
  recentWorkspaceIds: string[],
  activeWorkspaceId: string | null,
  limit: number,
): Set<string> | null {
  if (limit < 0) return null;
  const budget = limit + 1; // active + `limit` recent workspaces.
  const present = new Set(allWorkspaceIds);
  if (present.size <= budget) return null; // nothing to reclaim; keep all live.
  const warm = new Set<string>();
  if (activeWorkspaceId && present.has(activeWorkspaceId)) warm.add(activeWorkspaceId);
  for (const id of recentWorkspaceIds) {
    if (warm.size >= budget) break;
    if (present.has(id)) warm.add(id);
  }
  // Fill any remaining budget from the current workspaces so the warm set is
  // never smaller than the budget while extra workspaces exist — before recency
  // or an active workspace are established there can be unused slots.
  for (const id of allWorkspaceIds) {
    if (warm.size >= budget) break;
    warm.add(id);
  }
  return warm;
}
