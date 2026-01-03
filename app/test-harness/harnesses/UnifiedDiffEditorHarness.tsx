/**
 * UnifiedDiffEditor Test Harness
 *
 * Tests the unified diff approach where deleted lines are part of the document.
 */
import { useState, useEffect, useCallback } from 'react';
import { UnifiedDiffEditor, InlineComment } from '../../src/components/UnifiedDiffEditor';
import type { HarnessProps } from '../types';

// Sample diff content
const ORIGINAL = `function example() {
  console.log('line 1');
  console.log('line 2 - will be deleted');
  console.log('line 3 - will be deleted');
  console.log('line 4');
  console.log('line 5');
}`;

const MODIFIED = `function example() {
  console.log('line 1');
  console.log('line 4');
  console.log('new line - added');
  console.log('line 5');
}`;

export function UnifiedDiffEditorHarness({ onReady }: HarnessProps) {
  const [comments, setComments] = useState<InlineComment[]>([]);
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);

  // Mock addComment
  const addComment = useCallback(async (docLine: number, content: string) => {
    window.__HARNESS__.recordCall('addComment', [docLine, content]);
    const newComment: InlineComment = {
      id: `comment-${Date.now()}`,
      docLine,
      content,
      resolved: false,
      author: 'user',
    };
    setComments((prev) => [...prev, newComment]);
  }, []);

  // Mock editComment
  const editComment = useCallback(async (id: string, content: string) => {
    window.__HARNESS__.recordCall('editComment', [id, content]);
    setComments((prev) =>
      prev.map((c) => (c.id === id ? { ...c, content } : c))
    );
    setEditingCommentId(null);
  }, []);

  // Start editing
  const startEdit = useCallback((id: string) => {
    window.__HARNESS__.recordCall('startEdit', [id]);
    setEditingCommentId(id);
  }, []);

  // Cancel editing
  const cancelEdit = useCallback(() => {
    window.__HARNESS__.recordCall('cancelEdit', []);
    setEditingCommentId(null);
  }, []);

  // Mock resolveComment
  const resolveComment = useCallback(async (id: string, resolved: boolean) => {
    window.__HARNESS__.recordCall('resolveComment', [id, resolved]);
    setComments((prev) =>
      prev.map((c) => (c.id === id ? { ...c, resolved } : c))
    );
  }, []);

  // Mock deleteComment
  const deleteComment = useCallback(async (id: string) => {
    window.__HARNESS__.recordCall('deleteComment', [id]);
    setComments((prev) => prev.filter((c) => c.id !== id));
  }, []);

  useEffect(() => {
    // Give editor time to initialize
    const timer = setTimeout(() => onReady(), 300);
    return () => clearTimeout(timer);
  }, [onReady]);

  return (
    <div style={{ height: '400px', width: '100%' }}>
      <UnifiedDiffEditor
        original={ORIGINAL}
        modified={MODIFIED}
        comments={comments}
        editingCommentId={editingCommentId}
        language="javascript"
        onAddComment={addComment}
        onEditComment={editComment}
        onStartEdit={startEdit}
        onCancelEdit={cancelEdit}
        onResolveComment={resolveComment}
        onDeleteComment={deleteComment}
      />
    </div>
  );
}
