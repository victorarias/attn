// app/src/components/PRActions.tsx
import { useState } from 'react';
import { createPortal } from 'react-dom';
import { useDaemonContext } from '../contexts/DaemonContext';
import './PRActions.css';

interface ActionState {
  loading: boolean;
  success: boolean;
  error: string | null;
}

interface PRActionsProps {
  number: number;
  prId: string;
  author?: string;
  compact?: boolean;
  onMuted?: () => void;
  onActionComplete?: (prId: string, action: 'approve' | 'merge') => void;
  onOpen?: () => void;
}

export function PRActions({ number, prId, author, compact = false, onMuted, onActionComplete, onOpen }: PRActionsProps) {
  const { sendPRAction, sendMutePR, sendMuteAuthor } = useDaemonContext();
  const [showMergeConfirm, setShowMergeConfirm] = useState(false);
  const [approveState, setApproveState] = useState<ActionState>({ loading: false, success: false, error: null });
  const [mergeState, setMergeState] = useState<ActionState>({ loading: false, success: false, error: null });

  const handleApprove = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setApproveState({ loading: true, success: false, error: null });
    try {
      const result = await sendPRAction('approve', prId);
      if (result.success) {
        setApproveState({ loading: false, success: true, error: null });
        // After showing checkmark briefly, notify parent to fade out
        setTimeout(() => {
          setApproveState({ loading: false, success: false, error: null });
          onActionComplete?.(prId, 'approve');
        }, 1500);
      } else {
        setApproveState({ loading: false, success: false, error: result.error || 'Failed' });
      }
    } catch (err) {
      console.error('Approve failed:', err);
      setApproveState({ loading: false, success: false, error: String(err) });
    }
  };

  const handleMerge = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setShowMergeConfirm(true);
  };

  const confirmMerge = async () => {
    setShowMergeConfirm(false);
    setMergeState({ loading: true, success: false, error: null });
    try {
      const result = await sendPRAction('merge', prId, 'squash');
      if (result.success) {
        setMergeState({ loading: false, success: true, error: null });
        // After showing checkmark briefly, notify parent to fade out
        setTimeout(() => {
          setMergeState({ loading: false, success: false, error: null });
          onActionComplete?.(prId, 'merge');
        }, 1500);
      } else {
        setMergeState({ loading: false, success: false, error: result.error || 'Failed' });
      }
    } catch (err) {
      console.error('Merge failed:', err);
      setMergeState({ loading: false, success: false, error: String(err) });
    }
  };

  const handleMute = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    sendMutePR(prId);
    onMuted?.();
  };

  const handleOpen = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    onOpen?.();
  };

  const renderButton = (
    action: string,
    state: { loading?: boolean; success?: boolean; error?: string | null } | undefined,
    onClick: (e: React.MouseEvent) => void,
    label: string,
    icon: string
  ) => {
    const isLoading = state?.loading;
    const isSuccess = state?.success;
    const hasError = state?.error;

    return (
      <button
        className={`pr-action-btn ${compact ? 'compact' : ''}`}
        data-testid={`${action}-button`}
        data-action={action}
        data-loading={isLoading}
        data-success={isSuccess}
        data-error={!!hasError}
        onClick={onClick}
        disabled={isLoading}
        title={hasError || label}
      >
        {isLoading ? (
          <span className="spinner" />
        ) : isSuccess ? (
          '✓'
        ) : (
          compact ? icon : label
        )}
      </button>
    );
  };

  return (
    <>
      <div className={`pr-actions ${compact ? 'compact' : ''}`}>
        {onOpen && (
          <button
            className={`pr-action-btn ${compact ? 'compact' : ''}`}
            data-testid="open-button"
            data-action="open"
            onClick={handleOpen}
            title="Open in worktree"
          >
            {compact ? '↗' : 'Open'}
          </button>
        )}
        {renderButton('approve', approveState, handleApprove, 'Approve', '✓')}
        {renderButton('merge', mergeState, handleMerge, 'Merge', '⇋')}
        <button
          className={`pr-action-btn ${compact ? 'compact' : ''}`}
          data-testid="mute-button"
          data-action="mute"
          onClick={handleMute}
          title="Mute this PR"
        >
          {compact ? '⊘' : 'Mute'}
        </button>
        {author && !compact && (
          <button
            className="author-mute-btn"
            data-testid="mute-author-button"
            data-action="mute-author"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              sendMuteAuthor(author);
            }}
            title={`Mute all PRs by ${author}`}
          >
            {author.toLowerCase().includes('bot') ? '🤖' : '👤'} {author}
          </button>
        )}
      </div>

      {showMergeConfirm && createPortal(
        <div className="merge-confirm-overlay" onClick={() => setShowMergeConfirm(false)}>
          <div className="merge-confirm-modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-title">Merge PR #{number}?</span>
            </div>
            <div className="modal-body">
              This will merge the pull request and delete the branch.
            </div>
            <div className="modal-footer">
              <button className="modal-btn modal-btn-cancel" onClick={() => setShowMergeConfirm(false)}>
                Cancel
              </button>
              <button className="modal-btn modal-btn-primary" onClick={confirmMerge}>
                Merge
              </button>
            </div>
          </div>
        </div>,
        document.body
      )}
    </>
  );
}
