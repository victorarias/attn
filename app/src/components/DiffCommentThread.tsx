/**
 * DiffCommentThread — review-comment UI rendered into a @pierre/diffs native
 * line-annotation slot.
 *
 * The library renders whatever `renderAnnotation` returns as a slotted
 * (light-DOM) child of the diff custom element, so this component is styled by
 * the app's normal CSS (see DiffDetailPanel.css), not the diff's shadow styles.
 *
 * One thread groups every comment sharing the same (side, line) anchor, plus an
 * optional in-progress draft form for a brand new comment on that anchor.
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ReviewComment } from '../types/generated';

/** Rendered comment body; react-markdown escapes raw HTML by default. */
function CommentBody({ content }: { content: string }) {
  const remarkPlugins = useMemo(() => [remarkGfm], []);
  return (
    <div className="diff-comment-content">
      <ReactMarkdown remarkPlugins={remarkPlugins}>{content}</ReactMarkdown>
    </div>
  );
}

interface CommentFormProps {
  initialValue: string;
  saveLabel: string;
  onChange?: (content: string) => void;
  onSave: (content: string) => Promise<void> | void;
  onCancel: () => void;
}

function CommentForm({ initialValue, saveLabel, onChange, onSave, onCancel }: CommentFormProps) {
  const [value, setValue] = useState(initialValue);
  const ref = useRef<HTMLTextAreaElement>(null);

  // Focus the textarea and place the caret at the end when it opens.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);

  const submit = () => {
    const trimmed = value.trim();
    if (trimmed) onSave(trimmed);
  };

  return (
    <div className="diff-comment-form" data-testid="diff-comment-form">
      <textarea
        ref={ref}
        className="diff-comment-textarea"
        rows={2}
        placeholder="Add a comment..."
        value={value}
        onChange={(e) => {
          setValue(e.target.value);
          onChange?.(e.target.value);
        }}
        // Keep keystrokes from reaching the panel's global shortcut handlers.
        // Escape is handled by the parent via the escape stack (capture phase),
        // so it never reaches here.
        onKeyDown={(e) => {
          e.stopPropagation();
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            submit();
          }
        }}
      />
      <div className="diff-comment-form-actions">
        <button className="cancel-btn" onClick={onCancel}>Cancel</button>
        <button className="save-btn" onClick={submit}>{saveLabel}</button>
      </div>
    </div>
  );
}

export interface DiffCommentThreadProps {
  /** Saved comments grouped at this annotation's (side, line) anchor. */
  comments: ReviewComment[];
  /** When true, render a new-comment draft form at the bottom of the thread. */
  draft: boolean;
  /** Id of the comment currently being edited, if any. */
  editingCommentId: string | null;
  /** Comment ids to render without Edit/Resolve/Delete actions (e.g. already-submitted comments in a read-only view). */
  readOnlyCommentIds?: Set<string>;
  /** Whether the per-comment "Send to CC" action is available. */
  showSendToClaude: boolean;
  draftContent?: string;
  onDraftContentChange?: (content: string) => void;
  onSaveDraft: (content: string) => Promise<void> | void;
  onCancelDraft: () => void;
  onStartEdit: (id: string) => void;
  onEditComment: (id: string, content: string) => void;
  onCancelEdit: () => void;
  onResolveComment: (id: string, resolved: boolean) => void;
  onDeleteComment: (id: string) => void;
  onSendComment: (comment: ReviewComment) => void;
}

export function DiffCommentThread({
  comments,
  draft,
  editingCommentId,
  readOnlyCommentIds,
  showSendToClaude,
  draftContent = '',
  onDraftContentChange,
  onSaveDraft,
  onCancelDraft,
  onStartEdit,
  onEditComment,
  onCancelEdit,
  onResolveComment,
  onDeleteComment,
  onSendComment,
}: DiffCommentThreadProps) {
  return (
    <div className="diff-comment-thread" data-testid="diff-comment-thread">
      {comments.map((comment) => {
        const isEditing = editingCommentId === comment.id;
        const isAgent = comment.author === 'agent';
        const isReadOnly = readOnlyCommentIds?.has(comment.id) ?? false;
        return (
          <div
            key={comment.id}
            className={`diff-comment ${comment.resolved ? 'resolved' : ''}`}
            data-comment-id={comment.id}
          >
            {isEditing ? (
              <CommentForm
                initialValue={comment.content}
                saveLabel="Save"
                onSave={(content) => onEditComment(comment.id, content)}
                onCancel={onCancelEdit}
              />
            ) : (
              <>
                <div className="diff-comment-header">
                  <div className="diff-comment-left">
                    <span className="diff-comment-author">{isAgent ? 'Claude' : 'You'}</span>
                    {comment.resolved && (
                      <span className="diff-comment-resolved">
                        Resolved by {comment.resolved_by === 'agent' ? 'Claude' : 'you'}
                      </span>
                    )}
                  </div>
                  <div className="diff-comment-actions">
                    {!isReadOnly && (
                      <button className="edit-btn" onClick={() => onStartEdit(comment.id)}>Edit</button>
                    )}
                    {showSendToClaude && (
                      <button className="send-btn" onClick={() => onSendComment(comment)}>Send to CC</button>
                    )}
                    {!isReadOnly && (
                      <button
                        className="resolve-btn"
                        onClick={() => onResolveComment(comment.id, !comment.resolved)}
                      >
                        {comment.resolved ? 'Unresolve' : 'Resolve'}
                      </button>
                    )}
                    {!isReadOnly && (
                      <button className="delete-btn" onClick={() => onDeleteComment(comment.id)}>Delete</button>
                    )}
                  </div>
                </div>
                <CommentBody content={comment.content} />
              </>
            )}
          </div>
        );
      })}
      {draft && (
        <CommentForm
          initialValue={draftContent}
          saveLabel="Save"
          onChange={onDraftContentChange}
          onSave={onSaveDraft}
          onCancel={onCancelDraft}
        />
      )}
    </div>
  );
}

export default DiffCommentThread;
