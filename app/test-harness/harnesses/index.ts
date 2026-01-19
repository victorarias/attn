/**
 * Component Harness Registry
 *
 * Register all component harnesses here for the test harness router.
 */
import type { HarnessProps } from '../types';
import { DashboardPRsHarness } from './DashboardPRsHarness';
import { ReviewPanelHarness } from './ReviewPanelHarness';
import { UnifiedDiffEditorHarness } from './UnifiedDiffEditorHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  DashboardPRs: DashboardPRsHarness,
  ReviewPanel: ReviewPanelHarness,
  UnifiedDiffEditor: UnifiedDiffEditorHarness,
};
