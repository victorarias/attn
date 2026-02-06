import { useCallback } from 'react';
import type { DaemonPR, DaemonSettings } from './useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import { getRepoName } from '../utils/repo';

export type OpenPRErrorKind =
  | 'missing_projects_directory'
  | 'missing_head_branch'
  | 'fetch_pr_details_failed'
  | 'ensure_repo_failed'
  | 'create_worktree_failed'
  | 'create_session_failed'
  | 'unknown';

export interface OpenPRError {
  kind: OpenPRErrorKind;
  message?: string;
}

export type OpenPRResult =
  | { success: true; sessionId: string; worktreePath: string; pr: DaemonPR }
  | { success: false; error: OpenPRError };

export interface UseOpenPRDeps {
  settings: DaemonSettings;
  sendFetchPRDetails: (id: string) => Promise<{ success: boolean; prs?: DaemonPR[]; error?: string }>;
  sendEnsureRepo: (targetPath: string, cloneUrl: string) => Promise<{ success: boolean; cloned?: boolean; error?: string }>;
  sendCreateWorktreeFromBranch: (
    repoPath: string,
    branch: string
  ) => Promise<{ success: boolean; path?: string; error?: string }>;
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent) => Promise<string>;
}

export function useOpenPR({
  settings,
  sendFetchPRDetails,
  sendEnsureRepo,
  sendCreateWorktreeFromBranch,
  createSession,
}: UseOpenPRDeps) {
  return useCallback(async (pr: DaemonPR, agent?: SessionAgent): Promise<OpenPRResult> => {
    const projectsDir = settings.projects_directory;
    if (!projectsDir) {
      return { success: false, error: { kind: 'missing_projects_directory' } };
    }

    let prWithBranch = pr;
    if (!prWithBranch.head_branch) {
      let detailsResult: { success: boolean; prs?: DaemonPR[]; error?: string };
      try {
        detailsResult = await sendFetchPRDetails(pr.id);
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        return { success: false, error: { kind: 'fetch_pr_details_failed', message } };
      }
      if (!detailsResult.success) {
        return {
          success: false,
          error: { kind: 'fetch_pr_details_failed', message: detailsResult.error },
        };
      }

      if (detailsResult.prs && detailsResult.prs.length > 0) {
        const updated = detailsResult.prs.find(
          (candidate) => candidate.id === pr.id
        );
        if (updated) {
          prWithBranch = updated;
        }
      }

      if (!prWithBranch.head_branch) {
        return { success: false, error: { kind: 'missing_head_branch' } };
      }
    }

    const repoName = getRepoName(pr.repo);
    const normalizedProjectsDir = projectsDir.replace(/\/+$/, '');
    const localRepoPath = `${normalizedProjectsDir}/${repoName}`;

    // Construct clone URL from PR data
    const cloneUrl = `https://${pr.host}/${pr.repo}.git`;

    // Ensure repo exists (clone if needed) and fetch remotes
    let ensureResult: { success: boolean; cloned?: boolean; error?: string };
    try {
      ensureResult = await sendEnsureRepo(localRepoPath, cloneUrl);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      return { success: false, error: { kind: 'ensure_repo_failed', message } };
    }
    if (!ensureResult.success) {
      return {
        success: false,
        error: { kind: 'ensure_repo_failed', message: ensureResult.error },
      };
    }

    const remoteBranch = `origin/${prWithBranch.head_branch}`;
    let worktreeResult: { success: boolean; path?: string; error?: string };
    try {
      worktreeResult = await sendCreateWorktreeFromBranch(localRepoPath, remoteBranch);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      return { success: false, error: { kind: 'create_worktree_failed', message } };
    }
    if (!worktreeResult.success || !worktreeResult.path) {
      return {
        success: false,
        error: { kind: 'create_worktree_failed', message: worktreeResult.error },
      };
    }

    const label = `${repoName}#${pr.number}`;
    let sessionId: string;
    try {
      sessionId = await createSession(label, worktreeResult.path, undefined, agent);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      return { success: false, error: { kind: 'create_session_failed', message } };
    }

    return {
      success: true,
      sessionId,
      worktreePath: worktreeResult.path,
      pr: prWithBranch,
    };
  }, [settings, sendFetchPRDetails, sendEnsureRepo, sendCreateWorktreeFromBranch, createSession]);
}
