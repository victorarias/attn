import { useEffect, useMemo, useState } from 'react';
import type { ReviewLoopState } from '../hooks/useDaemonSocket';
import { useSettings } from '../contexts/SettingsContext';
import {
  BUILTIN_REVIEW_LOOP_PRESETS,
  parseSavedReviewLoopPresets,
  REVIEW_LOOP_SETTINGS_CUSTOM_PRESETS,
  REVIEW_LOOP_SETTINGS_LAST_ITERATIONS,
  REVIEW_LOOP_SETTINGS_LAST_PRESET,
  REVIEW_LOOP_SETTINGS_LAST_PROMPT,
} from '../utils/reviewLoopPresets';
import './SessionReviewLoopBar.css';

interface WaitingReviewSession {
  sessionId: string;
  label: string;
  loopState: ReviewLoopState;
}

interface SessionReviewLoopBarProps {
  sessionId: string;
  sessionLabel: string;
  loopState: ReviewLoopState | null;
  onClose: () => void;
  waitingReviewSessions: WaitingReviewSession[];
  onSelectSession: (sessionId: string) => void;
  onStart: (prompt: string, iterationLimit: number, presetId?: string) => Promise<void>;
  onStop: () => Promise<void>;
  onSetIterations: (iterationLimit: number) => Promise<void>;
  onAnswer: (loopId: string, interactionId: string, answer: string) => Promise<void>;
}

type LoopTone = 'idle' | 'running' | 'awaiting_user' | 'completed' | 'stopped' | 'error';

function toneForStatus(status?: string): LoopTone {
  switch (status) {
    case 'running':
      return 'running';
    case 'awaiting_user':
      return 'awaiting_user';
    case 'completed':
      return 'completed';
    case 'stopped':
      return 'stopped';
    case 'error':
      return 'error';
    default:
      return 'idle';
  }
}

function statusLabel(status?: string): string {
  switch (status) {
    case 'running':
      return 'Running';
    case 'awaiting_user':
      return 'Needs Input';
    case 'completed':
      return 'All Rounds Done';
    case 'stopped':
      return 'Stopped';
    case 'error':
      return 'Error';
    default:
      return 'Review Loop';
  }
}

function latestIterationLabel(loopState: ReviewLoopState | null): string {
  if (!loopState?.latest_iteration) {
    return loopState ? `Round ${loopState.iteration_count}/${loopState.iteration_limit}` : 'Not started';
  }
  return `Round ${loopState.latest_iteration.iteration_number}/${loopState.iteration_limit}`;
}

