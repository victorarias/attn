/**
 * Component Harness Registry
 *
 * Register all component harnesses here for the test harness router.
 */
import type { HarnessProps } from '../types';
import { BrokenLinksHarness } from './BrokenLinksHarness';
import { DashboardPRsHarness } from './DashboardPRsHarness';
import { DiffDetailPanelHarness } from './DiffDetailPanelHarness';
import { DiffViewHarness } from './DiffViewHarness';
import { FileTreeHarness } from './FileTreeHarness';
import { FrontmatterCardHarness } from './FrontmatterCardHarness';
import { GridLayoutControlHarness } from './GridLayoutControlHarness';
import { GridViewHarness } from './GridViewHarness';
import { LiveMarkdownEditorHarness } from './LiveMarkdownEditorHarness';
import { NotebookBrowserHarness } from './NotebookBrowserHarness';
import { NotebookTileHarness } from './NotebookTileHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  BrokenLinks: BrokenLinksHarness,
  DashboardPRs: DashboardPRsHarness,
  DiffDetailPanel: DiffDetailPanelHarness,
  DiffView: DiffViewHarness,
  FileTree: FileTreeHarness,
  FrontmatterCard: FrontmatterCardHarness,
  GridLayoutControl: GridLayoutControlHarness,
  GridView: GridViewHarness,
  LiveMarkdownEditor: LiveMarkdownEditorHarness,
  NotebookBrowser: NotebookBrowserHarness,
  NotebookTile: NotebookTileHarness,
};
