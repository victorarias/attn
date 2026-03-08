import { useEffect, useMemo, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
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

interface ReviewLoopTraceEntry {
  kind: 'text' | 'tool';
  content?: string;
  tool?: string;
  command?: string;
  paths?: string[];
}

interface ReviewLoopTracePayload {
  entries: ReviewLoopTraceEntry[];
}

interface SessionReviewLoopBarProps {
  sessionId: string;
  sessionLabel: string;
  loopState: ReviewLoopState | null;
  getReviewLoopRun: (loopId: string) => Promise<{ success: boolean; state: ReviewLoopState | null }>;
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

function iterationStatusLabel(iteration?: ReviewLoopState['latest_iteration'] | null): string {
  if (!iteration?.status) {
    return 'pending';
  }
  return iteration.status.replace('_', ' ');
}

function renderBashCommand(command: string) {
  const lines = command.split('\n');
  return lines.map((line, lineIndex) => (
    <div key={`${lineIndex}-${line}`} className="review-loop-bash-line">
      {line.split(/(\s+|"[^"]*"|'[^']*'|\$\w+|--?[a-zA-Z0-9_-]+)/g).filter(Boolean).map((part, tokenIndex) => {
        let className = 'review-loop-bash-token';
        if (/^#/.test(part.trim())) {
          className += ' review-loop-bash-token--comment';
        } else if (/^--?[a-zA-Z0-9_-]+$/.test(part)) {
          className += ' review-loop-bash-token--flag';
        } else if (/^".*"$|^'.*'$/.test(part)) {
          className += ' review-loop-bash-token--string';
        } else if (/^\$\w+$/.test(part)) {
          className += ' review-loop-bash-token--variable';
        } else if (tokenIndex === 0 && part.trim() !== '') {
          className += ' review-loop-bash-token--command';
        }
        return (
          <span key={`${tokenIndex}-${part}`} className={className}>
            {part}
          </span>
        );
      })}
    </div>
  ));
}

export function SessionReviewLoopBar({
  sessionId,
  sessionLabel,
  loopState,
  getReviewLoopRun,
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
  const [expandedToolEntries, setExpandedToolEntries] = useState<Record<number, boolean>>({});
  const [runDetails, setRunDetails] = useState<ReviewLoopState | null>(null);
  const [selectedIterationId, setSelectedIterationId] = useState<string | null>(null);
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
  const reviewLoopModel = settings.review_loop_model || 'claude-sonnet-4-6';
  const effectiveRun = runDetails ?? loopState;
  const iterations = effectiveRun?.iterations ?? (loopState?.latest_iteration ? [loopState.latest_iteration] : []);
  const selectedIteration = useMemo(() => {
    if (iterations.length === 0) {
      return loopState?.latest_iteration ?? null;
    }
    if (selectedIterationId) {
      const found = iterations.find((iteration) => iteration.id === selectedIterationId);
      if (found) return found;
    }
    return iterations[iterations.length - 1];
  }, [iterations, loopState?.latest_iteration, selectedIterationId]);
  const selectedIterationIndex = selectedIteration ? iterations.findIndex((iteration) => iteration.id === selectedIteration.id) : -1;
  const filesTouched = selectedIteration?.files_touched ?? [];
  const latestSummary = selectedIteration?.summary || effectiveRun?.last_result_summary || '';
  const latestResultText = selectedIteration?.result_text || '';
  const latestTrace = selectedIteration?.assistant_trace_json || '';
  const selectedChangeStats = selectedIteration?.change_stats ?? [];

  useEffect(() => {
    if (!presets.some((preset) => preset.id === selectedPresetId)) {
      setSelectedPresetId(BUILTIN_REVIEW_LOOP_PRESETS[0].id);
    }
  }, [presets, selectedPresetId]);

  useEffect(() => {
    if (!loopState?.loop_id) {
      setRunDetails(loopState);
      setSelectedIterationId(loopState?.latest_iteration?.id ?? null);
      return;
    }
    let cancelled = false;
    getReviewLoopRun(loopState.loop_id)
      .then((result) => {
        if (cancelled) return;
        setRunDetails(result.state ?? loopState);
      })
      .catch(() => {
        if (cancelled) return;
        setRunDetails(loopState);
      });
    return () => {
      cancelled = true;
    };
  }, [getReviewLoopRun, loopState]);

  useEffect(() => {
    if (!loopState) {
      setRunDetails(null);
      return;
    }
    setRunDetails((prev) => {
      if (!prev || prev.loop_id !== loopState.loop_id) {
        return loopState;
      }

      const next: ReviewLoopState = { ...prev, ...loopState };
      if (loopState.latest_iteration) {
        const iterations = prev.iterations ? [...prev.iterations] : [];
        const index = iterations.findIndex((iteration) => iteration.id === loopState.latest_iteration?.id);
        if (index >= 0) {
          iterations[index] = { ...iterations[index], ...loopState.latest_iteration };
        } else {
          iterations.push(loopState.latest_iteration);
        }
        iterations.sort((a, b) => a.iteration_number - b.iteration_number);
        next.iterations = iterations;
      }
      return next;
    });
  }, [loopState]);

  useEffect(() => {
    if (!selectedIteration) {
      setSelectedIterationId(loopState?.latest_iteration?.id ?? null);
      return;
    }
    if (!selectedIterationId) {
      setSelectedIterationId(selectedIteration.id);
    }
  }, [loopState?.latest_iteration?.id, selectedIteration, selectedIterationId]);

  useEffect(() => {
    if (loopState?.status === 'running') {
      setLogOpen(true);
    }
  }, [loopState?.status]);

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
    return latestTrace || latestResultText || (loopState?.status === 'running'
      ? 'Waiting for persisted reviewer output from the current round.'
      : 'No reviewer log captured yet for this loop.');
  };

  const traceEntries = useMemo<ReviewLoopTraceEntry[] | null>(() => {
    if (!latestTrace) {
      return null;
    }
    try {
      const parsed = JSON.parse(latestTrace) as ReviewLoopTracePayload;
      if (Array.isArray(parsed.entries)) {
        return parsed.entries.filter((entry) => entry && (entry.kind === 'text' || entry.kind === 'tool'));
      }
    } catch {
      // Fallback to plain-text log rendering below.
    }
    return null;
  }, [latestTrace]);

  const toggleToolEntry = (index: number) => {
    setExpandedToolEntries((prev) => ({ ...prev, [index]: !prev[index] }));
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

          {iterations.length > 1 && selectedIteration && (
            <div className="review-loop-iteration-nav">
              <button
                className="review-loop-iteration-nav-btn"
                onClick={() => setSelectedIterationId(iterations[Math.max(0, selectedIterationIndex - 1)]?.id ?? null)}
                disabled={selectedIterationIndex <= 0}
              >
                ‹
              </button>
              <span className="review-loop-iteration-nav-label">
                Iteration {selectedIteration.iteration_number} of {iterations.length}
              </span>
              <button
                className="review-loop-iteration-nav-btn"
                onClick={() => setSelectedIterationId(iterations[Math.min(iterations.length - 1, selectedIterationIndex + 1)]?.id ?? null)}
                disabled={selectedIterationIndex < 0 || selectedIterationIndex >= iterations.length - 1}
              >
                ›
              </button>
            </div>
          )}

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
                    {iterationStatusLabel(selectedIteration)}
                  </span>
                </div>
                <div className="review-loop-stat-card">
                  <span className="review-loop-stat-label">Pass Count</span>
                  <span className="review-loop-stat-value">
                    {selectedIteration?.iteration_number ?? loopState.iteration_count}/{loopState.iteration_limit}
                  </span>
                </div>
              </div>

              {latestSummary && (
                <section className="review-loop-panel-card review-loop-panel-card--summary">
                  <div className="review-loop-panel-header">
                    <h4>Latest Summary</h4>
                  </div>
                  <div className="review-loop-panel-content review-loop-panel-content--summary">
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>
                      {latestSummary}
                    </ReactMarkdown>
                  </div>
                </section>
              )}

              <section className="review-loop-panel-card review-loop-panel-card--files">
                <div className="review-loop-panel-header">
                  <h4>{selectedChangeStats.length > 0 ? 'Changed This Iteration' : 'Files Touched This Round'}</h4>
                  <span className="review-loop-panel-meta">
                    {filesTouched.length > 0 ? `${filesTouched.length} file${filesTouched.length === 1 ? '' : 's'}` : 'none yet'}
                  </span>
                </div>
                <div className="review-loop-panel-content">
                  {selectedChangeStats.length > 0 ? (
                    <ul className="review-loop-change-list">
                      {selectedChangeStats.map((file) => (
                        <li key={file.path} className="review-loop-change-item">
                          <span className="review-loop-change-path">{file.path}</span>
                          <span className="review-loop-change-stats">
                            <span className="review-loop-change-add">+{file.additions ?? 0}</span>
                            <span className="review-loop-change-del">-{file.deletions ?? 0}</span>
                          </span>
                        </li>
                      ))}
                    </ul>
                  ) : filesTouched.length > 0 ? (
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

              <section className={`review-loop-panel-card review-loop-panel-card--log ${logOpen ? 'is-expanded' : 'is-collapsed'}`}>
                <div className="review-loop-panel-header">
                  <h4>Reviewer Log</h4>
                  <button className="review-loop-panel-toggle" onClick={() => setLogOpen((open) => !open)}>
                    {logOpen ? 'Hide Log' : 'Show Log'}
                  </button>
                </div>
                {logOpen && (
                  <div className="review-loop-panel-content review-loop-panel-content--log">
                    {traceEntries ? (
                      <div className="review-loop-log-stream">
                        {traceEntries.map((entry, index) => {
                          if (entry.kind === 'tool') {
                            const isExpanded = Boolean(expandedToolEntries[index]);
                            return (
                              <div key={`${entry.tool || 'tool'}-${index}`} className="review-loop-log-tool">
                                <div className="review-loop-log-tool-header">
                                  <span className="review-loop-log-tool-name">{entry.tool || 'Tool'}</span>
                                  {entry.paths && entry.paths.length > 0 && (
                                    <div className="review-loop-log-tool-paths">
                                      {entry.paths.map((path) => (
                                        <span key={path} className="review-loop-log-tool-path">
                                          {path}
                                        </span>
                                      ))}
                                    </div>
                                  )}
                                  {entry.command && (
                                    <button
                                      className="review-loop-log-tool-toggle"
                                      onClick={() => toggleToolEntry(index)}
                                    >
                                      {isExpanded ? 'Hide Command' : 'Show Command'}
                                    </button>
                                  )}
                                </div>
                                {entry.command && isExpanded && (
                                  <div className="review-loop-log-command">
                                    {renderBashCommand(entry.command)}
                                  </div>
                                )}
                              </div>
                            );
                          }

                          return (
                            <div key={`text-${index}`} className="review-loop-log-entry">
                              <ReactMarkdown remarkPlugins={[remarkGfm]}>
                                {entry.content || ''}
                              </ReactMarkdown>
                            </div>
                          );
                        })}
                      </div>
                    ) : (
                      <div className="review-loop-log-entry review-loop-log-entry--fallback">
                        <ReactMarkdown remarkPlugins={[remarkGfm]}>
                          {renderLogBody()}
                        </ReactMarkdown>
                      </div>
                    )}
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
