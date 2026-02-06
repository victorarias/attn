/**
 * Dashboard PRs Panel Harness
 *
 * Renders the Dashboard PR list in isolation with mocked daemon props.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Dashboard } from '../../src/components/Dashboard';
import { DaemonProvider } from '../../src/contexts/DaemonContext';
import { useDaemonStore } from '../../src/store/daemonSessions';
import { useOpenPR } from '../../src/hooks/useOpenPR';
import type { DaemonPR, DaemonSettings } from '../../src/hooks/useDaemonSocket';
import { PRRole } from '../../src/types/generated';
import type { HarnessProps } from '../types';
import '../../src/components/Dashboard.css';

const BASE_SETTINGS: DaemonSettings = {
  projects_directory: '/Users/test/projects',
};
const DEFAULT_HOST = 'github.com';
const formatPRID = (repo: string, number: number) => `${DEFAULT_HOST}:${repo}#${number}`;

type Scenario =
  | 'default'
  | 'fetch-details-failed'
  | 'missing-projects-directory'
  | 'fetch-remotes-failed'
  | 'worktree-failed'
  | 'actions';

function getScenario(): Scenario {
  const params = new URLSearchParams(window.location.search);
  const scenario = params.get('scenario');
  switch (scenario) {
    case 'fetch-details-failed':
    case 'missing-projects-directory':
    case 'fetch-remotes-failed':
    case 'worktree-failed':
    case 'actions':
      return scenario;
    default:
      return 'default';
  }
}

function getInitialPRs(scenario: Scenario): DaemonPR[] {
  switch (scenario) {
    case 'fetch-details-failed':
      return [
        {
          id: formatPRID('test/fetchfail', 303),
          host: DEFAULT_HOST,
          repo: 'test/fetchfail',
          number: 303,
          title: 'Fetch details failed',
          url: 'https://example.com/test/fetchfail/pull/303',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: false,
          approved_by_me: false,
          has_new_changes: true,
        },
      ];
    case 'missing-projects-directory':
      return [
        {
          id: formatPRID('test/noprojects', 404),
          host: DEFAULT_HOST,
          repo: 'test/noprojects',
          number: 404,
          title: 'Missing projects directory',
          url: 'https://example.com/test/noprojects/pull/404',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: false,
          approved_by_me: false,
          has_new_changes: true,
        },
      ];
    case 'fetch-remotes-failed':
      return [
        {
          id: formatPRID('test/fetchremotes', 505),
          host: DEFAULT_HOST,
          repo: 'test/fetchremotes',
          number: 505,
          title: 'Fetch remotes failed',
          url: 'https://example.com/test/fetchremotes/pull/505',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: true,
          head_branch: 'feature/remotes',
          approved_by_me: false,
          has_new_changes: true,
        },
      ];
    case 'worktree-failed':
      return [
        {
          id: formatPRID('test/worktree', 606),
          host: DEFAULT_HOST,
          repo: 'test/worktree',
          number: 606,
          title: 'Worktree failed',
          url: 'https://example.com/test/worktree/pull/606',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: true,
          head_branch: 'feature/worktree',
          approved_by_me: false,
          has_new_changes: true,
        },
      ];
    case 'actions':
      return [
        {
          id: formatPRID('test/actions', 707),
          host: DEFAULT_HOST,
          repo: 'test/actions',
          number: 707,
          title: 'Action buttons',
          url: 'https://example.com/test/actions/pull/707',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: true,
          head_branch: 'feature/actions',
          approved_by_me: false,
          has_new_changes: false,
        },
      ];
    case 'default':
    default:
      return [
        {
          id: formatPRID('test/repo', 101),
          host: DEFAULT_HOST,
          repo: 'test/repo',
          number: 101,
          title: 'Missing head branch',
          url: 'https://example.com/test/repo/pull/101',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: false,
          approved_by_me: false,
          has_new_changes: true,
        },
        {
          id: formatPRID('test/missing', 202),
          host: DEFAULT_HOST,
          repo: 'test/missing',
          number: 202,
          title: 'Still missing head branch',
          url: 'https://example.com/test/missing/pull/202',
          role: PRRole.Reviewer,
          state: 'waiting',
          reason: 'review_needed',
          last_updated: new Date().toISOString(),
          last_polled: new Date().toISOString(),
          muted: false,
          details_fetched: false,
          approved_by_me: false,
          has_new_changes: true,
        },
      ];
  }
}

export function DashboardPRsHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const scenario = useMemo(() => getScenario(), []);
  const initialPrs = useMemo(() => getInitialPRs(scenario), [scenario]);
  const [prs, setPrs] = useState<DaemonPR[]>(initialPrs);
  const prsRef = useRef<DaemonPR[]>(initialPrs);
  const [openStatus, setOpenStatus] = useState('idle');
  const [openError, setOpenError] = useState<string | null>(null);
  const [, forceRender] = useState(0);

  useEffect(() => {
    useDaemonStore.setState({ repoStates: [] });
  }, []);

  useEffect(() => {
    setTriggerRerender(() => {
      forceRender((n) => n + 1);
    });
  }, [setTriggerRerender]);

  const sendPRAction = useCallback(async (action: 'approve' | 'merge', id: string, method?: string) => {
    window.__HARNESS__.recordCall('sendPRAction', [action, id, method]);
    return { success: true };
  }, []);

  const sendMutePR = useCallback((prId: string) => {
    window.__HARNESS__.recordCall('sendMutePR', [prId]);
  }, []);

  const sendMuteRepo = useCallback((repo: string) => {
    window.__HARNESS__.recordCall('sendMuteRepo', [repo]);
  }, []);

  const sendPRVisited = useCallback((prId: string) => {
    window.__HARNESS__.recordCall('sendPRVisited', [prId]);
  }, []);

  const sendEnsureRepo = useCallback(async (targetPath: string, cloneUrl: string) => {
    window.__HARNESS__.recordCall('sendEnsureRepo', [targetPath, cloneUrl]);
    if (scenario === 'fetch-remotes-failed' && targetPath.includes('fetchremotes')) {
      return { success: false, cloned: false, error: 'not a git repository' };
    }
    return { success: true, cloned: false };
  }, [scenario]);

  const sendCreateWorktreeFromBranch = useCallback(async (repoPath: string, branch: string) => {
    window.__HARNESS__.recordCall('sendCreateWorktreeFromBranch', [repoPath, branch]);
    if (scenario === 'worktree-failed' && repoPath.includes('worktree')) {
      return { success: false, error: 'already exists' };
    }
    return { success: true, path: `${repoPath}/../test-repo-feature` };
  }, [scenario]);

  const sendFetchPRDetails = useCallback(async (id: string) => {
    window.__HARNESS__.recordCall('sendFetchPRDetails', [id]);
    if (scenario === 'fetch-details-failed' && id === formatPRID('test/fetchfail', 303)) {
      return { success: false, error: 'boom' };
    }
    const updated = prsRef.current.map((pr) =>
      pr.id === id && pr.number === 101
        ? { ...pr, head_branch: 'feature/missing-head', details_fetched: true }
        : pr.id === id
          ? { ...pr, details_fetched: true }
          : pr
    );
    prsRef.current = updated;
    setPrs(updated);
    return { success: true, prs: updated };
  }, [scenario]);

  const createSession = useCallback(async (label: string, cwd: string) => {
    window.__HARNESS__.recordCall('createSession', [label, cwd]);
    return 'session-123';
  }, []);

  const settings = useMemo(() => {
    if (scenario === 'missing-projects-directory') {
      return {} as DaemonSettings;
    }
    return BASE_SETTINGS;
  }, [scenario]);

  const openPR = useOpenPR({
    settings,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendCreateWorktreeFromBranch,
    createSession,
  });

  const handleOpenPR = useCallback(async (pr: DaemonPR) => {
    window.__HARNESS__.recordCall('onOpenPR', [pr.id]);
    setOpenStatus('opening');
    setOpenError(null);
    const result = await openPR(pr);
    if (result.success) {
      setOpenStatus('success');
    } else {
      setOpenStatus('error');
      setOpenError(result.error.kind);
    }
  }, [openPR]);

  const handleRefresh = useCallback(() => {
    window.__HARNESS__.recordCall('onRefreshPRs', []);
  }, []);

  useEffect(() => {
    onReady();
  }, [onReady]);

  return (
    <DaemonProvider
      sendPRAction={sendPRAction}
      sendMutePR={sendMutePR}
      sendMuteRepo={sendMuteRepo}
      sendPRVisited={sendPRVisited}
    >
      <div style={{ padding: 24 }}>
        <Dashboard
          sessions={[]}
          prs={prs}
          isLoading={false}
          isRefreshing={false}
          refreshError={null}
          rateLimit={null}
          settings={settings}
          onSelectSession={() => {}}
          onNewSession={() => {}}
          onRefreshPRs={handleRefresh}
          onOpenPR={handleOpenPR}
          onSetSetting={() => {}}
        />
      </div>
      <div data-testid="open-status">{openStatus}</div>
      {openError && <div data-testid="open-error">{openError}</div>}
    </DaemonProvider>
  );
}
