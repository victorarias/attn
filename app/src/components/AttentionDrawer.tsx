// app/src/components/AttentionDrawer.tsx
import { DaemonPR } from '../hooks/useDaemonSocket';
import { usePRsNeedingAttention } from '../hooks/usePRsNeedingAttention';
import { PRActions } from './PRActions';
import { StateIndicator } from './StateIndicator';
import { getRepoName } from '../utils/repo';
import './AttentionDrawer.css';

interface AttentionDrawerProps {
  isOpen: boolean;
  onClose: () => void;
  waitingSessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting_input' | 'idle' | 'pending_approval';
  }>;
  prs: DaemonPR[];
  onSelectSession: (id: string) => void;
}

export function AttentionDrawer({
  isOpen,
  onClose,
  waitingSessions,
  prs,
  onSelectSession,
}: AttentionDrawerProps) {
  const { reviewRequested: reviewPRs, yourPRs: authorPRs } = usePRsNeedingAttention(prs);

  const totalItems = waitingSessions.length + reviewPRs.length + authorPRs.length;

  return (
    <div className={`attention-drawer ${isOpen ? 'open' : ''}`}>
      <div className="drawer-header">
        <span className="drawer-title">Needs Attention</span>
        <span className="drawer-count">{totalItems}</span>
        <button className="drawer-close" onClick={onClose}>×</button>
      </div>

      <div className="drawer-body">
        {/* Waiting Sessions (local) */}
        {waitingSessions.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Sessions Waiting
              <span className="section-count">{waitingSessions.length}</span>
            </div>
            {waitingSessions.map((s) => (
              <div
                key={s.id}
                className="attention-item clickable"
                data-testid={`attention-session-${s.id}`}
                data-state={s.state}
                onClick={() => onSelectSession(s.id)}
              >
                <StateIndicator state={s.state} size="sm" kind="session" />
                <span className="item-name">{s.label}</span>
              </div>
            ))}
          </div>
        )}


        {/* PRs - Review Requested */}
        {reviewPRs.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Review Requested
              <span className="section-count">{reviewPRs.length}</span>
            </div>
            {reviewPRs.map((pr) => (
              <div key={pr.id} className="attention-item pr-item">
                <a
                  href={pr.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="pr-link"
                >
                  <div className="pr-meta">
                    <StateIndicator state="waiting_input" size="sm" kind="pr" />
                    <span className="pr-repo">{getRepoName(pr.repo)}</span>
                    <span className="pr-number">#{pr.number}</span>
                  </div>
                  <span className="pr-title-full">{pr.title}</span>
                </a>
                <div className="pr-footer">
                  <span />
                  <PRActions repo={pr.repo} number={pr.number} prId={pr.id} compact />
                </div>
              </div>
            ))}
          </div>
        )}

        {/* PRs - Your PRs */}
        {authorPRs.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Your PRs
              <span className="section-count">{authorPRs.length}</span>
            </div>
            {authorPRs.map((pr) => (
              <div key={pr.id} className="attention-item pr-item">
                <a
                  href={pr.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="pr-link"
                >
                  <div className="pr-meta">
                    <StateIndicator state="waiting_input" size="sm" kind="pr" />
                    <span className="pr-repo">{getRepoName(pr.repo)}</span>
                    <span className="pr-number">#{pr.number}</span>
                  </div>
                  <span className="pr-title-full">{pr.title}</span>
                </a>
                <div className="pr-footer">
                  <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
                  <PRActions repo={pr.repo} number={pr.number} prId={pr.id} compact />
                </div>
              </div>
            ))}
          </div>
        )}

        {totalItems === 0 && (
          <div className="drawer-empty">Nothing needs attention</div>
        )}
      </div>

      <div className="drawer-footer">
        <span className="shortcut"><kbd>⌘K</kbd> toggle</span>
        <span className="shortcut"><kbd>Esc</kbd> close</span>
      </div>
    </div>
  );
}
