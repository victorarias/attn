// app/src/hooks/usePRsNeedingAttention.ts
import { useMemo } from 'react';
import { DaemonPR } from './useDaemonSocket';
import { useDaemonStore } from '../store/daemonSessions';

interface FilteredPRs {
  // All non-muted PRs (repo, owner, and individual mute state)
  activePRs: DaemonPR[];
  // PRs that need attention (excludes approved without new changes)
  needsAttention: DaemonPR[];
  // PRs where you're a requested reviewer
  reviewRequested: DaemonPR[];
  // PRs you authored that need attention
  yourPRs: DaemonPR[];
}

/**
 * Extract owner from repo name (e.g., "spotify/backstage" → "spotify")
 */
function getOwnerFromRepo(repo: string): string {
  const parts = repo.split('/');
  return parts[0] || '';
}

/**
 * Hook that filters PRs based on mute state and attention criteria.
 * Centralizes the duplicated filter logic from App, Dashboard, and AttentionDrawer.
 *
 * @param prs - Array of PRs from daemon
 * @param hiddenPRs - Optional set of PR IDs to exclude (for post-merge fade out)
 */
export function usePRsNeedingAttention(
  prs: DaemonPR[],
  hiddenPRs?: Set<string>
): FilteredPRs {
  // Subscribe to both isRepoMuted/isOwnerMuted functions AND state arrays
  // The function references are stable, so we need the state arrays to trigger recalculation
  const { isRepoMuted, isOwnerMuted, repoStates, ownerStates } = useDaemonStore();

  return useMemo(() => {
    // Base filter: not individually muted, not repo muted, not owner muted, not hidden
    const activePRs = prs.filter(
      (p) => !p.muted &&
             !isRepoMuted(p.repo) &&
             !isOwnerMuted(getOwnerFromRepo(p.repo)) &&
             (!hiddenPRs || !hiddenPRs.has(p.id))
    );

    // PRs needing attention: active PRs that aren't approved (or have new changes since approval)
    const needsAttention = activePRs.filter(
      (p) => !p.approved_by_me || p.has_new_changes
    );

    // Split by role for attention drawer
    const reviewRequested = needsAttention.filter((p) => p.role === 'reviewer');
    const yourPRs = needsAttention.filter((p) => p.role === 'author');

    return { activePRs, needsAttention, reviewRequested, yourPRs };
  }, [prs, isRepoMuted, isOwnerMuted, repoStates, ownerStates, hiddenPRs]);
}
