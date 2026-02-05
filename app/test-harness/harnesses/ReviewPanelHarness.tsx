/**
 * ReviewPanel Test Harness
 *
 * Renders ReviewPanel with mocked daemon props for Playwright testing.
 * Exposes window.__HARNESS__ API for test control.
 */
import { useState, useEffect, useCallback, useRef } from 'react';
import { ReviewPanel } from '../../src/components/ReviewPanel';
import type { BranchDiffFilesResult, GitStatusUpdate, FileDiffResult, ReviewState } from '../../src/hooks/useDaemonSocket';
import type { ReviewComment } from '../../src/types/generated';
import type { HarnessProps } from '../types';
import '../../src/components/ReviewPanel.css';

// Generate lines with a marker at specific positions
function generateLines(count: number, prefix: string): string {
  return Array.from({ length: count }, (_, i) => `  console.log('${prefix} ${i + 1}');`).join('\n');
}

// Sample diff with MULTIPLE hunks spread throughout the file
// This creates scrollable content even in hunk-only view
const DIFF_WITH_DELETIONS: FileDiffResult = {
  success: true,
  original: `function example() {
  // Section 1
${generateLines(10, 'section1')}
  // DELETED HUNK 1
  console.log('deleted A1');
  console.log('deleted A2');
  console.log('deleted A3');
  // Section 2
${generateLines(15, 'section2')}
  // DELETED HUNK 2
  console.log('deleted B1');
  console.log('deleted B2');
  // Section 3
${generateLines(15, 'section3')}
  // DELETED HUNK 3
  console.log('deleted C1');
  console.log('deleted C2');
  console.log('deleted C3');
  console.log('deleted C4');
  // Section 4
${generateLines(10, 'section4')}
}`,
  modified: `function example() {
  // Section 1
${generateLines(10, 'section1')}
  // Section 2
${generateLines(15, 'section2')}
  // Section 3
${generateLines(15, 'section3')}
  // Section 4
${generateLines(10, 'section4')}
}`,
};

// Sample git status with modified file
const createGitStatus = (): GitStatusUpdate => ({
  directory: '/test/repo',
  staged: [],
  unstaged: [
    {
      path: 'src/example.ts',
      status: 'modified',
      additions: 2,
      deletions: 3,
    },
  ],
  untracked: [],
});

// Sample review state
const REVIEW_STATE: ReviewState = {
  review_id: 'test-review-123',
  repo_path: '/test/repo',
  branch: 'main',
  viewed_files: [],
};

function parseFilesParam(raw: string | null, fallback: string[]): string[] {
  if (!raw) return fallback;
  const files = raw
    .split(',')
    .map((f) => f.trim())
    .filter(Boolean);
  return files.length > 0 ? files : fallback;
}

function toBranchDiffResult(files: string[]): BranchDiffFilesResult {
  return {
    success: true,
    base_ref: 'origin/main',
    files: files.map((path) => ({
      path,
      status: 'modified',
      additions: 2,
      deletions: 3,
      has_uncommitted: false,
    })),
  };
}

