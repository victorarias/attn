/**
 * DiffView Test Harness
 *
 * Renders DiffView (the @pierre/diffs wrapper) in isolation with mocked review
 * callbacks. Exposes window.__HARNESS__ controls for switching files, toggling
 * layout, and seeding comments.
 */
import { useCallback, useEffect, useState } from 'react';
import { DiffView } from '../../src/components/DiffView';
import type { ReviewComment } from '../../src/types/generated';
import type { HarnessProps } from '../types';

const SMALL_ORIGINAL = `function example() {
  console.log('line 1');
  console.log('line 2 - will be deleted');
  console.log('line 3 - will be deleted');
  console.log('line 4');
  console.log('line 5');
}`;

const SMALL_MODIFIED = `function example() {
  console.log('line 1');
  console.log('line 4');
  console.log('new line - added');
  console.log('line 5');
}`;

const FILE_B_ORIGINAL = `class Calculator {
  add(a: number, b: number) {
    return a + b;
  }
  multiply(a: number, b: number) {
    return a * b;
  }
}`;

const FILE_B_MODIFIED = `class Calculator {
  add(a: number, b: number) {
    return a + b;
  }
  subtract(a: number, b: number) {
    return a - b;
  }
  multiply(a: number, b: number) {
    return a * b;
  }
}`;

function generateLines(count: number, prefix: string): string {
  return Array.from({ length: count }, (_, i) => `  console.log('${prefix} ${i + 1}');`).join('\n');
}

// A larger diff with several hunks so collapsing/expansion can be exercised.
const LARGE_ORIGINAL = `function example() {
  // Section 1
${generateLines(12, 'section1')}
  console.log('deleted A1');
  console.log('deleted A2');
  // Section 2
${generateLines(20, 'section2')}
  console.log('deleted B1');
  // Section 3
${generateLines(20, 'section3')}
}`;

const LARGE_MODIFIED = `function example() {
  // Section 1
${generateLines(12, 'section1')}
  // Section 2
${generateLines(20, 'section2')}
  // Section 3
${generateLines(20, 'section3')}
  console.log('new tail line');
}`;

