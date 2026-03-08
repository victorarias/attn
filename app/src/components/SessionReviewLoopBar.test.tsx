import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '../test/utils';
import { SessionReviewLoopBar } from './SessionReviewLoopBar';
import { SettingsProvider } from '../contexts/SettingsContext';

function renderBar(settings: Record<string, string> = {}) {
  const setSetting = vi.fn();
  const onStart = vi.fn(async () => {});
  const onStop = vi.fn(async () => {});
  const onSetIterations = vi.fn(async () => {});
  const onAnswer = vi.fn(async () => {});

  render(
    <SettingsProvider settings={settings} setSetting={setSetting}>
      <SessionReviewLoopBar
        sessionId="s1"
        sessionLabel="session one"
        loopState={null}
        getReviewLoopRun={vi.fn(async () => ({ success: true, state: null }))}
        onClose={vi.fn()}
        waitingReviewSessions={[]}
        onSelectSession={vi.fn()}
        onStart={onStart}
        onStop={onStop}
        onSetIterations={onSetIterations}
        onAnswer={onAnswer}
      />
    </SettingsProvider>
  );

  return { setSetting, onStart, onStop, onSetIterations, onAnswer };
}

describe('SessionReviewLoopBar', () => {
  it('shows saved custom presets from settings', () => {
    renderBar({
      review_loop_prompt_presets: JSON.stringify([
        { id: 'custom-architect', name: 'Architect Pass', prompt: 'Review as architect', iterationLimit: 4 },
      ]),
    });

    expect(screen.getByRole('option', { name: 'Architect Pass' })).toBeInTheDocument();
  });

  it('starts review loop with selected saved preset', async () => {
    const { onStart } = renderBar({
      review_loop_prompt_presets: JSON.stringify([
        { id: 'custom-architect', name: 'Architect Pass', prompt: 'Review as architect', iterationLimit: 4 },
      ]),
      review_loop_last_preset: 'custom-architect',
      review_loop_last_prompt: 'Review as architect',
      review_loop_last_iterations: '4',
    });

    fireEvent.click(screen.getByRole('button', { name: 'Start Review Loop' }));

    expect(onStart).toHaveBeenCalledWith('Review as architect', 4, 'custom-architect');
  });
});
