import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SessionActivitySettings } from './SessionActivitySettings';
import { activityStaleMs } from '../utils/activitySettings';

const renderPane = (settings: Record<string, string> = {}) => {
  const onSetSetting = vi.fn();
  render(
    <SessionActivitySettings
      settings={settings}
      agents={['claude', 'codex']}
      onSetSetting={onSetSetting}
    />
  );
  return onSetSetting;
};

describe('SessionActivitySettings', () => {
  // Picking the agent is choosing whose account pays and how fast lines arrive,
  // so the feature cannot be switched on before that choice is saved.
  it('refuses to enable before an agent is saved', () => {
    renderPane();

    expect(screen.getByTestId('settings-activity-toggle')).toBeDisabled();
  });

  it('enables once an agent is saved', () => {
    const onSetSetting = renderPane({ 'activity.config': '{"agent":"codex"}' });

    fireEvent.click(screen.getByTestId('settings-activity-toggle'));

    expect(onSetSetting).toHaveBeenCalledWith('activity.enabled', 'true');
  });

  it('saves the agent, model, effort and both intervals together', () => {
    const onSetSetting = renderPane();

    fireEvent.change(screen.getByTestId('settings-activity-agent'), { target: { value: 'codex' } });
    fireEvent.change(screen.getByTestId('settings-activity-model'), { target: { value: 'gpt-5.6-luna' } });
    fireEvent.change(screen.getByTestId('settings-activity-effort'), { target: { value: 'low' } });
    fireEvent.change(screen.getByTestId('settings-activity-watching'), { target: { value: '90' } });
    fireEvent.change(screen.getByTestId('settings-activity-present'), { target: { value: '600' } });
    fireEvent.click(screen.getByTestId('settings-activity-save'));

    expect(onSetSetting).toHaveBeenCalledWith(
      'activity.config',
      JSON.stringify({ agent: 'codex', model: 'gpt-5.6-luna', effort: 'low' }),
    );
    expect(onSetSetting).toHaveBeenCalledWith(
      'activity.intervals',
      JSON.stringify({ watching: 90, present: 600 }),
    );
  });

  // Effort measured inert on Claude, so offering the control would suggest a
  // knob that does nothing.
  it('offers reasoning effort only where it changes anything', () => {
    renderPane();

    fireEvent.change(screen.getByTestId('settings-activity-agent'), { target: { value: 'claude' } });
    expect(screen.queryByTestId('settings-activity-effort')).toBeNull();

    fireEvent.change(screen.getByTestId('settings-activity-agent'), { target: { value: 'codex' } });
    expect(screen.getByTestId('settings-activity-effort')).toBeInTheDocument();
  });

  // The presets are per-agent, so a model carried across agents would be saved
  // against an agent that cannot run it.
  it('drops the model when the agent changes', () => {
    const onSetSetting = renderPane();

    fireEvent.change(screen.getByTestId('settings-activity-agent'), { target: { value: 'claude' } });
    fireEvent.change(screen.getByTestId('settings-activity-model'), { target: { value: 'sonnet' } });
    fireEvent.change(screen.getByTestId('settings-activity-agent'), { target: { value: 'codex' } });
    fireEvent.click(screen.getByTestId('settings-activity-save'));

    expect(onSetSetting).toHaveBeenCalledWith('activity.config', JSON.stringify({ agent: 'codex' }));
  });

  it('takes a custom model', () => {
    const onSetSetting = renderPane();

    fireEvent.change(screen.getByTestId('settings-activity-agent'), { target: { value: 'claude' } });
    fireEvent.change(screen.getByTestId('settings-activity-model'), { target: { value: 'custom' } });
    fireEvent.change(screen.getByTestId('settings-activity-model-custom'), {
      target: { value: 'claude-opus-5' },
    });
    fireEvent.click(screen.getByTestId('settings-activity-save'));

    expect(onSetSetting).toHaveBeenCalledWith(
      'activity.config',
      JSON.stringify({ agent: 'claude', model: 'claude-opus-5' }),
    );
  });

  // The presence tier is live state and settings are only re-pushed when a
  // setting changes, so a tier rendered from the settings snapshot would sit
  // there claiming `away` at a user who is plainly reading the pane.
  it('does not render a presence tier from the settings snapshot', () => {
    renderPane({ 'activity.presence_tier': 'away' });

    expect(screen.queryByText(/away/i)).toBeNull();
  });
});

describe('activityStaleMs', () => {
  it('is three times the slowest default cadence when nothing is configured', () => {
    expect(activityStaleMs({})).toBe(15 * 60 * 1000);
  });

  // The intervals are the user's to set. A fixed window measured against a slower
  // cadence would dim every line the instant it was written.
  it('follows a configured cadence', () => {
    expect(activityStaleMs({ 'activity.intervals': JSON.stringify({ watching: 600, present: 1800 }) }))
      .toBe(90 * 60 * 1000);
  });

  it('falls back to the defaults for a value that no longer parses', () => {
    expect(activityStaleMs({ 'activity.intervals': 'not json' })).toBe(15 * 60 * 1000);
  });
});
