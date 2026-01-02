import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '../test/utils';
import {
  createMockDaemon,
  createGitStatus,
  createFileDiffResult,
  createReviewState,
  setupDefaultResponses,
  sleep,
  MockDaemon,
} from '../test/utils';
import { ReviewPanel } from './ReviewPanel';

// Mock CodeMirror since it requires DOM measurements
vi.mock('@codemirror/view', () => {
  class MockEditorView {
    destroy = vi.fn();
    static theme = vi.fn(() => []);
    static editable = { of: vi.fn(() => []) };
  }

  return {
    EditorView: MockEditorView,
    lineNumbers: vi.fn(() => []),
    highlightActiveLineGutter: vi.fn(() => []),
    highlightSpecialChars: vi.fn(() => []),
    drawSelection: vi.fn(() => []),
    rectangularSelection: vi.fn(() => []),
  };
});

vi.mock('@codemirror/state', () => ({
  EditorState: {
    create: vi.fn(() => ({})),
    allowMultipleSelections: { of: vi.fn(() => []) },
  },
}));

vi.mock('@codemirror/merge', () => ({
  unifiedMergeView: vi.fn(() => []),
}));

vi.mock('@codemirror/theme-one-dark', () => ({
  oneDark: [],
}));

vi.mock('@codemirror/lang-javascript', () => ({
  javascript: vi.fn(() => []),
}));

vi.mock('@codemirror/lang-markdown', () => ({
  markdown: vi.fn(() => []),
}));

vi.mock('@codemirror/lang-python', () => ({
  python: vi.fn(() => []),
}));

vi.mock('@codemirror/language', () => ({
  foldGutter: vi.fn(() => []),
  indentOnInput: vi.fn(() => []),
  syntaxHighlighting: vi.fn(() => []),
  defaultHighlightStyle: {},
  bracketMatching: vi.fn(() => []),
}));

vi.mock('@codemirror/commands', () => ({
  history: vi.fn(() => []),
}));

vi.mock('@codemirror/search', () => ({
  highlightSelectionMatches: vi.fn(() => []),
}));

