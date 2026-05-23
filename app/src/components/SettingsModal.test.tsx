import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal } from './SettingsModal';

describe('SettingsModal review loop prompts', () => {
  it('closes on escape', async () => {
    const onClose = vi.fn();

    render(
      <SettingsModal
        isOpen
        onClose={onClose}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    await screen.findByText('Mobile Web Client');
    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders authenticated GitHub hosts provided by the daemon', async () => {
    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={['ghe.example.test', 'github.com']}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    expect(await screen.findByText('ghe.example.test')).toBeInTheDocument();
    expect(screen.getByText('github.com')).toBeInTheDocument();
  });

  it('saves a custom review loop preset to settings', async () => {
    const onSetSetting = vi.fn();

    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={onSetSetting}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByTestId('settings-nav-review'));
    await screen.findByText('Review Loop Prompts');
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
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={onAddEndpoint}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.change(screen.getByLabelText('Endpoint name'), { target: { value: 'gpu-box' } });
    fireEvent.change(screen.getByLabelText('SSH target'), { target: { value: 'user@gpu-box' } });
    fireEvent.click(screen.getByText('Add Endpoint'));

    await waitFor(() => {
      expect(onAddEndpoint).toHaveBeenCalledWith('gpu-box', 'user@gpu-box', '');
    });
  });

  it('installs a plugin from a local directory', async () => {
    const onInstallPlugin = vi.fn().mockResolvedValue({ success: true });
    const onListPlugins = vi.fn().mockResolvedValue({ plugins: [], issues: [] });

    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={onListPlugins}
        onInstallPlugin={onInstallPlugin}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByTestId('settings-nav-plugins'));
    fireEvent.change(screen.getByLabelText('Plugin directory'), { target: { value: '/tmp/my-plugin' } });
    fireEvent.click(screen.getByText('Install Plugin'));

    await waitFor(() => {
      expect(onInstallPlugin).toHaveBeenCalledWith('/tmp/my-plugin');
    });
    expect(onListPlugins).toHaveBeenCalled();
  });

  it('updates provider priority for an installed plugin', async () => {
    const onSetPluginPriority = vi.fn().mockResolvedValue({ success: true });

    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{}}
        endpoints={[]}
        plugins={[{
          name: 'services-pilot-worktrees',
          version: '0.1.0',
          dir: '/tmp/services-pilot-worktrees',
          priority: 10,
          connected: true,
          running: true,
        }]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({
          plugins: [{
            name: 'services-pilot-worktrees',
            version: '0.1.0',
            dir: '/tmp/services-pilot-worktrees',
            priority: 10,
            connected: true,
            running: true,
          }],
          issues: [],
        })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={onSetPluginPriority}
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByTestId('settings-nav-plugins'));
    const priority = await screen.findByLabelText('services-pilot-worktrees priority');
    fireEvent.change(priority, { target: { value: '25' } });
    fireEvent.click(screen.getByText('Save'));

    await waitFor(() => {
      expect(onSetPluginPriority).toHaveBeenCalledWith('services-pilot-worktrees', 25);
    });
  });

  it('refreshes a plugin status when the live daemon snapshot changes', async () => {
    const baseProps = {
      isOpen: true,
      onClose: vi.fn(),
      mutedRepos: [] as string[],
      githubHosts: [] as string[],
      onUnmuteRepo: vi.fn(),
      mutedAuthors: [] as string[],
      onUnmuteAuthor: vi.fn(),
      settings: {},
      endpoints: [],
      pluginIssues: [],
      onAddEndpoint: vi.fn().mockResolvedValue({ success: true }),
      onUpdateEndpoint: vi.fn().mockResolvedValue({ success: true }),
      onRemoveEndpoint: vi.fn().mockResolvedValue({ success: true }),
      onSetEndpointRemoteWeb: vi.fn().mockResolvedValue({ success: true }),
      onListPlugins: vi.fn().mockResolvedValue({ plugins: [], issues: [] }),
      onInstallPlugin: vi.fn().mockResolvedValue({ success: true }),
      onRemovePlugin: vi.fn().mockResolvedValue({ success: true }),
      onSetPluginPriority: vi.fn().mockResolvedValue({ success: true }),
      onSetSetting: vi.fn(),
      themePreference: 'system' as const,
      onSetTheme: vi.fn(),
    };
    const startingPlugin = {
      name: 'services-pilot-worktrees',
      version: '0.1.0',
      dir: '/tmp/services-pilot-worktrees',
      priority: 0,
      connected: false,
      running: true,
      health_status: 'unknown',
    };

    const { rerender } = render(
      <SettingsModal
        {...baseProps}
        plugins={[startingPlugin]}
      />,
    );

    fireEvent.click(screen.getByTestId('settings-nav-plugins'));
    expect(await screen.findByText('starting')).toBeInTheDocument();

    rerender(
      <SettingsModal
        {...baseProps}
        plugins={[{ ...startingPlugin, connected: true, health_status: 'healthy' }]}
      />,
    );

    expect(await screen.findByText('connected')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getAllByText('healthy').length).toBeGreaterThan(0);
    });
  });

  it('toggles tailscale serve on the existing device', async () => {
    const onSetSetting = vi.fn();

    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{
          tailscale_enabled: 'false',
          tailscale_status: 'disabled',
          tailscale_domain: 'macbook-epidemic.tail1bfe77.ts.net',
        }}
        endpoints={[]}
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
        onSetSetting={onSetSetting}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    await screen.findByText('Mobile Web Client');
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
        githubHosts={[]}
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
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={onSetEndpointRemoteWeb}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
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
        githubHosts={[]}
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
        plugins={[]}
        pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onUpdateEndpoint={onUpdateEndpoint}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [], issues: [] })}
        onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })}
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