function makeComment(overrides: Partial<ReviewComment>): ReviewComment {
  return {
    id: `comment-${Math.round(performance.now() * 1000)}-${Math.floor(Math.random() * 1e6)}`,
    review_id: 'harness-review',
    filepath: 'fileA.ts',
    line_start: 4,
    line_end: 4,
    content: 'Seeded comment',
    author: 'user',
    resolved: false,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

export function DiffViewHarness({ onReady }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const seed = params.get('seed') !== '0';

  const [comments, setComments] = useState<ReviewComment[]>(
    seed ? [makeComment({ id: 'seeded-1', content: 'Seeded comment on an added line' })] : []
  );
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);
  const [diffStyle, setDiffStyle] = useState<'unified' | 'split'>('unified');
  const [expandUnchanged, setExpandUnchanged] = useState(false);
  const [useLargeDiff, setUseLargeDiff] = useState(false);
  const [filePath, setFilePath] = useState('fileA.ts');
  const [refreshCount, setRefreshCount] = useState(0);
  const [shrunk, setShrunk] = useState(false);

  const baseOriginal = filePath === 'fileB.ts' ? FILE_B_ORIGINAL : useLargeDiff ? LARGE_ORIGINAL : SMALL_ORIGINAL;
  const baseModified = filePath === 'fileB.ts' ? FILE_B_MODIFIED : useLargeDiff ? LARGE_MODIFIED : SMALL_MODIFIED;
  // `shrunk` collapses the file to a single line so any comment on a higher line
  // becomes stale (its anchor no longer exists) — mirrors the code changing a lot.
  const original = shrunk ? 'shrunk();' : refreshCount > 0 ? `${baseOriginal}\n// refresh-${refreshCount}` : baseOriginal;
  const modified = shrunk ? 'shrunk();' : refreshCount > 0 ? `${baseModified}\n// refresh-${refreshCount}` : baseModified;

  const onAddComment = useCallback((lineStart: number, lineEnd: number, content: string) => {
    window.__HARNESS__.recordCall('addComment', [lineStart, lineEnd, content]);
    setComments((prev) => [
      ...prev,
      makeComment({
        id: `comment-${prev.length + 1}`,
        filepath: filePath,
        line_start: lineStart,
        line_end: lineEnd,
        content,
      }),
    ]);
  }, [filePath]);

  const onEditComment = useCallback((id: string, content: string) => {
    window.__HARNESS__.recordCall('editComment', [id, content]);
    setComments((prev) => prev.map((c) => (c.id === id ? { ...c, content } : c)));
    setEditingCommentId(null);
  }, []);

  const onStartEdit = useCallback((id: string) => {
    window.__HARNESS__.recordCall('startEdit', [id]);
    setEditingCommentId(id);
  }, []);

  const onCancelEdit = useCallback(() => {
    window.__HARNESS__.recordCall('cancelEdit', []);
    setEditingCommentId(null);
  }, []);

  const onResolveComment = useCallback((id: string, resolved: boolean) => {
    window.__HARNESS__.recordCall('resolveComment', [id, resolved]);
    setComments((prev) =>
      prev.map((c) => (c.id === id ? { ...c, resolved, resolved_by: resolved ? 'user' : '' } : c))
    );
  }, []);

  const onDeleteComment = useCallback((id: string) => {
    window.__HARNESS__.recordCall('deleteComment', [id]);
    setComments((prev) => prev.filter((c) => c.id !== id));
  }, []);

  const onSendToClaude = useCallback((reference: string) => {
    window.__HARNESS__.recordCall('sendToClaude', [reference]);
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => onReady(), 400);
    return () => clearTimeout(timer);
  }, [onReady]);

  useEffect(() => {
    const api = window.__HARNESS__ as unknown as Record<string, unknown>;
    api.switchFile = (path: string) => setFilePath(path);
    api.refreshContent = () => setRefreshCount((c) => c + 1);
    api.setDiffStyle = (style: 'unified' | 'split') => setDiffStyle(style);
    api.setExpandUnchanged = (value: boolean) => setExpandUnchanged(value);
    api.setUseLargeDiff = (value: boolean) => setUseLargeDiff(value);
    // Simulate a background change that re-renders the diff without touching the
    // file content: a comment arrives on another line (as the agent or a poll
    // would deliver). The selected file's original/modified are unchanged.
    api.addBackgroundComment = () =>
      setComments((prev) => [
        ...prev,
        makeComment({
          id: `bg-${prev.length + 1}`,
          line_start: 2,
          line_end: 2,
          content: `background comment ${prev.length + 1}`,
        }),
      ]);
    // A comment whose anchor line is past the end of the file (already stale).
    api.seedStaleComment = () =>
      setComments((prev) => [
        ...prev,
        makeComment({ id: 'stale-1', line_start: 999, line_end: 999, content: 'Stale: this code is gone' }),
      ]);
    // Collapse the file so existing comments on higher lines go stale.
    api.shrinkContent = () => setShrunk(true);
  }, []);

  const controlsStyle: React.CSSProperties = {
    display: 'flex',
    gap: '16px',
    padding: '12px',
    backgroundColor: '#1e1e1e',
    borderBottom: '1px solid #3e4451',
    alignItems: 'center',
    flexWrap: 'wrap',
    color: '#abb2bf',
    fontSize: '13px',
  };

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={controlsStyle}>
        <label>
          <input
            type="radio"
            name="diffStyle"
            checked={diffStyle === 'unified'}
            onChange={() => setDiffStyle('unified')}
          />
          Unified
        </label>
        <label>
          <input
            type="radio"
            name="diffStyle"
            checked={diffStyle === 'split'}
            onChange={() => setDiffStyle('split')}
          />
          Split
        </label>
        <label>
          <input
            type="checkbox"
            checked={expandUnchanged}
            onChange={(e) => setExpandUnchanged(e.target.checked)}
          />
          Full file
        </label>
        <label>
          <input
            type="checkbox"
            checked={useLargeDiff}
            onChange={(e) => setUseLargeDiff(e.target.checked)}
          />
          Large diff
        </label>
      </div>

      <div style={{ flex: 1, minHeight: 0 }}>
        <DiffView
          original={original}
          modified={modified}
          filePath={filePath}
          comments={comments.filter((c) => c.filepath === filePath)}
          editingCommentId={editingCommentId}
          diffStyle={diffStyle}
          expandUnchanged={expandUnchanged}
          onAddComment={onAddComment}
          onEditComment={onEditComment}
          onStartEdit={onStartEdit}
          onCancelEdit={onCancelEdit}
          onResolveComment={onResolveComment}
          onDeleteComment={onDeleteComment}
          onSendToClaude={onSendToClaude}
        />
      </div>
    </div>
  );
}
