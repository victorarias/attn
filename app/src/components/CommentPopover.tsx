import { useState, useRef, useEffect } from 'react';
import type { ReviewComment } from '../types/generated';
import './CommentPopover.css';

interface CommentPopoverProps {
  // For new comments
  isNew?: boolean;
  lineStart?: number;
  lineEnd?: number;
  // For existing comments
  comment?: ReviewComment;
  // Actions
  onSave: (content: string) => Promise<void>;
  onCancel: () => void;
  onResolve?: (resolved: boolean) => Promise<void>;
  onDelete?: () => Promise<void>;
  onSendToClaude?: () => void;
  // Position
  position: { top: number; left: number };
}

export function CommentPopover({
  isNew = false,
  lineStart,
  lineEnd,
  comment,
  onSave,
  onCancel,
  onResolve,
  onDelete: _onDelete,
  onSendToClaude,
  position,
}: CommentPopoverProps) {
  // Note: onDelete is available in props for future use but not yet implemented in UI
  void _onDelete;
  const [content, setContent] = useState(comment?.content || '');
  const [isEditing, setIsEditing] = useState(isNew);
  const [isSaving, setIsSaving] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (isEditing && textareaRef.current) {
      textareaRef.current.focus();
    }
  }, [isEditing]);

  const handleSave = async () => {
    if (!content.trim()) return;
    setIsSaving(true);
    try {
      await onSave(content.trim());
      if (isNew) {
        onCancel(); // Close after creating new comment
      } else {
        setIsEditing(false);
      }
    } finally {
      setIsSaving(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      onCancel();
    } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSave();
    }
  };

  const formatTimestamp = (iso: string) => {
    const date = new Date(iso);
    return date.toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    });
  };

  return (
    <div
      className={`comment-popover ${comment?.resolved ? 'resolved' : ''}`}
      style={{ top: position.top, left: position.left }}
      onKeyDown={handleKeyDown}
    >
      {!isNew && comment && (
        <div className="comment-header">
          <span className={`comment-author ${comment.author}`}>
            {comment.author === 'agent' ? 'Claude' : 'You'}
          </span>
          <span className="comment-time">{formatTimestamp(comment.created_at)}</span>
          {comment.resolved && <span className="comment-resolved-badge">Resolved</span>}
        </div>
      )}

      {isNew && (
        <div className="comment-header">
          <span className="comment-lines">
            Line{lineStart !== lineEnd ? `s ${lineStart}-${lineEnd}` : ` ${lineStart}`}
          </span>
        </div>
      )}

      {isEditing ? (
        <textarea
          ref={textareaRef}
          className="comment-textarea"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="Add a comment..."
          rows={3}
        />
      ) : (
        <div className="comment-content">{comment?.content}</div>
      )}

      <div className="comment-actions">
        {isEditing ? (
          <>
            <button className="comment-btn cancel" onClick={onCancel}>
              Cancel
            </button>
            <button
              className="comment-btn save"
              onClick={handleSave}
              disabled={!content.trim() || isSaving}
            >
              {isSaving ? 'Saving...' : 'Save'}
            </button>
          </>
        ) : (
          <>
            <button className="comment-btn edit" onClick={() => setIsEditing(true)}>
              Edit
            </button>
            {onSendToClaude && (
              <button className="comment-btn send" onClick={onSendToClaude}>
                Send to CC
              </button>
            )}
            {onResolve && !comment?.resolved && (
              <button
                className="comment-btn resolve"
                onClick={() => onResolve(true)}
              >
                Resolve
              </button>
            )}
            {onResolve && comment?.resolved && (
              <button
                className="comment-btn unresolve"
                onClick={() => onResolve(false)}
              >
                Unresolve
              </button>
            )}
          </>
        )}
      </div>
    </div>
  );
}
