// Auto mode settings: the proposals waiting on a human, and the policy they
// resolve into.
//
// This section is auto mode's trust boundary made visible. An agent can propose
// a pattern or a model over the CLI and nothing happens; a person promoting one
// here is what puts it in front of the next pi session. That is why promote and
// discard exist on this transport alone, and why the effective policy is shown
// beside the list rather than a page away.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.

import type { AutoModeProposalInfo } from '../hooks/daemonAutoModeEvents';
import type { AutoModePolicy } from '../hooks/useAutoModePolicy';
import './AutoModeSettings.css';

interface AutoModeSettingsProps {
  policy: AutoModePolicy;
}

export function AutoModeSettings({ policy }: AutoModeSettingsProps) {
  const { state, error, loading, resolvingID, refresh, promote, discard } = policy;

  if (error && !state) {
    return (
      <section className="settings-block">
        {renderIntro()}
        <div className="settings-block-body">
          <div className="automode-state">
            <span className="settings-warning">{error}</span>
            <button type="button" className="settings-action" onClick={() => void refresh()}>
              Try again
            </button>
          </div>
        </div>
      </section>
    );
  }

  if (!state) {
    return (
      <section className="settings-block">
        {renderIntro()}
        <div className="settings-block-body">
          <div className="automode-state" data-testid="automode-loading">
            Reading auto mode…
          </div>
        </div>
      </section>
    );
  }

  const { config, proposals } = state;

  return (
    <section className="settings-block" data-testid="settings-automode">
      {renderIntro()}
      <div className="settings-block-body">
        {error && <span className="settings-warning">{error}</span>}

        <div className="automode-section-head">
          <h4>Proposed rules</h4>
          <p className="settings-description">
            An agent can propose a pattern or a model from <code>attn automode</code>;
            nothing it proposes changes what a session runs under until it is
            promoted here.
          </p>
        </div>

        {proposals.length === 0 ? (
          <div className="settings-empty" data-testid="automode-no-proposals">
            No proposals are waiting. Anything an agent proposes shows up here.
          </div>
        ) : (
          <div className="automode-proposals" data-testid="automode-proposals">
            {proposals.map((proposal) => (
              <div
                key={proposal.id}
                className="automode-proposal"
                data-testid={`automode-proposal-${proposal.id}`}
              >
                <span className="automode-proposal-subject">
                  <span className={`settings-pill ${proposal.kind === 'allow' ? 'warn' : ''}`}>
                    {proposalKindLabel(proposal)}
                  </span>
                  <code className="automode-value">{proposal.value}</code>
                </span>
                <span className="automode-proposal-origin">
                  {proposal.proposed_by || 'unattributed'}
                  {proposal.created_at && ` · ${formatStamp(proposal.created_at)}`}
                </span>
                <span className="automode-proposal-actions">
                  <button
                    type="button"
                    className="settings-action"
                    data-testid={`automode-promote-${proposal.id}`}
                    disabled={resolvingID !== null}
                    onClick={() => void promote(proposal.id)}
                  >
                    Promote
                  </button>
                  <button
                    type="button"
                    className="settings-action danger"
                    data-testid={`automode-discard-${proposal.id}`}
                    disabled={resolvingID !== null}
                    onClick={() => void discard(proposal.id)}
                  >
                    Discard
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}

        <div className="automode-section-head">
          <h4>Effective policy</h4>
          <p className="settings-description">
            What a pi session launches with today. Prose and models are edited
            from <code>attn automode</code>; patterns arrive here by promotion.
          </p>
        </div>

        <div className="automode-config" data-testid="automode-config">
          <div className="automode-field">
            <span className="automode-field-label">New sessions</span>
            <span className="automode-field-value">
              <span className={`settings-pill ${config.enabled_default ? 'good' : ''}`}>
                {config.enabled_default ? 'Auto mode on' : 'Auto mode off'}
              </span>
            </span>
          </div>
          {renderModels('Classifier', 'automode-classifier-models', config.classifier_models)}
          {renderModels('Escalation', 'automode-escalation-models', config.escalation_models)}
          {renderPatterns('Allowed', 'automode-allow', config.allow, 'Nothing skips the classifier.')}
          {renderPatterns(
            'Hard denied',
            'automode-hard-deny',
            config.hard_deny,
            'Nothing is refused before the classifier sees it.',
          )}
          {renderPatterns(
            'Environment',
            'automode-environment',
            config.environment,
            'The classifier is told nothing about this machine.',
          )}
        </div>

        <div className="automode-footer">
          <button
            type="button"
            className="settings-action"
            data-testid="automode-refresh"
            disabled={loading}
            onClick={() => void refresh()}
          >
            {loading ? 'Reading…' : 'Refresh'}
          </button>
          <span className="settings-hint">
            A promotion reaches the next session that launches; a running one keeps
            the policy it started with.
          </span>
        </div>
      </div>
    </section>
  );
}

/**
 * A layer's models, in the order pi walks them: the first one judges, and the
 * rest are only reached when the one before it cannot be. Marking which is
 * which is the whole difference between a fallback list and a list of models
 * somebody might expect to share the work.
 */
function renderModels(label: string, testID: string, models: string[]) {
  return (
    <div className="automode-field">
      <span className="automode-field-label">{label}</span>
      <span className="automode-field-value" data-testid={testID}>
        {models.length === 0 ? (
          <span className="settings-hint">No model can serve this layer.</span>
        ) : (
          <ul className="automode-patterns">
            {models.map((model, index) => (
              <li key={model}>
                <code className="automode-value automode-mono">{model}</code>
                <span className="settings-hint"> {index === 0 ? 'judges' : 'fallback'}</span>
              </li>
            ))}
          </ul>
        )}
      </span>
    </div>
  );
}

function renderPatterns(label: string, testID: string, values: string[], empty: string) {
  return (
    <div className="automode-field">
      <span className="automode-field-label">{label}</span>
      <span className="automode-field-value" data-testid={testID}>
        {values.length === 0 ? (
          <span className="settings-hint">{empty}</span>
        ) : (
          <ul className="automode-patterns">
            {values.map((value) => (
              <li key={value}>
                <code className="automode-value">{value}</code>
              </li>
            ))}
          </ul>
        )}
      </span>
    </div>
  );
}

/** `deny` and `allow` take no target; a model proposal names which model. */
function proposalKindLabel(proposal: AutoModeProposalInfo): string {
  if (proposal.kind === 'model') {
    return proposal.target ? `${proposal.target} model` : 'model';
  }
  return proposal.kind;
}

function formatStamp(value: string): string {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  return at.toLocaleString();
}

function renderIntro() {
  return (
    <div className="settings-block-intro">
      <span className="settings-kicker">Auto Mode</span>
      <h3>pi's safety envelope</h3>
      <p className="settings-description">
        Work inside a session's own directory runs free; anything reaching
        further is judged by a classifier against what the conversation asked
        for. Agents propose changes to that policy — you promote them.
      </p>
    </div>
  );
}
