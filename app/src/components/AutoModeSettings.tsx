// Auto mode settings: the policy a pi session launches with, the two lists a
// human edits here, the proposals waiting on a human, and what auto mode has
// refused.
//
// This section is auto mode's trust boundary made visible. An agent can propose
// a pattern or a model over the CLI and nothing happens; a person promoting one
// here — or typing one straight into a list here — is what puts it in front of
// the next pi session. That is why promote, discard and the two edit verbs
// exist on this transport alone, and why the effective policy is shown beside
// them rather than a page away.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md and
// docs/plans/2026-08-19-automode-direct-edit-and-settings-shape.md.

import { useState } from 'react';
import type { AutoModeDenialInfo, AutoModeProposalInfo } from '../hooks/daemonAutoModeEvents';
import type { AutoModePatternList, AutoModePolicy } from '../hooks/useAutoModePolicy';
import './AutoModeSettings.css';

interface AutoModeSettingsProps {
  policy: AutoModePolicy;
}

export function AutoModeSettings({ policy }: AutoModeSettingsProps) {
  const { state, error, loading, resolvingID, refresh, promote, discard } = policy;

  if (error && !state) {
    return (
      <section className="settings-block">
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
        <div className="settings-block-body">
          <div className="automode-state" data-testid="automode-loading">
            Reading auto mode…
          </div>
        </div>
      </section>
    );
  }

  const { config, proposals, denials } = state;
  const shipped = new Set(config.shipped_hard_deny);

  return (
    <section className="settings-block" data-testid="settings-automode">
      <div className="settings-block-body">
        {error && <span className="settings-warning">{error}</span>}

        <div className="automode-section-head">
          <h4>Effective policy</h4>
          <p className="settings-description">
            What a pi session launches with today. Prose and models are edited
            from <code>attn automode</code>.
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
          {renderPatterns(
            'Environment',
            'automode-environment',
            config.environment,
            'The classifier is told nothing about this machine.',
          )}
        </div>

        <div className="automode-section-head">
          <h4>Allowed</h4>
          <p className="settings-description">
            Narrow patterns that skip the classifier and run. A pattern that
            names nothing is refused — a blanket allow is what the classifier
            exists to replace.
          </p>
        </div>
        <PatternEditor
          list="allow"
          testID="automode-allow"
          values={config.allow}
          shipped={new Set<string>()}
          empty="Nothing skips the classifier."
          placeholder="git status*"
          policy={policy}
        />

        <div className="automode-section-head">
          <h4>Hard denied</h4>
          <p className="settings-description">
            Refused before anything else looks at the call. The built-in entries
            are what stop a session under auto mode from rewriting its own
            policy; they are not stored and cannot be removed.
          </p>
        </div>
        <PatternEditor
          list="hard_deny"
          testID="automode-hard-deny"
          values={config.hard_deny}
          shipped={shipped}
          empty="Nothing is refused before the classifier sees it."
          placeholder="*rm -rf /*"
          policy={policy}
        />

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
          <h4>Recent denials</h4>
          <p className="settings-description">
            What auto mode refused, newest first, and which layer decided.
          </p>
        </div>
        {renderDenials(denials)}

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
            An edit or a promotion reaches the next session that launches; a
            running one keeps the policy it started with.
          </span>
        </div>
      </div>
    </section>
  );
}

interface PatternEditorProps {
  list: AutoModePatternList;
  testID: string;
  values: string[];
  /** Entries auto mode resolves in at read: shown as built-in, never removable. */
  shipped: Set<string>;
  empty: string;
  placeholder: string;
  policy: AutoModePolicy;
}

/**
 * One editable pattern list. The refusal a rejected add or remove carries is
 * printed here, under the input that caused it, rather than raised to the
 * section — a validation message a re-read would wipe out is a message nobody
 * reads.
 */
function PatternEditor({
  list, testID, values, shipped, empty, placeholder, policy,
}: PatternEditorProps) {
  const [draft, setDraft] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editingList !== null;

  const submit = async () => {
    const pattern = draft.trim();
    if (pattern === '') return;
    setFailure(null);
    try {
      await policy.addPattern(list, pattern);
      setDraft('');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not add the pattern');
    }
  };

  const remove = async (pattern: string) => {
    setFailure(null);
    try {
      await policy.removePattern(list, pattern);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not remove the pattern');
    }
  };

  return (
    <div className="automode-editor" data-testid={testID}>
      {values.length === 0 ? (
        <span className="settings-hint">{empty}</span>
      ) : (
        <ul className="automode-patterns">
          {values.map((value) => {
            const builtIn = shipped.has(value);
            return (
              <li
                key={value}
                className={`automode-pattern-row${builtIn ? ' is-builtin' : ''}`}
                data-testid={`${testID}-entry`}
              >
                <code className="automode-value">{value}</code>
                {builtIn ? (
                  <span className="settings-pill" data-testid={`${testID}-builtin`}>
                    built-in
                  </span>
                ) : (
                  <button
                    type="button"
                    className="settings-action danger"
                    data-testid={`${testID}-remove`}
                    aria-label={`Remove ${value}`}
                    disabled={busy}
                    onClick={() => void remove(value)}
                  >
                    Remove
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div className="automode-pattern-add">
        <input
          type="text"
          className="settings-input"
          data-testid={`${testID}-input`}
          value={draft}
          placeholder={placeholder}
          aria-label={list === 'allow' ? 'Allow pattern' : 'Hard deny pattern'}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          disabled={busy}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit();
          }}
        />
        <button
          type="button"
          className="settings-action"
          data-testid={`${testID}-add`}
          disabled={busy || draft.trim() === ''}
          onClick={() => void submit()}
        >
          Add
        </button>
      </div>
      {failure && (
        <span className="settings-warning" data-testid={`${testID}-error`}>
          {failure}
        </span>
      )}
    </div>
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

function renderDenials(denials: AutoModeDenialInfo[]) {
  if (denials.length === 0) {
    return (
      <div className="settings-empty" data-testid="automode-no-denials">
        Auto mode has refused nothing yet.
      </div>
    );
  }
  return (
    <div className="automode-denials" data-testid="automode-denials">
      {denials.map((denial) => (
        <div key={denial.id} className="automode-denial">
          <code className="automode-value">{denial.signature || denial.tool}</code>
          <span className="automode-denial-rule">
            <span className="settings-pill">{denial.rule || 'unattributed'}</span>
          </span>
          <span className="automode-proposal-origin">
            {denial.created_at ? formatStamp(denial.created_at) : ''}
          </span>
        </div>
      ))}
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
