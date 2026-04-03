import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal } from './SettingsModal';

describe('SettingsModal review loop prompts', () => {
  it('closes on escape', () => {
    const onClose = vi.fn();

    render(
      <SettingsModal
        isOpen
        onClose={onClose}
        mutedRepos={[]}
        connectedHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

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
        endpoints={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
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

  it('submits a new endpoint through the modal', async () => {
    const onAddEndpoint = vi.fn().mockResolvedValue({ success: true });

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
        endpoints={[]}
        onAddEndpoint={onAddEndpoint}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.change(screen.getByLabelText('Endpoint name'), { target: { value: 'gpu-box' } });
    fireEvent.change(screen.getByLabelText('SSH target'), { target: { value: 'user@gpu-box' } });
    fireEvent.click(screen.getByText('Add Endpoint'));

    await waitFor(() => {
      expect(onAddEndpoint).toHaveBeenCalledWith('gpu-box', 'user@gpu-box');
    });
  });
});
