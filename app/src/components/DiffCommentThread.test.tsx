import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DiffCommentThread } from './DiffCommentThread';
import type { ReviewComment } from '../types/generated';

function makeComment(overrides: Partial<ReviewComment> = {}): ReviewComment {
  return {
    id: 'comment-1',
    content: 'a comment',
    filepath: 'src/foo.ts',
    line_start: 1,
    line_end: 1,
    author: 'user',
    resolved: false,
    created_at: '2026-07-01T00:00:00Z',
    review_id: 'round-1',
    ...overrides,
  };
}

const noop = () => {};

describe('DiffCommentThread', () => {
  it('hides Edit/Resolve/Delete for a comment id in readOnlyCommentIds', () => {
    render(
      <DiffCommentThread
        comments={[makeComment({ id: 'read-only-1' })]}
        draft={false}
        editingCommentId={null}
        readOnlyCommentIds={new Set(['read-only-1'])}
        showSendToClaude={false}
        onSaveDraft={noop}
        onCancelDraft={noop}
        onStartEdit={noop}
        onEditComment={noop}
        onCancelEdit={noop}
        onResolveComment={noop}
        onDeleteComment={noop}
        onSendComment={noop}
      />
    );

    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resolve' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('shows Edit/Resolve/Delete for a comment id not in readOnlyCommentIds', () => {
    render(
      <DiffCommentThread
        comments={[makeComment({ id: 'editable-1' })]}
        draft={false}
        editingCommentId={null}
        readOnlyCommentIds={new Set(['some-other-id'])}
        showSendToClaude={false}
        onSaveDraft={noop}
        onCancelDraft={noop}
        onStartEdit={noop}
        onEditComment={noop}
        onCancelEdit={noop}
        onResolveComment={noop}
        onDeleteComment={noop}
        onSendComment={noop}
      />
    );

    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resolve' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('defaults to showing all actions when readOnlyCommentIds is undefined', () => {
    render(
      <DiffCommentThread
        comments={[makeComment()]}
        draft={false}
        editingCommentId={null}
        showSendToClaude={false}
        onSaveDraft={noop}
        onCancelDraft={noop}
        onStartEdit={noop}
        onEditComment={noop}
        onCancelEdit={noop}
        onResolveComment={noop}
        onDeleteComment={noop}
        onSendComment={vi.fn()}
      />
    );

    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resolve' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });
});