export function SessionReviewLoopBar({
  sessionId,
  sessionLabel,
  loopState,
  onClose,
  waitingReviewSessions,
  onSelectSession,
  onStart,
  onStop,
  onSetIterations,
  onAnswer,
}: SessionReviewLoopBarProps) {
  const { settings, setSetting } = useSettings();
  const savedCustomPresets = useMemo(
    () => parseSavedReviewLoopPresets(settings[REVIEW_LOOP_SETTINGS_CUSTOM_PRESETS]),
    [settings]
  );
  const presets = useMemo(
    () => [...BUILTIN_REVIEW_LOOP_PRESETS, ...savedCustomPresets],
    [savedCustomPresets]
  );
  const [logOpen, setLogOpen] = useState(false);
  const [composerOpen, setComposerOpen] = useState(false);
  const [selectedPresetId, setSelectedPresetId] = useState(settings[REVIEW_LOOP_SETTINGS_LAST_PRESET] || BUILTIN_REVIEW_LOOP_PRESETS[0].id);
  const selectedPreset = useMemo(
    () => presets.find((preset) => preset.id === selectedPresetId) ?? BUILTIN_REVIEW_LOOP_PRESETS[0],
    [presets, selectedPresetId]
  );
  const [prompt, setPrompt] = useState(settings[REVIEW_LOOP_SETTINGS_LAST_PROMPT] || selectedPreset.prompt);
  const [iterationLimit, setIterationLimit] = useState(Number(settings[REVIEW_LOOP_SETTINGS_LAST_ITERATIONS] || selectedPreset.iterationLimit));
  const [answer, setAnswer] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);

  const awaitingUser = loopState?.status === 'awaiting_user' && !!loopState.pending_interaction;
  const tone = toneForStatus(loopState?.status);
  const waitingSwitcherVisible = waitingReviewSessions.length > 1;
  const filesTouched = loopState?.latest_iteration?.files_touched ?? [];
  const latestSummary = loopState?.latest_iteration?.summary || loopState?.last_result_summary || '';
  const latestResultText = loopState?.latest_iteration?.result_text || '';
  const latestTrace = loopState?.latest_iteration?.assistant_trace_json || '';
  const reviewLoopModel = settings.review_loop_model || 'claude-sonnet-4-6';

  useEffect(() => {
    if (!presets.some((preset) => preset.id === selectedPresetId)) {
      setSelectedPresetId(BUILTIN_REVIEW_LOOP_PRESETS[0].id);
    }
  }, [presets, selectedPresetId]);

  useEffect(() => {
    if (!settings[REVIEW_LOOP_SETTINGS_LAST_PROMPT]) {
      setPrompt(selectedPreset.prompt);
    }
    if (!settings[REVIEW_LOOP_SETTINGS_LAST_ITERATIONS]) {
      setIterationLimit(selectedPreset.iterationLimit);
    }
  }, [selectedPreset, settings]);

  const persistDraft = (nextPrompt: string, nextIterations: number, nextPresetId: string) => {
    setSetting(REVIEW_LOOP_SETTINGS_LAST_PROMPT, nextPrompt);
    setSetting(REVIEW_LOOP_SETTINGS_LAST_ITERATIONS, String(nextIterations));
    setSetting(REVIEW_LOOP_SETTINGS_LAST_PRESET, nextPresetId);
  };

  const handlePresetChange = (presetId: string) => {
    setSelectedPresetId(presetId);
    const preset = presets.find((entry) => entry.id === presetId) ?? BUILTIN_REVIEW_LOOP_PRESETS[0];
    setPrompt(preset.prompt);
    setIterationLimit(preset.iterationLimit);
    persistDraft(preset.prompt, preset.iterationLimit, presetId);
  };

  const handleStart = async () => {
    const trimmedPrompt = prompt.trim();
    if (!trimmedPrompt || iterationLimit <= 0) {
      return;
    }
    setIsSubmitting(true);
    try {
      persistDraft(trimmedPrompt, iterationLimit, selectedPresetId);
      await onStart(trimmedPrompt, iterationLimit, selectedPresetId);
      setComposerOpen(false);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleStop = async () => {
    setIsSubmitting(true);
    try {
      await onStop();
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleSetIterations = async () => {
    if (iterationLimit <= 0) return;
    setIsSubmitting(true);
    try {
      persistDraft(prompt, iterationLimit, selectedPresetId);
      await onSetIterations(iterationLimit);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleAnswer = async () => {
    if (!loopState?.loop_id || !loopState.pending_interaction?.id || !answer.trim()) {
      return;
    }
    setIsSubmitting(true);
    try {
      await onAnswer(loopState.loop_id, loopState.pending_interaction.id, answer.trim());
      setAnswer('');
    } finally {
      setIsSubmitting(false);
    }
  };

  const renderLogBody = () => {
    if (latestTrace) {
      return latestTrace;
    }
    if (latestResultText) {
      return latestResultText;
    }
    if (loopState?.status === 'running') {
      return 'Waiting for persisted reviewer output from the current round.';
    }
    return 'No reviewer log captured yet for this loop.';
  };

  const showHeaderIterationControls = loopState?.status === 'running';
  const showHeaderStop = loopState?.status === 'running' || awaitingUser;
  const showHeaderComposerToggle = !loopState || (loopState.status !== 'running' && loopState.status !== 'awaiting_user');

  return (
      <div className="review-loop-drawer-panel" data-testid={`review-loop-drawer-${sessionId}`}>
        <div className="review-loop-drawer-header">
          <div className="review-loop-drawer-topline">
            <span className="review-loop-drawer-kicker">Review Loop</span>
            <button className="review-loop-close-btn" onClick={onClose}>Hide</button>
          </div>

          <div className={`review-loop-drawer-status review-loop-drawer-status--${tone}`}>
            {statusLabel(loopState?.status)}
          </div>

          <h3 className="review-loop-drawer-title">{sessionLabel}</h3>
          <p className="review-loop-drawer-subtitle">
            {loopState
              ? `${latestIterationLabel(loopState)} · model ${reviewLoopModel}`
              : `Configure and start an autonomous Claude review loop for this session.`}
          </p>

          <div className="review-loop-drawer-actions review-loop-drawer-actions--header">
            {showHeaderIterationControls && (
              <label className="review-loop-inline-field">
                <span>Iterations</span>
                <input
                  type="number"
                  min={1}
                  value={iterationLimit}
                  onChange={(e) => setIterationLimit(Number(e.target.value) || 1)}
                />
              </label>
            )}
            {showHeaderIterationControls && (
              <button className="review-loop-action review-loop-action--secondary" onClick={() => void handleSetIterations()} disabled={isSubmitting}>
                Update Limit
              </button>
            )}
            {showHeaderStop && (
              <button className="review-loop-action review-loop-action--danger" onClick={() => void handleStop()} disabled={isSubmitting}>
                Stop Loop
              </button>
            )}
            {showHeaderComposerToggle && (
              <button className="review-loop-action review-loop-action--primary" onClick={() => setComposerOpen((open) => !open)}>
                {composerOpen ? 'Hide Start Form' : loopState ? 'Restart Loop' : 'Start Review'}
              </button>
            )}
          </div>

          {waitingSwitcherVisible && (
            <div className="review-loop-waiting-switcher">
              {waitingReviewSessions.map((item) => (
                <button
                  key={item.sessionId}
                  className={`review-loop-switch-chip ${item.sessionId === sessionId ? 'active' : ''}`}
                  onClick={() => onSelectSession(item.sessionId)}
                >
                  <span className={`review-loop-switch-dot review-loop-switch-dot--${toneForStatus(item.loopState.status)}`} />
                  {item.label}
                  <span className="review-loop-switch-round">
                    {item.loopState.iteration_count}/{item.loopState.iteration_limit}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="review-loop-drawer-body">
          {loopState ? (
            <>
              <div className="review-loop-stat-grid">
                <div className="review-loop-stat-card">
                  <span className="review-loop-stat-label">Loop State</span>
                  <span className="review-loop-stat-value">{statusLabel(loopState.status)}</span>
                </div>
                <div className="review-loop-stat-card">
                  <span className="review-loop-stat-label">Iteration State</span>
                  <span className="review-loop-stat-value">
                    {loopState.latest_iteration?.status?.replace('_', ' ') || 'pending'}
                  </span>
                </div>
                <div className="review-loop-stat-card">
                  <span className="review-loop-stat-label">Pass Count</span>
                  <span className="review-loop-stat-value">{loopState.iteration_count}/{loopState.iteration_limit}</span>
                </div>
              </div>

              {latestSummary && (
                <section className="review-loop-panel-card">
                  <div className="review-loop-panel-header">
                    <h4>Latest Summary</h4>
                  </div>
                  <div className="review-loop-panel-content review-loop-panel-content--summary">
                    {latestSummary}
                  </div>
                </section>
              )}

              <section className="review-loop-panel-card">
                <div className="review-loop-panel-header">
                  <h4>Files Touched This Round</h4>
                  <span className="review-loop-panel-meta">
                    {filesTouched.length > 0 ? `${filesTouched.length} file${filesTouched.length === 1 ? '' : 's'}` : 'none yet'}
                  </span>
                </div>
                <div className="review-loop-panel-content">
                  {filesTouched.length > 0 ? (
                    <ul className="review-loop-file-list">
                      {filesTouched.map((file) => (
                        <li key={file}>{file}</li>
                      ))}
                    </ul>
                  ) : (
                    <div className="review-loop-empty-state">No persisted file list for the current round yet.</div>
                  )}
                </div>
              </section>

              {awaitingUser && loopState.pending_interaction && (
                <section className="review-loop-question-card">
                  <div className="review-loop-question-label">Reviewer asks</div>
                  <div className="review-loop-question-text">{loopState.pending_interaction.question}</div>
                  <textarea
                    className="review-loop-answer-box"
                    value={answer}
                    onChange={(e) => setAnswer(e.target.value)}
                    rows={4}
                    spellCheck={false}
                    placeholder="Type your answer to resume the same review loop..."
                  />
                  <div className="review-loop-answer-actions">
                    <button className="review-loop-action review-loop-action--danger" onClick={() => void handleStop()} disabled={isSubmitting}>
                      Stop Loop
                    </button>
                    <button className="review-loop-action review-loop-action--primary" onClick={() => void handleAnswer()} disabled={isSubmitting || !answer.trim()}>
                      Send Answer
                    </button>
                  </div>
                </section>
              )}

              <section className="review-loop-panel-card">
                <div className="review-loop-panel-header">
                  <h4>Reviewer Log</h4>
                  <button className="review-loop-panel-toggle" onClick={() => setLogOpen((open) => !open)}>
                    {logOpen ? 'Hide Log' : 'Show Log'}
                  </button>
                </div>
                {logOpen && (
                  <div className="review-loop-panel-content">
                    <pre className="review-loop-log-output">{renderLogBody()}</pre>
                  </div>
                )}
              </section>
            </>
          ) : (
            <section className="review-loop-panel-card review-loop-panel-card--empty">
              <div className="review-loop-panel-content">
                Start an autonomous Claude review loop. The reviewer runs in a separate SDK context and only interrupts when it needs an answer.
              </div>
            </section>
          )}

          {(composerOpen || !loopState) && !awaitingUser && (
            <section className="review-loop-panel-card">
              <div className="review-loop-panel-header">
                <h4>{loopState ? 'Restart Review Loop' : 'Start Review Loop'}</h4>
              </div>
              <div className="review-loop-panel-content review-loop-panel-content--form">
                <div className="review-loop-row">
                  <label className="review-loop-field">
                    <span>Preset</span>
                    <select value={selectedPresetId} onChange={(e) => handlePresetChange(e.target.value)}>
                      <optgroup label="Built-in">
                        {BUILTIN_REVIEW_LOOP_PRESETS.map((preset) => (
                          <option key={preset.id} value={preset.id}>{preset.name}</option>
                        ))}
                      </optgroup>
                      {savedCustomPresets.length > 0 && (
                        <optgroup label="Saved">
                          {savedCustomPresets.map((preset) => (
                            <option key={preset.id} value={preset.id}>{preset.name}</option>
                          ))}
                        </optgroup>
                      )}
                    </select>
                  </label>
                  <label className="review-loop-field review-loop-field--limit">
                    <span>Rounds</span>
                    <input
                      type="number"
                      min={1}
                      value={iterationLimit}
                      onChange={(e) => setIterationLimit(Number(e.target.value) || 1)}
                    />
                  </label>
                </div>
                <label className="review-loop-field review-loop-field--prompt">
                  <span>Prompt</span>
                  <textarea
                    value={prompt}
                    onChange={(e) => setPrompt(e.target.value)}
                    rows={7}
                    spellCheck={false}
                  />
                </label>
                <div className="review-loop-drawer-actions">
                  {loopState && (
                    <button className="review-loop-action review-loop-action--secondary" onClick={() => setComposerOpen(false)} disabled={isSubmitting}>
                      Cancel
                    </button>
                  )}
                  <button className="review-loop-action review-loop-action--primary" onClick={() => void handleStart()} disabled={isSubmitting || !prompt.trim()}>
                    {loopState ? 'Restart Review Loop' : 'Start Review Loop'}
                  </button>
                </div>
              </div>
            </section>
          )}
        </div>
      </div>
  );
}
