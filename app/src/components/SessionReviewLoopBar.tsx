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

interface SessionReviewLoopBarProps {
  sessionId: string;
  loopState: ReviewLoopState | null;
  onStart: (prompt: string, iterationLimit: number, presetId?: string) => Promise<void>;
  onStop: () => Promise<void>;
  onSetIterations: (iterationLimit: number) => Promise<void>;
  onAnswer: (loopId: string, interactionId: string, answer: string) => Promise<void>;
}

function isActiveStatus(status?: string): boolean {
  return status === 'running';
}

function statusLabel(status?: string): string {
  switch (status) {
    case 'running':
      return 'Running';
    case 'awaiting_user':
      return 'Needs Input';
    case 'completed':
      return 'Completed';
    case 'stopped':
      return 'Stopped';
    case 'error':
      return 'Error';
    default:
      return 'Idle';
  }
}

export function SessionReviewLoopBar({
  sessionId,
  loopState,
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

  useEffect(() => {
    if (!presets.some((preset) => preset.id === selectedPresetId)) {
      setSelectedPresetId(BUILTIN_REVIEW_LOOP_PRESETS[0].id);
    }
  }, [presets, selectedPresetId]);

  useEffect(() => {
    if (!composerOpen) return;
    if (!settings[REVIEW_LOOP_SETTINGS_LAST_PROMPT]) {
      setPrompt(selectedPreset.prompt);
    }
    if (!settings[REVIEW_LOOP_SETTINGS_LAST_ITERATIONS]) {
      setIterationLimit(selectedPreset.iterationLimit);
    }
  }, [composerOpen, selectedPreset, settings]);

  useEffect(() => {
    if (!loopState) return;
    if (isActiveStatus(loopState.status)) {
      setComposerOpen(false);
    }
  }, [loopState]);

  const active = isActiveStatus(loopState?.status);
  const awaitingUser = loopState?.status === 'awaiting_user' && !!loopState.pending_interaction;

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

  return (
    <div className="review-loop-bar" data-testid={`review-loop-bar-${sessionId}`}>
      <div className="review-loop-summary">
        <span className="review-loop-title">Review Loop</span>
        <span className={`review-loop-status review-loop-status--${loopState?.status ?? 'idle'}`}>
          {statusLabel(loopState?.status)}
        </span>
        {loopState && (
          <span className="review-loop-progress">
            Passes {loopState.iteration_count}/{loopState.iteration_limit}
          </span>
        )}
        {loopState?.stop_reason && !active && !awaitingUser && (
          <span className="review-loop-reason">{loopState.stop_reason.split('_').join(' ')}</span>
        )}
      </div>

      <div className="review-loop-actions">
        {active && loopState && (
          <>
            <label className="review-loop-inline-field">
              <span>Limit</span>
              <input
                type="number"
                min={1}
                value={iterationLimit}
                onChange={(e) => setIterationLimit(Number(e.target.value) || 1)}
              />
            </label>
            <button className="review-loop-btn secondary" onClick={() => void handleSetIterations()} disabled={isSubmitting}>
              Update Limit
            </button>
            <button className="review-loop-btn danger" onClick={() => void handleStop()} disabled={isSubmitting}>
              Stop Loop
            </button>
          </>
        )}

        {!active && !awaitingUser && (
          <button
            className="review-loop-btn primary"
            onClick={() => setComposerOpen((open) => !open)}
            disabled={isSubmitting}
          >
            {composerOpen ? 'Close' : loopState ? 'Restart Loop' : 'Start Loop'}
          </button>
        )}
      </div>

      {awaitingUser && loopState?.pending_interaction && (
        <div className="review-loop-composer">
          <div className="review-loop-field review-loop-field--prompt">
            <span>Question</span>
            <textarea
              value={loopState.pending_interaction.question}
              rows={3}
              readOnly
              spellCheck={false}
            />
          </div>
          <label className="review-loop-field review-loop-field--prompt">
            <span>Your Answer</span>
            <textarea
              value={answer}
              onChange={(e) => setAnswer(e.target.value)}
              rows={4}
              spellCheck={false}
            />
          </label>
          <div className="review-loop-composer-actions">
            <button className="review-loop-btn danger" onClick={() => void handleStop()} disabled={isSubmitting}>
              Stop Loop
            </button>
            <button className="review-loop-btn primary" onClick={() => void handleAnswer()} disabled={isSubmitting || !answer.trim()}>
              Send Answer
            </button>
          </div>
        </div>
      )}

      {composerOpen && !active && !awaitingUser && (
        <div className="review-loop-composer">
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
              <span>Iterations</span>
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
              rows={5}
              spellCheck={false}
            />
          </label>
          <div className="review-loop-settings-hint">
            Manage saved prompts in Settings.
          </div>
          <div className="review-loop-composer-actions">
            <button className="review-loop-btn secondary" onClick={() => setComposerOpen(false)} disabled={isSubmitting}>
              Cancel
            </button>
            <button className="review-loop-btn primary" onClick={() => void handleStart()} disabled={isSubmitting || !prompt.trim()}>
              Start Review Loop
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