export function ReviewPanelHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const fetchDelayMs = Number(params.get('fetchDelayMs') || '0');
  const fetchFail = params.get('fetchFail') === '1';
  const localFiles = parseFilesParam(params.get('localFiles'), ['src/example.ts']);
  const refreshedFiles = parseFilesParam(params.get('refreshedFiles'), localFiles);
  const remotesFetchedRef = useRef(false);

  const [savedComments, setSavedComments] = useState<ReviewComment[]>([]);
  // Force re-renders by changing props that trigger the editor effect
  // We use a state that gets passed to ReviewPanel and causes effect re-run
  const [, forceRender] = useState(0);

  // Register triggerRerender with harness API
  // This simulates what happens when the editor effect re-runs (e.g., font size change)
  // The component instance stays alive, preserving refs
  useEffect(() => {
    setTriggerRerender(() => {
      console.log('[Harness] Triggering re-render (preserves refs)');
      // Force React to re-render which may cause effects to re-run
      // This simulates natural state changes without destroying the component
      forceRender((n) => n + 1);
    });
  }, [setTriggerRerender]);

  // Mock fetchDiff - returns diff with deleted lines
  const fetchDiff = useCallback(async (_path: string, _options?: { staged?: boolean; baseRef?: string }): Promise<FileDiffResult> => {
    window.__HARNESS__.recordCall('fetchDiff', [_path, _options]);
    return DIFF_WITH_DELETIONS;
  }, []);

  // Mock getReviewState
  const getReviewState = useCallback(
    async (_repoPath: string, _branch: string): Promise<{ success: boolean; state?: ReviewState }> => {
      window.__HARNESS__.recordCall('getReviewState', [_repoPath, _branch]);
      return { success: true, state: REVIEW_STATE };
    },
    []
  );

  const sendFetchRemotes = useCallback(async (_repoPath: string): Promise<{ success: boolean; error?: string }> => {
    window.__HARNESS__.recordCall('sendFetchRemotes', [_repoPath]);
    remotesFetchedRef.current = false;
    if (fetchDelayMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, fetchDelayMs));
    }
    if (fetchFail) {
      return { success: false, error: 'mock fetch failed' };
    }
    remotesFetchedRef.current = true;
    return { success: true };
  }, [fetchDelayMs, fetchFail]);

  const sendGetBranchDiffFiles = useCallback(
    async (_repoPath: string): Promise<BranchDiffFilesResult> => {
      window.__HARNESS__.recordCall('sendGetBranchDiffFiles', [_repoPath]);
      return toBranchDiffResult(remotesFetchedRef.current ? refreshedFiles : localFiles);
    },
    [localFiles, refreshedFiles]
  );

  // Mock markFileViewed
  const markFileViewed = useCallback(
    async (_reviewId: string, _filepath: string, _viewed: boolean): Promise<{ success: boolean }> => {
      window.__HARNESS__.recordCall('markFileViewed', [_reviewId, _filepath, _viewed]);
      return { success: true };
    },
    []
  );

  // Mock addComment - THIS IS KEY: records calls for verification
  const addComment = useCallback(
    async (
      reviewId: string,
      filepath: string,
      lineStart: number,
      lineEnd: number,
      content: string
    ): Promise<{ success: boolean; comment?: ReviewComment }> => {
      window.__HARNESS__.recordCall('addComment', [reviewId, filepath, lineStart, lineEnd, content]);
      console.log('[Harness] addComment called:', { reviewId, filepath, lineStart, lineEnd, content });

      const newComment: ReviewComment = {
        id: `comment-${Date.now()}`,
        review_id: reviewId,
        filepath,
        line_start: lineStart,
        line_end: lineEnd,
        content,
        author: 'user',
        resolved: false,
        created_at: new Date().toISOString(),
      };

      setSavedComments((prev) => [...prev, newComment]);
      return { success: true, comment: newComment };
    },
    []
  );

  // Mock getComments
  const getComments = useCallback(
    async (_reviewId: string, _filepath?: string): Promise<{ success: boolean; comments?: ReviewComment[] }> => {
      window.__HARNESS__.recordCall('getComments', [_reviewId, _filepath]);
      return { success: true, comments: savedComments };
    },
    [savedComments]
  );

  // Mock resolveComment
  const resolveComment = useCallback(
    async (_commentId: string, _resolved: boolean): Promise<{ success: boolean }> => {
      window.__HARNESS__.recordCall('resolveComment', [_commentId, _resolved]);
      return { success: true };
    },
    []
  );

  // Mock deleteComment
  const deleteComment = useCallback(async (_commentId: string): Promise<{ success: boolean }> => {
    window.__HARNESS__.recordCall('deleteComment', [_commentId]);
    setSavedComments((prev) => prev.filter((c) => c.id !== _commentId));
    return { success: true };
  }, []);

  // Signal ready when mounted
  useEffect(() => {
    // Give CodeMirror time to initialize
    const timer = setTimeout(() => {
      onReady();
    }, 500);
    return () => clearTimeout(timer);
  }, [onReady]);

  return (
    <ReviewPanel
      isOpen={true}
      gitStatus={createGitStatus()}
      repoPath="/test/repo"
      branch="main"
      onClose={() => {
        window.__HARNESS__.recordCall('onClose', []);
      }}
      fetchDiff={fetchDiff}
      sendFetchRemotes={sendFetchRemotes}
      sendGetBranchDiffFiles={sendGetBranchDiffFiles}
      getReviewState={getReviewState}
      markFileViewed={markFileViewed}
      addComment={addComment}
      getComments={getComments}
      resolveComment={resolveComment}
      deleteComment={deleteComment}
    />
  );
}
