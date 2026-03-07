/**
 * Component Harness Registry
 *
 * Register all component harnesses here for the test harness router.
 */
import type { HarnessProps } from '../types';
import { DashboardPRsHarness } from './DashboardPRsHarness';
import { DiffDetailPanelHarness } from './DiffDetailPanelHarness';
import { UnifiedDiffEditorHarness } from './UnifiedDiffEditorHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  DashboardPRs: DashboardPRsHarness,
  DiffDetailPanel: DiffDetailPanelHarness,
  UnifiedDiffEditor: UnifiedDiffEditorHarness,
};
