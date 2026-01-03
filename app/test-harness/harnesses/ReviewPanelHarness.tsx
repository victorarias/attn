/**
 * ReviewPanel Test Harness
 *
 * Renders ReviewPanel with mocked daemon props for Playwright testing.
 * Exposes window.__HARNESS__ API for test control.
 */
import { useState, useEffect, useCallback } from 'react';
import { ReviewPanel } from '../../src/components/ReviewPanel';
import type { GitStatusUpdate, FileDiffResult, ReviewState } from '../../src/hooks/useDaemonSocket';
import type { ReviewComment } from '../../src/types/generated';
import type { HarnessProps } from '../types';
import '../../src/components/ReviewPanel.css';

// Sample diff with deleted lines - this is what we're testing
const DIFF_WITH_DELETIONS: FileDiffResult = {
  success: true,
  original: `function example() {
  console.log('line 1');
  console.log('deleted line A');
  console.log('deleted line B');
  console.log('deleted line C');
  console.log('line 5');
}`,
  modified: `function example() {
  console.log('line 1');
  console.log('line 5');
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

export function ReviewPanelHarness({ onReady, setTriggerRerender }: HarnessProps) {
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
  const fetchDiff = useCallback(async (_path: string, _staged: boolean): Promise<FileDiffResult> => {
    window.__HARNESS__.recordCall('fetchDiff', [_path, _staged]);
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
      getReviewState={getReviewState}
      markFileViewed={markFileViewed}
      addComment={addComment}
      getComments={getComments}
      resolveComment={resolveComment}
      deleteComment={deleteComment}
    />
  );
}
