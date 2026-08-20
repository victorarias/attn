// The plugins panel's own state: the source path being typed, the per-plugin
// priority drafts, and the list re-read that follows every install, removal or
// priority change.

import { useCallback, useState } from 'react';
import type { PluginListResult } from '../hooks/useDaemonSocket';
import { usePanelAction, type PanelAction } from './settingsPanelAction';

export interface PluginPanel extends PanelAction {
  sourcePath: string;
  setSourcePath: (next: string) => void;
  /**
   * Priority is edited as text and committed on blur, so the draft is a string.
   * Before the first re-list a plugin has no draft yet, hence the fallback.
   */
  priorityDraft: (name: string, fallback?: string) => string;
  setPriorityDraft: (name: string, value: string) => void;
  loading: boolean;
  /** Re-read the list and reseed the priority drafts from it. */
  refresh: () => Promise<void>;
}

export function usePluginPanel(
  listPlugins: () => Promise<PluginListResult>,
): PluginPanel {
  const action = usePanelAction();
  const { fail, clearError } = action;
  const [sourcePath, setSourcePath] = useState('');
  const [priorityDrafts, setPriorityDrafts] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    clearError();
    try {
      const result = await listPlugins();
      setPriorityDrafts(
        Object.fromEntries(result.plugins.map((plugin) => [plugin.name, String(plugin.priority)])),
      );
    } catch (caught) {
      fail(caught instanceof Error ? caught.message : 'Failed to load plugins');
    } finally {
      setLoading(false);
    }
  }, [clearError, fail, listPlugins]);

  const priorityDraft = useCallback(
    (name: string, fallback = '') => priorityDrafts[name] ?? fallback,
    [priorityDrafts],
  );

  const setPriorityDraft = useCallback((name: string, value: string) => {
    setPriorityDrafts((prev) => ({ ...prev, [name]: value }));
  }, []);

  return {
    ...action,
    sourcePath,
    setSourcePath,
    priorityDraft,
    setPriorityDraft,
    loading,
    refresh,
  };
}