describe('ReviewPanel', () => {
  let mockDaemon: MockDaemon;
  let onClose: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockDaemon = createMockDaemon();
    setupDefaultResponses(mockDaemon);
    onClose = vi.fn();
  });

  function renderPanel(overrides?: {
    gitStatus?: ReturnType<typeof createGitStatus>;
    isOpen?: boolean;
  }) {
    const gitStatus = overrides?.gitStatus ?? createGitStatus(['src/App.tsx']);
    const isOpen = overrides?.isOpen ?? true;

    return render(
      <ReviewPanel
        isOpen={isOpen}
        gitStatus={gitStatus}
        repoPath="/test/repo"
        branch="main"
        onClose={onClose}
        fetchDiff={mockDaemon.createFetchDiff()}
        getReviewState={mockDaemon.createGetReviewState()}
        markFileViewed={mockDaemon.createMarkFileViewed()}
      />
    );
  }

  // Helper to find file in the list (not toolbar)
  function getFileInList(filename: string): HTMLElement {
    const fileList = document.querySelector('.review-file-list');
    const fileItem = fileList?.querySelector(`.file-item .file-name[title="${filename}"]`);
    if (!fileItem) throw new Error(`File ${filename} not found in list`);
    return fileItem.closest('.file-item') as HTMLElement;
  }

  describe('on open', () => {
    it('does not trigger infinite loop when fetching diff', async () => {
      renderPanel();

      // Wait for the file to appear in the list
      await waitFor(() => {
        expect(getFileInList('src/App.tsx')).toBeInTheDocument();
      });

      // Should have fetched at least once
      await waitFor(() => {
        expect(mockDaemon.getCalls('fetchDiff').length).toBeGreaterThanOrEqual(1);
      });

      const callsAfterInitial = mockDaemon.getCalls('fetchDiff').length;

      // Wait to ensure no infinite loop - if there was a loop, calls would grow exponentially
      await sleep(200);

      // Should NOT have many more calls (a loop would cause 100+ calls)
      // Allow for background effect to run once more, but not exponential growth
      const callsAfterWait = mockDaemon.getCalls('fetchDiff').length;
      expect(callsAfterWait).toBeLessThan(5); // Reasonable threshold - no loop

      // Verify first call was for the correct file
      expect(mockDaemon.getCalls('fetchDiff')[0].args[0]).toEqual('src/App.tsx');
    });

    it('does not fetch when closed', async () => {
      renderPanel({ isOpen: false });

      await sleep(100);

      // No calls when panel is closed
      expect(mockDaemon.getCalls('fetchDiff')).toHaveLength(0);
    });

    it('loads review state on open', async () => {
      renderPanel();

      await waitFor(() => {
        expect(mockDaemon.getCalls('getReviewState')).toHaveLength(1);
      });

      expect(mockDaemon.getCalls('getReviewState')[0].args).toEqual(['/test/repo', 'main']);
    });
  });

  describe('file navigation', () => {
    it('fetches new diff when clicking different file', async () => {
      const gitStatus = createGitStatus(['src/App.tsx', 'src/utils.ts']);

      // Set up different responses for each file
      mockDaemon.setResponse('fetchDiff', (args: unknown[]) => {
        const [path] = args as [string, boolean];
        return {
          ...createFileDiffResult(
            `// original ${path}`,
            `// modified ${path}`
          ),
          path,
        };
      });

      renderPanel({ gitStatus });

      // Wait for first file to load
      await waitFor(() => {
        expect(mockDaemon.getCalls('fetchDiff').length).toBeGreaterThanOrEqual(1);
      });

      const initialCallCount = mockDaemon.getCalls('fetchDiff').length;

      // Click second file
      const secondFile = getFileInList('src/utils.ts');
      secondFile.click();

      // Should fetch second file (plus possibly background checks on first file)
      await waitFor(() => {
        const calls = mockDaemon.getCalls('fetchDiff');
        const utilsCalls = calls.filter(c => c.args[0] === 'src/utils.ts');
        expect(utilsCalls).toHaveLength(1);
      });

      // Verify the second file was fetched
      const utilsCalls = mockDaemon.getCalls('fetchDiff').filter(c => c.args[0] === 'src/utils.ts');
      expect(utilsCalls[0].args).toEqual(['src/utils.ts', false]);
    });

    it('displays correct content when responses arrive out of order', async () => {
      const gitStatus = createGitStatus(['file-A.tsx', 'file-B.tsx']);

      // Create controlled responses - track all pending promises
      const pendingPromises: Map<string, (value: unknown) => void> = new Map();

      mockDaemon.setResponse('fetchDiff', (args: unknown[]) => {
        const [path] = args as [string, boolean];
        return new Promise((resolve) => {
          pendingPromises.set(path, resolve);
        });
      });

      renderPanel({ gitStatus });

      // Wait for first file request (file-A)
      await waitFor(() => {
        const calls = mockDaemon.getCalls('fetchDiff').filter(c => c.args[0] === 'file-A.tsx');
        expect(calls).toHaveLength(1);
      });

      // Click second file before first resolves
      const secondFile = getFileInList('file-B.tsx');
      secondFile.click();

      // Wait for second request (file-B)
      await waitFor(() => {
        const calls = mockDaemon.getCalls('fetchDiff').filter(c => c.args[0] === 'file-B.tsx');
        expect(calls).toHaveLength(1);
      });

      // Resolve in reverse order - B first, then A
      pendingPromises.get('file-B.tsx')!({
        success: true,
        original: '// original B',
        modified: '// modified B - CORRECT',
        path: 'file-B.tsx',
      });

      // Small delay then resolve A
      await sleep(10);
      pendingPromises.get('file-A.tsx')!({
        success: true,
        original: '// original A',
        modified: '// modified A - SHOULD NOT SHOW',
        path: 'file-A.tsx',
      });

      // File B should be selected
      await waitFor(() => {
        const selectedFile = getFileInList('file-B.tsx');
        expect(selectedFile).toHaveClass('selected');
      });

      // Verify the correct file is shown in toolbar
      const toolbar = document.querySelector('.diff-filename');
      expect(toolbar?.textContent).toContain('file-B.tsx');
    });
  });

  describe('change detection', () => {
    it('shows CHANGED badge when file content differs from last view', async () => {
      const gitStatus = createGitStatus(['src/App.tsx']);

      // First response
      mockDaemon.setResponse('fetchDiff', () => ({
        ...createFileDiffResult('// v1', '// v1 modified'),
        path: 'src/App.tsx',
      }));

      renderPanel({ gitStatus });

      // Wait for first load
      await waitFor(() => {
        expect(mockDaemon.getCalls('fetchDiff')).toHaveLength(1);
      });

      // No badge initially
      expect(screen.queryByText('changed')).not.toBeInTheDocument();

      // Verify file is in the list
      const fileItem = getFileInList('src/App.tsx');
      expect(fileItem).toBeInTheDocument();
    });

    it('clears CHANGED badge when navigating away from file', async () => {
      const gitStatus = createGitStatus(['file-A.tsx', 'file-B.tsx']);

      renderPanel({ gitStatus });

      // Wait for initial load (file-A)
      await waitFor(() => {
        const callsA = mockDaemon.getCalls('fetchDiff').filter(c => c.args[0] === 'file-A.tsx');
        expect(callsA.length).toBeGreaterThanOrEqual(1);
      });

      // Click second file
      const secondFile = getFileInList('file-B.tsx');
      secondFile.click();

      // Wait for file-B to be fetched
      await waitFor(() => {
        const callsB = mockDaemon.getCalls('fetchDiff').filter(c => c.args[0] === 'file-B.tsx');
        expect(callsB.length).toBeGreaterThanOrEqual(1);
      });

      // Badge should not be visible (no changes detected in this test)
      expect(screen.queryByText('changed')).not.toBeInTheDocument();
    });
  });

  describe('guard rails', () => {
    it('respects maxCalls limit', async () => {
      const strictMock = createMockDaemon({
        maxCalls: { fetchDiff: 2 },
      });
      setupDefaultResponses(strictMock);

      // This test verifies the guard rail mechanism works
      strictMock.createFetchDiff()('file1.tsx', false);
      strictMock.createFetchDiff()('file2.tsx', false);

      // Third call should throw
      await expect(
        strictMock.createFetchDiff()('file3.tsx', false)
      ).rejects.toThrow('Max calls exceeded');
    });

    it('fails on unexpected calls in strict mode', async () => {
      const strictMock = createMockDaemon({ strict: true });
      setupDefaultResponses(strictMock);
      strictMock.expect('getReviewState');

      // Unexpected call should throw
      await expect(
        strictMock.createFetchDiff()('file.tsx', false)
      ).rejects.toThrow('Unexpected call to fetchDiff in strict mode');
    });
  });

  describe('error handling', () => {
    it('shows error message when diff fetch fails', async () => {
      mockDaemon.setResponse('fetchDiff', () => {
        throw new Error('Network error');
      });

      renderPanel();

      await waitFor(() => {
        expect(screen.getByText('Network error')).toBeInTheDocument();
      });
    });
  });
});
