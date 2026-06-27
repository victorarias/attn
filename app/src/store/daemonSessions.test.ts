import { describe, it, expect, beforeEach } from 'vitest';
import { useDaemonStore } from './daemonSessions';

describe('daemonSessions store', () => {
  beforeEach(() => {
    // Reset store state between tests
    useDaemonStore.setState({
      daemonSessions: [],
      prs: [],
      repoStates: [],
      isConnected: false,
    });
  });

  describe('setDaemonSessions', () => {
    it('updates daemonSessions state', () => {
      const sessions = [
        { id: 'sess-1', label: 'Session 1', state: 'idle' as const },
      ];
      useDaemonStore.getState().setDaemonSessions(sessions as any);
      expect(useDaemonStore.getState().daemonSessions).toEqual(sessions);
    });
  });

  describe('setPRs', () => {
    it('updates prs state', () => {
      const prs = [{ id: 'pr-1', title: 'Test PR' }];
      useDaemonStore.getState().setPRs(prs as any);
      expect(useDaemonStore.getState().prs).toEqual(prs);
    });
  });

  describe('setRepoStates', () => {
    it('updates repoStates', () => {
      const repos = [{ repo: 'org/repo', muted: true, collapsed: false }];
      useDaemonStore.getState().setRepoStates(repos);
      expect(useDaemonStore.getState().repoStates).toEqual(repos);
    });
  });

  describe('isRepoMuted', () => {
    it('returns false when repo is not in repoStates', () => {
      expect(useDaemonStore.getState().isRepoMuted('unknown/repo')).toBe(false);
    });

    it('returns false when repo exists but is not muted', () => {
      useDaemonStore.getState().setRepoStates([
        { repo: 'org/repo', muted: false, collapsed: false },
      ]);
      expect(useDaemonStore.getState().isRepoMuted('org/repo')).toBe(false);
    });

    it('returns true when repo is muted', () => {
      useDaemonStore.getState().setRepoStates([
        { repo: 'org/repo', muted: true, collapsed: false },
      ]);
      expect(useDaemonStore.getState().isRepoMuted('org/repo')).toBe(true);
    });

    it('finds correct repo among multiple', () => {
      useDaemonStore.getState().setRepoStates([
        { repo: 'org/repo-a', muted: false, collapsed: false },
        { repo: 'org/repo-b', muted: true, collapsed: false },
        { repo: 'org/repo-c', muted: false, collapsed: false },
      ]);
      expect(useDaemonStore.getState().isRepoMuted('org/repo-a')).toBe(false);
      expect(useDaemonStore.getState().isRepoMuted('org/repo-b')).toBe(true);
      expect(useDaemonStore.getState().isRepoMuted('org/repo-c')).toBe(false);
    });
  });

  describe('setConnected', () => {
    it('updates connection status', () => {
      expect(useDaemonStore.getState().isConnected).toBe(false);
      useDaemonStore.getState().setConnected(true);
      expect(useDaemonStore.getState().isConnected).toBe(true);
      useDaemonStore.getState().setConnected(false);
      expect(useDaemonStore.getState().isConnected).toBe(false);
    });
  });
});
