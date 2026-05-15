import type { OpenPRProgressStep } from '../hooks/useOpenPR';
import './OpenPRLauncherProgress.css';

interface OpenPRLauncherProgressProps {
  repo: string;
  number: number;
  title: string;
  step: OpenPRProgressStep;
}

const STEPS: Array<{ step: OpenPRProgressStep; label: string }> = [
  { step: 'fetching_pr_details', label: 'Fetch PR' },
  { step: 'ensuring_repo', label: 'Sync repo' },
  { step: 'creating_worktree', label: 'Create worktree' },
  { step: 'starting_session', label: 'Start session' },
];

const STEP_COPY: Record<OpenPRProgressStep, string> = {
  fetching_pr_details: 'Fetching branch details',
  ensuring_repo: 'Ensuring local repository',
  creating_worktree: 'Creating worktree',
  starting_session: 'Starting session',
};

export function OpenPRLauncherProgress({ repo, number, title, step }: OpenPRLauncherProgressProps) {
  const activeIndex = Math.max(0, STEPS.findIndex((entry) => entry.step === step));

  return (
    <div className="open-pr-launcher" role="status" aria-live="polite" aria-label={`Opening PR ${number}`}>
      <div className="open-pr-launcher-header">
        <span className="open-pr-launcher-kicker">Opening PR</span>
        <span className="open-pr-launcher-target">{repo}#{number}</span>
      </div>
      <div className="open-pr-launcher-title">{title}</div>
      <div className="open-pr-launcher-current">
        <span className="open-pr-launcher-pulse" aria-hidden="true" />
        <span>{STEP_COPY[step]}</span>
      </div>
      <div className="open-pr-launcher-steps" aria-hidden="true">
        {STEPS.map((entry, index) => {
          const state = index < activeIndex ? 'done' : index === activeIndex ? 'active' : 'pending';
          return (
            <div key={entry.step} className={`open-pr-launcher-step ${state}`}>
              <span className="open-pr-launcher-step-dot" />
              <span>{entry.label}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
