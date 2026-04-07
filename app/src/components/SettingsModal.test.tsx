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
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
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
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
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
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
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

  it('toggles tailscale serve on the existing device', () => {
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
        settings={{
          tailscale_enabled: 'false',
          tailscale_status: 'disabled',
          tailscale_domain: 'macbook-epidemic.tail1bfe77.ts.net',
        }}
        endpoints={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={onSetSetting}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByText('Enable'));
    expect(onSetSetting).toHaveBeenCalledWith('tailscale_enabled', 'true');
    expect(screen.getByText(/does not register a second tailnet device/i)).toBeInTheDocument();
  });

  it('toggles remote web access for a connected endpoint', async () => {
    const onSetEndpointRemoteWeb = vi.fn().mockResolvedValue({ success: true });

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
        endpoints={[{
          id: 'ep-1',
          name: 'gpu-box',
          ssh_target: 'user@gpu-box',
          status: 'connected',
          enabled: true,
          capabilities: {
            protocol_version: '49',
            agents_available: ['codex'],
            tailscale_enabled: false,
            tailscale_status: 'disabled',
            tailscale_domain: 'gpu-box.tail1bfe77.ts.net',
            tailscale_auth_url: 'https://login.tailscale.example/auth',
          },
        }]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={onSetEndpointRemoteWeb}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByText('Enable Web'));

    await waitFor(() => {
      expect(onSetEndpointRemoteWeb).toHaveBeenCalledWith('ep-1', true);
    });
    expect(screen.getByText(/sign this host into tailscale/i)).toBeInTheDocument();
  });

  it('re-bootstraps an enabled endpoint by disabling and re-enabling it', async () => {
    const onUpdateEndpoint = vi.fn().mockResolvedValue({ success: true });

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
        endpoints={[{
          id: 'ep-1',
          name: 'gpu-box',
          ssh_target: 'user@gpu-box',
          status: 'error',
          enabled: true,
        }]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={onUpdateEndpoint}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByText('Re-bootstrap'));

    await waitFor(() => {
      expect(onUpdateEndpoint).toHaveBeenNthCalledWith(1, 'ep-1', { enabled: false });
      expect(onUpdateEndpoint).toHaveBeenNthCalledWith(2, 'ep-1', { enabled: true });
    });
  });
});
