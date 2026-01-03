/**
 * Component Harness Registry
 *
 * Register all component harnesses here for the test harness router.
 */
import type { HarnessProps } from '../types';
import { ReviewPanelHarness } from './ReviewPanelHarness';
import { UnifiedDiffEditorHarness } from './UnifiedDiffEditorHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  ReviewPanel: ReviewPanelHarness,
  UnifiedDiffEditor: UnifiedDiffEditorHarness,
};
