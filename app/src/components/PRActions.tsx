// app/src/components/PRActions.tsx
import { useState } from 'react';
import { usePRActions } from '../hooks/usePRActions';
import { useMuteStore } from '../store/mutes';
import './PRActions.css';

interface PRActionsProps {
  repo: string;
  number: number;
  prId: string;
  compact?: boolean;
  onMuted?: () => void;
}

export function PRActions({ repo, number, prId, compact = false, onMuted }: PRActionsProps) {
  const { approve, merge, getActionState } = usePRActions();
  const { mutePR } = useMuteStore();
  const [showMergeConfirm, setShowMergeConfirm] = useState(false);

  const approveState = getActionState(repo, number, 'approve');
  const mergeState = getActionState(repo, number, 'merge');

  const handleApprove = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      await approve(repo, number);
    } catch (err) {
      console.error('Approve failed:', err);
    }
  };

  const handleMerge = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setShowMergeConfirm(true);
  };

  const confirmMerge = async () => {
    setShowMergeConfirm(false);
    try {
      await merge(repo, number);
    } catch (err) {
      console.error('Merge failed:', err);
    }
  };

  const handleMute = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    mutePR(prId);
    onMuted?.();
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
        {renderButton('approve', approveState, handleApprove, 'Approve', '✓')}
        {renderButton('merge', mergeState, handleMerge, 'Merge', '⇋')}
        <button
          className={`pr-action-btn ${compact ? 'compact' : ''}`}
          data-action="mute"
          onClick={handleMute}
          title="Mute this PR"
        >
          {compact ? '⊘' : 'Mute'}
        </button>
      </div>

      {showMergeConfirm && (
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
        </div>
      )}
    </>
  );
}
