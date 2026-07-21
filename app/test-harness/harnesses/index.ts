/**
 * Component Harness Registry
 *
 * Register all component harnesses here for the test harness router.
 */
import type { HarnessProps } from '../types';
import { AutomationYamlEditorHarness } from './AutomationYamlEditorHarness';
import { BrokenLinksHarness } from './BrokenLinksHarness';
import { DashboardPRsHarness } from './DashboardPRsHarness';
import { DiffViewHarness } from './DiffViewHarness';
import { FileTreeHarness } from './FileTreeHarness';
import { FrontmatterCardHarness } from './FrontmatterCardHarness';
import { GridLayoutControlHarness } from './GridLayoutControlHarness';
import { GridViewHarness } from './GridViewHarness';
import { LiveMarkdownEditorHarness } from './LiveMarkdownEditorHarness';
import { NotebookBrowserHarness } from './NotebookBrowserHarness';
import { NotebookTileHarness } from './NotebookTileHarness';
import { PresentTourHarness } from './PresentTourHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  AutomationYamlEditor: AutomationYamlEditorHarness,
  BrokenLinks: BrokenLinksHarness,
  DashboardPRs: DashboardPRsHarness,
  DiffView: DiffViewHarness,
  FileTree: FileTreeHarness,
  FrontmatterCard: FrontmatterCardHarness,
  GridLayoutControl: GridLayoutControlHarness,
  GridView: GridViewHarness,
  LiveMarkdownEditor: LiveMarkdownEditorHarness,
  NotebookBrowser: NotebookBrowserHarness,
  NotebookTile: NotebookTileHarness,
  PresentTour: PresentTourHarness,
};
