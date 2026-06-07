/**
 * Component Harness Registry
 *
 * Register all component harnesses here for the test harness router.
 */
import type { HarnessProps } from '../types';
import { DashboardPRsHarness } from './DashboardPRsHarness';
import { DiffDetailPanelHarness } from './DiffDetailPanelHarness';
import { GridLayoutControlHarness } from './GridLayoutControlHarness';
import { GridViewHarness } from './GridViewHarness';
import { SessionReviewLoopBarHarness } from './SessionReviewLoopBarHarness';
import { UnifiedDiffEditorHarness } from './UnifiedDiffEditorHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  DashboardPRs: DashboardPRsHarness,
  DiffDetailPanel: DiffDetailPanelHarness,
  GridLayoutControl: GridLayoutControlHarness,
  GridView: GridViewHarness,
  SessionReviewLoopBar: SessionReviewLoopBarHarness,
  UnifiedDiffEditor: UnifiedDiffEditorHarness,
};
