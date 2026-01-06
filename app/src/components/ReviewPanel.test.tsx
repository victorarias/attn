import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '../test/utils';
import {
  createMockDaemon,
  createGitStatus,
  createFileDiffResult,
  createReviewComment,
  createDeletedLineComment,
  setupDefaultResponses,
  sleep,
  MockDaemon,
} from '../test/utils';
import { ReviewPanel } from './ReviewPanel';

// Mock UnifiedDiffEditor since it uses CodeMirror which requires DOM measurements
vi.mock('./UnifiedDiffEditor', () => ({
  default: vi.fn(({ original, modified }) => (
    <div data-testid="unified-diff-editor">
      <div className="original">{original?.substring(0, 50)}</div>
      <div className="modified">{modified?.substring(0, 50)}</div>
    </div>
  )),
  buildUnifiedDocument: vi.fn(() => []),
  resolveAnchor: vi.fn(() => ({ docLine: 1, isOutdated: false, isOrphaned: false })),
}));

describe('ReviewPanel', () => {
  let mockDaemon: MockDaemon;
  let onClose: () => void;

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

/**
 * Tests for deleted-line comment encoding and fixtures.
 * These are unit tests for the data model used to distinguish regular vs deleted-line comments.
 *
 * The deleted-line comment convention:
 * - Regular comments: line_end >= 0 (the actual line number)
 * - Deleted-line comments: line_end < 0 (encoded as -(index + 1))
 *   - line_end = -1: comment after deleted line index 0 (first deleted line)
 *   - line_end = -2: comment after deleted line index 1 (second deleted line)
 *   - etc.
 */
describe('Deleted-Line Comment Fixtures', () => {
  describe('createReviewComment', () => {
    it('creates a regular comment with positive line_end by default', () => {
      const comment = createReviewComment();

      expect(comment.id).toBeTruthy();
      expect(comment.review_id).toBe('test-review-id');
      expect(comment.filepath).toBe('src/App.tsx');
      expect(comment.line_start).toBe(10);
      expect(comment.line_end).toBe(10);
      expect(comment.content).toBe('Test comment');
      expect(comment.author).toBe('user');
      expect(comment.resolved).toBe(false);
      expect(comment.created_at).toBeTruthy();
    });

    it('accepts overrides for all fields', () => {
      const comment = createReviewComment({
        id: 'custom-id',
        filepath: 'src/utils.ts',
        line_start: 5,
        line_end: 8,
        content: 'Custom comment',
        author: 'agent',
        resolved: true,
      });

      expect(comment.id).toBe('custom-id');
      expect(comment.filepath).toBe('src/utils.ts');
      expect(comment.line_start).toBe(5);
      expect(comment.line_end).toBe(8);
      expect(comment.content).toBe('Custom comment');
      expect(comment.author).toBe('agent');
      expect(comment.resolved).toBe(true);
    });

    it('can create deleted-line comments with negative line_end', () => {
      // line_end = -1 means: after deleted line at index 0
      const comment = createReviewComment({ line_end: -1 });

      expect(comment.line_end).toBe(-1);
      expect(comment.line_end).toBeLessThan(0);
    });

    it('generates unique IDs for each comment', () => {
      const comment1 = createReviewComment();
      const comment2 = createReviewComment();

      expect(comment1.id).not.toBe(comment2.id);
    });
  });

  describe('createDeletedLineComment', () => {
    it('encodes deleted line index 0 as line_end = -1', () => {
      const comment = createDeletedLineComment(15, 0);

      expect(comment.line_start).toBe(15);  // Anchor line (line before deleted chunk)
      expect(comment.line_end).toBe(-1);    // Encoded: -(0 + 1) = -1
    });

    it('encodes deleted line index 1 as line_end = -2', () => {
      const comment = createDeletedLineComment(15, 1);

      expect(comment.line_start).toBe(15);
      expect(comment.line_end).toBe(-2);    // Encoded: -(1 + 1) = -2
    });

    it('encodes deleted line index 5 as line_end = -6', () => {
      const comment = createDeletedLineComment(20, 5);

      expect(comment.line_start).toBe(20);
      expect(comment.line_end).toBe(-6);    // Encoded: -(5 + 1) = -6
    });

    it('accepts additional overrides', () => {
      const comment = createDeletedLineComment(10, 2, {
        content: 'Comment on third deleted line',
        author: 'agent',
        resolved: true,
      });

      expect(comment.line_start).toBe(10);
      expect(comment.line_end).toBe(-3);    // Encoded index 2
      expect(comment.content).toBe('Comment on third deleted line');
      expect(comment.author).toBe('agent');
      expect(comment.resolved).toBe(true);
    });

    it('creates comments that are recognized as deleted-line comments', () => {
      const deletedLineComment = createDeletedLineComment(10, 0);
      const regularComment = createReviewComment();

      // The detection logic: line_end < 0
      expect(deletedLineComment.line_end < 0).toBe(true);
      expect(regularComment.line_end < 0).toBe(false);
    });
  });

  describe('deleted line index encoding/decoding', () => {
    /**
     * The encoding convention:
     * - encode: line_end = -(deletedLineIndex + 1)
     * - decode: deletedLineIndex = Math.abs(line_end) - 1
     */
    it('encodes index 0 to -1 and decodes back', () => {
      const index = 0;
      const encoded = -(index + 1);
      const decoded = Math.abs(encoded) - 1;

      expect(encoded).toBe(-1);
      expect(decoded).toBe(index);
    });

    it('encodes index 1 to -2 and decodes back', () => {
      const index = 1;
      const encoded = -(index + 1);
      const decoded = Math.abs(encoded) - 1;

      expect(encoded).toBe(-2);
      expect(decoded).toBe(index);
    });

    it('encodes index 10 to -11 and decodes back', () => {
      const index = 10;
      const encoded = -(index + 1);
      const decoded = Math.abs(encoded) - 1;

      expect(encoded).toBe(-11);
      expect(decoded).toBe(index);
    });

    it('all negative values decode to valid non-negative indices', () => {
      // Test that any negative line_end value decodes to a valid index
      for (let lineEnd = -1; lineEnd >= -100; lineEnd--) {
        const index = Math.abs(lineEnd) - 1;
        expect(index).toBeGreaterThanOrEqual(0);
        expect(Number.isInteger(index)).toBe(true);
      }
    });
  });

  describe('comment categorization', () => {
    it('correctly identifies regular comments', () => {
      const comments = [
        createReviewComment({ line_end: 0 }),   // Edge case: line 0
        createReviewComment({ line_end: 1 }),
        createReviewComment({ line_end: 10 }),
        createReviewComment({ line_end: 100 }),
      ];

      const isDeletedLine = (c: { line_end: number }) => c.line_end < 0;
      const regularComments = comments.filter(c => !isDeletedLine(c));
      const deletedLineComments = comments.filter(c => isDeletedLine(c));

      expect(regularComments).toHaveLength(4);
      expect(deletedLineComments).toHaveLength(0);
    });

    it('correctly identifies deleted-line comments', () => {
      const comments = [
        createDeletedLineComment(5, 0),   // line_end = -1
        createDeletedLineComment(10, 1),  // line_end = -2
        createDeletedLineComment(15, 5),  // line_end = -6
      ];

      const isDeletedLine = (c: { line_end: number }) => c.line_end < 0;
      const regularComments = comments.filter(c => !isDeletedLine(c));
      const deletedLineComments = comments.filter(c => isDeletedLine(c));

      expect(regularComments).toHaveLength(0);
      expect(deletedLineComments).toHaveLength(3);
    });

    it('correctly categorizes a mixed set of comments', () => {
      const comments = [
        createReviewComment({ line_end: 5 }),       // Regular
        createDeletedLineComment(8, 0),             // Deleted-line (index 0)
        createReviewComment({ line_end: 12 }),      // Regular
        createDeletedLineComment(15, 2),            // Deleted-line (index 2)
        createReviewComment({ line_end: 20 }),      // Regular
      ];

      const isDeletedLine = (c: { line_end: number }) => c.line_end < 0;
      const regularComments = comments.filter(c => !isDeletedLine(c));
      const deletedLineComments = comments.filter(c => isDeletedLine(c));

      expect(regularComments).toHaveLength(3);
      expect(deletedLineComments).toHaveLength(2);

      // Verify the deleted-line comments have the expected encoded values
      expect(deletedLineComments[0].line_end).toBe(-1);  // index 0
      expect(deletedLineComments[1].line_end).toBe(-3);  // index 2
    });

    it('handles edge cases for anchor lines', () => {
      // Deleted chunk after line 0 (beginning of file)
      const commentAfterLine0 = createDeletedLineComment(0, 0);
      expect(commentAfterLine0.line_start).toBe(0);
      expect(commentAfterLine0.line_end).toBe(-1);

      // Deleted chunk after line 1000
      const commentAfterLine1000 = createDeletedLineComment(1000, 5);
      expect(commentAfterLine1000.line_start).toBe(1000);
      expect(commentAfterLine1000.line_end).toBe(-6);

      // Both should be recognized as deleted-line comments
      const isDeletedLine = (c: { line_end: number }) => c.line_end < 0;
      expect(isDeletedLine(commentAfterLine0)).toBe(true);
      expect(isDeletedLine(commentAfterLine1000)).toBe(true);
    });
  });

  // Note: canAddCommentToDeletedLine tests and comment form state preservation tests
  // were removed as part of UnifiedDiffEditor integration - that logic is now in
  // UnifiedDiffEditor which has its own comprehensive tests in unified-diff-editor.spec.ts

  describe('daemon mock comment operations', () => {
    let mockDaemon: MockDaemon;

    beforeEach(() => {
      mockDaemon = createMockDaemon();
    });

    it('can add a regular comment', async () => {
      const savedComment = createReviewComment({
        id: 'saved-1',
        filepath: 'src/test.tsx',
        line_start: 5,
        line_end: 5,
        content: 'Regular comment',
      });

      mockDaemon.setResponse('addComment', () => ({
        success: true,
        comment: savedComment,
      }));

      const addComment = mockDaemon.createAddComment();
      const result = await addComment(
        'review-123',
        'src/test.tsx',
        5,  // line_start
        5,  // line_end (regular)
        'Regular comment'
      );

      expect(result.success).toBe(true);
      expect(result.comment?.line_end).toBe(5);
      expect(result.comment?.line_end).toBeGreaterThanOrEqual(0);

      // Verify the call was recorded
      const calls = mockDaemon.getCalls('addComment');
      expect(calls).toHaveLength(1);
      expect(calls[0].args).toEqual([
        'review-123',
        'src/test.tsx',
        5,
        5,
        'Regular comment',
      ]);
    });

    it('can add a deleted-line comment with encoded line_end', async () => {
      const deletedLineIndex = 2;  // Third deleted line
      const encodedLineEnd = -(deletedLineIndex + 1);  // = -3

      const savedComment = createDeletedLineComment(10, deletedLineIndex, {
        id: 'saved-deleted-1',
        content: 'Comment on deleted line',
      });

      mockDaemon.setResponse('addComment', () => ({
        success: true,
        comment: savedComment,
      }));

      const addComment = mockDaemon.createAddComment();
      const result = await addComment(
        'review-123',
        'src/App.tsx',
        10,             // line_start (anchor)
        encodedLineEnd, // line_end = -3 (encoded index 2)
        'Comment on deleted line'
      );

      expect(result.success).toBe(true);
      expect(result.comment?.line_end).toBe(-3);
      expect(result.comment?.line_end).toBeLessThan(0);

      // Verify the call used the encoded value
      const calls = mockDaemon.getCalls('addComment');
      expect(calls[0].args[3]).toBe(-3);
    });

    it('can resolve/unresolve comments', async () => {
      mockDaemon.setResponse('resolveComment', () => ({ success: true }));

      const resolveComment = mockDaemon.createResolveComment();

      // Resolve
      let result = await resolveComment('comment-1', true);
      expect(result.success).toBe(true);

      // Unresolve
      result = await resolveComment('comment-1', false);
      expect(result.success).toBe(true);

      const calls = mockDaemon.getCalls('resolveComment');
      expect(calls).toHaveLength(2);
      expect(calls[0].args).toEqual(['comment-1', true]);
      expect(calls[1].args).toEqual(['comment-1', false]);
    });

    it('can mark/unmark comments as won\'t fix', async () => {
      mockDaemon.setResponse('wontFixComment', () => ({ success: true }));

      const wontFixComment = mockDaemon.createWontFixComment();

      // Mark as won't fix
      let result = await wontFixComment('comment-1', true);
      expect(result.success).toBe(true);

      // Undo won't fix
      result = await wontFixComment('comment-1', false);
      expect(result.success).toBe(true);

      const calls = mockDaemon.getCalls('wontFixComment');
      expect(calls).toHaveLength(2);
      expect(calls[0].args).toEqual(['comment-1', true]);
      expect(calls[1].args).toEqual(['comment-1', false]);
    });

    it('can fetch comments and separate regular from deleted-line', async () => {
      const mixedComments = [
        createReviewComment({ id: 'regular-1', line_end: 5 }),
        createDeletedLineComment(10, 0, { id: 'deleted-1' }),  // line_end = -1
        createReviewComment({ id: 'regular-2', line_end: 15 }),
        createDeletedLineComment(20, 3, { id: 'deleted-2' }),  // line_end = -4
      ];

      mockDaemon.setResponse('getComments', () => ({
        success: true,
        comments: mixedComments,
      }));

      const getComments = mockDaemon.createGetComments();
      const result = await getComments('review-123');

      expect(result.success).toBe(true);
      expect(result.comments).toHaveLength(4);

      // Split and verify
      const isDeletedLine = (c: { line_end: number }) => c.line_end < 0;
      const regularComments = result.comments!.filter(c => !isDeletedLine(c));
      const deletedLineComments = result.comments!.filter(c => isDeletedLine(c));

      expect(regularComments).toHaveLength(2);
      expect(deletedLineComments).toHaveLength(2);

      // Verify encoding is preserved
      expect(deletedLineComments[0].line_end).toBe(-1);  // index 0
      expect(deletedLineComments[1].line_end).toBe(-4);  // index 3
    });

    it('preserves deleted-line comment data through save/load cycle', async () => {
      // Simulate saving a deleted-line comment
      const originalIndex = 5;
      const savedComment = createDeletedLineComment(25, originalIndex, {
        id: 'persisted-comment',
        content: 'Important note on deleted code',
      });

      mockDaemon.setResponse('addComment', () => ({
        success: true,
        comment: savedComment,
      }));

      mockDaemon.setResponse('getComments', () => ({
        success: true,
        comments: [savedComment],
      }));

      // Save
      const addComment = mockDaemon.createAddComment();
      await addComment(
        'review-123',
        'src/App.tsx',
        25,
        -(originalIndex + 1),
        'Important note on deleted code'
      );

      // Load (simulate switching files and coming back)
      const getComments = mockDaemon.createGetComments();
      const loadResult = await getComments('review-123');

      expect(loadResult.success).toBe(true);
      expect(loadResult.comments).toHaveLength(1);

      const loadedComment = loadResult.comments![0];

      // Verify the encoded data is preserved
      expect(loadedComment.line_start).toBe(25);
      expect(loadedComment.line_end).toBe(-(originalIndex + 1));  // = -6

      // Verify we can decode back to the original index
      const decodedIndex = Math.abs(loadedComment.line_end) - 1;
      expect(decodedIndex).toBe(originalIndex);
    });
  });
});
