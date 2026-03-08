import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '../test/utils';
import { SettingsModal } from './SettingsModal';

describe('SettingsModal review loop prompts', () => {
  it('saves a custom review loop preset to settings', () => {
    const onSetSetting = vi.fn();

    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        connectedHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        onSetSetting={onSetSetting}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.change(screen.getByLabelText('Prompt name'), { target: { value: 'Architect Pass' } });
    fireEvent.change(screen.getByLabelText('Default iterations'), { target: { value: '5' } });
    fireEvent.change(screen.getByLabelText('Prompt'), { target: { value: 'Review like an architect' } });
    fireEvent.click(screen.getByText('Save Prompt'));

    expect(onSetSetting).toHaveBeenCalledWith(
      'review_loop_prompt_presets',
      JSON.stringify([
        {
          id: 'custom-architect-pass',
          name: 'Architect Pass',
          prompt: 'Review like an architect',
          iterationLimit: 5,
        },
      ])
    );
  });
});
