import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal } from './SettingsModal';
import { getSettingsAutomationHandle } from './settingsAutomation';

describe('SettingsModal', () => {
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

  it('installs a plugin from a source entered in settings', async () => {
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
    fireEvent.change(screen.getByLabelText('Plugin source'), { target: { value: 'git@ghe.spotify.net:victora/attn-snipe.git' } });
    fireEvent.click(screen.getByText('Install Plugin'));

    await waitFor(() => {
      expect(onInstallPlugin).toHaveBeenCalledWith('git@ghe.spotify.net:victora/attn-snipe.git');
    });
    expect(onListPlugins).toHaveBeenCalled();
  });

  it('installs an available bundled plugin', async () => {
    const onInstallBundledPlugin = vi.fn().mockResolvedValue({ success: true });
    const bundled = {
      name: 'attn-opencode', version: '0.1.0', dir: '/Applications/attn.app/Contents/Resources/plugins/attn-opencode',
      priority: 0, connected: false, running: false, availability: 'bundled', installation_state: 'available',
      runtime_state: 'stopped', can_install: true, can_uninstall: false,
    };
    render(
      <SettingsModal
        isOpen onClose={vi.fn()} mutedRepos={[]} githubHosts={[]} onUnmuteRepo={vi.fn()}
        mutedAuthors={[]} onUnmuteAuthor={vi.fn()} settings={{}} endpoints={[]} plugins={[bundled]} pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })} onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })} onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [bundled], issues: [] })} onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onInstallBundledPlugin={onInstallBundledPlugin} onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })} onSetSetting={vi.fn()} themePreference="system" onSetTheme={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('settings-nav-plugins'));
    expect(await screen.findByText('Bundled')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Install', { selector: 'button' }));
    await waitFor(() => expect(onInstallBundledPlugin).toHaveBeenCalledWith('attn-opencode'));
  });

  it('uninstalls an installed bundled plugin', async () => {
    const onUninstallPlugin = vi.fn().mockResolvedValue({ success: true });
    const bundled = {
      name: 'attn-opencode', version: '0.1.0', dir: '/Applications/attn.app/Contents/Resources/plugins/attn-opencode',
      priority: 0, connected: true, running: true, availability: 'bundled', installation_state: 'installed',
      runtime_state: 'connected', can_install: false, can_uninstall: true,
    };
    render(
      <SettingsModal
        isOpen onClose={vi.fn()} mutedRepos={[]} githubHosts={[]} onUnmuteRepo={vi.fn()}
        mutedAuthors={[]} onUnmuteAuthor={vi.fn()} settings={{}} endpoints={[]} plugins={[bundled]} pluginIssues={[]}
        onAddEndpoint={vi.fn().mockResolvedValue({ success: true })} onUpdateEndpoint={vi.fn().mockResolvedValue({ success: true })}
        onRemoveEndpoint={vi.fn().mockResolvedValue({ success: true })} onSetEndpointRemoteWeb={vi.fn().mockResolvedValue({ success: true })}
        onListPlugins={vi.fn().mockResolvedValue({ plugins: [bundled], issues: [] })} onInstallPlugin={vi.fn().mockResolvedValue({ success: true })}
        onUninstallPlugin={onUninstallPlugin} onRemovePlugin={vi.fn().mockResolvedValue({ success: true })}
        onSetPluginPriority={vi.fn().mockResolvedValue({ success: true })} onSetSetting={vi.fn()} themePreference="system" onSetTheme={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('settings-nav-plugins'));
    fireEvent.click(await screen.findByText('Uninstall', { selector: 'button' }));
    await waitFor(() => expect(onUninstallPlugin).toHaveBeenCalledWith('attn-opencode'));
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
          availability: 'user',
          installation_state: 'installed',
          runtime_state: 'connected',
          can_install: false,
          can_uninstall: true,
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
      runtime_phase: 'starting',
      runtime_state: 'starting',
      health_status: 'unknown',
      availability: 'user',
      installation_state: 'installed',
      can_install: false,
      can_uninstall: true,
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
        plugins={[{
          ...startingPlugin,
          running: false,
          runtime_phase: 'backoff',
          runtime_state: 'degraded',
          restart_attempt: 2,
          next_restart_at: '2026-07-15T22:20:00Z',
          last_exit: '2026-07-15T22:19:59Z: exit code 1',
        }]}
      />,
    );

    expect(await screen.findByText('degraded')).toBeInTheDocument();
    expect(screen.getByText('Restart attempt')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText(/Last exit: 2026-07-15T22:19:59Z: exit code 1/)).toBeInTheDocument();

    rerender(
      <SettingsModal
        {...baseProps}
        plugins={[{ ...startingPlugin, connected: true, runtime_phase: 'connected', runtime_state: 'connected', health_status: 'healthy' }]}
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

  it('enables workflows when off and disables them when on', async () => {
    const onSetSetting = vi.fn();

    const { rerender } = render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{ workflows_enabled: 'false' }}
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

    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    const toggle = await screen.findByTestId('settings-workflows-toggle');
    expect(toggle).toHaveTextContent('Enable');
    fireEvent.click(toggle);
    expect(onSetSetting).toHaveBeenCalledWith('workflows_enabled', 'true');

    rerender(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{ workflows_enabled: 'true' }}
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

    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    const toggleOn = await screen.findByTestId('settings-workflows-toggle');
    expect(toggleOn).toHaveTextContent('Disable');
    fireEvent.click(toggleOn);
    expect(onSetSetting).toHaveBeenCalledWith('workflows_enabled', 'false');
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

  it('shows plugin agents without offering attn-owned executable overrides', async () => {
    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={{ snipe_available: 'true', snipe_cap_resume: 'true' }}
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

    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    expect(await screen.findByRole('button', { name: 'Snipe' })).toBeInTheDocument();
    expect(screen.getByText('Resume: on')).toBeInTheDocument();
    expect(document.getElementById('settings-snipe-exec')).toBeNull();
  });

  it('saves the workspace context keeper agent and model atomically', async () => {
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
          codex_available: 'true',
          codex_cap_headless_task: 'true',
          claude_available: 'true',
          claude_cap_headless_task: 'true',
          snipe_available: 'true',
          snipe_cap_headless_task: 'true',
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

    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    expect(screen.queryByRole('option', { name: 'Snipe' })).not.toBeInTheDocument();
    fireEvent.change(await screen.findByTestId('settings-context-keeper-agent'), {
      target: { value: 'codex' },
    });
    expect(screen.getByTestId('settings-context-keeper-model')).toHaveValue('gpt-5.4');
    expect(screen.queryByTestId('settings-context-keeper-model-custom')).not.toBeInTheDocument();
    fireEvent.change(screen.getByTestId('settings-context-keeper-model'), {
      target: { value: 'custom' },
    });
    expect(screen.getByTestId('settings-context-keeper-save')).toBeDisabled();
    fireEvent.change(screen.getByTestId('settings-context-keeper-model-custom'), {
      target: { value: 'gpt-test' },
    });
    fireEvent.click(screen.getByTestId('settings-context-keeper-save'));

    expect(onSetSetting).toHaveBeenCalledWith(
      'workspace_keeper_compact',
      '{"agent":"codex","model":"gpt-test"}',
    );

    fireEvent.change(screen.getByTestId('settings-context-keeper-agent'), {
      target: { value: 'claude' },
    });
    expect(screen.getByTestId('settings-context-keeper-model')).toHaveValue('opus');
    expect(screen.getByTestId('settings-context-keeper-save')).toBeEnabled();
    fireEvent.change(screen.getByTestId('settings-context-keeper-agent'), {
      target: { value: 'codex' },
    });
    expect(screen.getByTestId('settings-context-keeper-model')).toHaveValue('gpt-5.4');
    expect(screen.getByTestId('settings-context-keeper-save')).toBeEnabled();
  });

  it('preserves a configured custom keeper model for editing', async () => {
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
          codex_available: 'true',
          codex_cap_headless_task: 'true',
          workspace_keeper_compact: '{"agent":"codex","model":"gpt-custom"}',
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
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
      />
    );

    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    expect(await screen.findByTestId('settings-context-keeper-model')).toHaveValue('custom');
    expect(screen.getByTestId('settings-context-keeper-model-custom')).toHaveValue('gpt-custom');
  });
});

describe('SettingsModal notebook folder', () => {
  function renderModal(
    settings: Record<string, string>,
    onSetSetting = vi.fn(),
  ) {
    render(
      <SettingsModal
        isOpen
        onClose={vi.fn()}
        mutedRepos={[]}
        githubHosts={[]}
        onUnmuteRepo={vi.fn()}
        mutedAuthors={[]}
        onUnmuteAuthor={vi.fn()}
        settings={settings}
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
      />,
    );
    return onSetSetting;
  }

  it('shows the override value and the daemon-resolved effective folder', async () => {
    renderModal({
      'notebook.root': '~/my-notes',
      'notebook.root.effective': '/Users/me/my-notes',
    });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    const input = await screen.findByTestId('settings-notebook-root-input');
    expect(input).toHaveValue('~/my-notes');
    expect(screen.getByTestId('settings-notebook-root-effective')).toHaveTextContent(
      'Currently: /Users/me/my-notes',
    );
  });

  it('falls back to the effective default as placeholder when no override is set', async () => {
    renderModal({ 'notebook.root.effective': '/Users/me/attn-notebook' });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    const input = await screen.findByTestId('settings-notebook-root-input');
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', '/Users/me/attn-notebook');
  });

  it('persists a new folder on blur and an empty value to restore the default', async () => {
    const onSetSetting = renderModal({
      'notebook.root': '~/my-notes',
      'notebook.root.effective': '/Users/me/my-notes',
    });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    const input = await screen.findByTestId('settings-notebook-root-input');

    fireEvent.change(input, { target: { value: '/Users/me/elsewhere' } });
    fireEvent.blur(input);
    expect(onSetSetting).toHaveBeenCalledWith('notebook.root', '/Users/me/elsewhere');

    // Clearing the override commits an empty value, which the daemon resolves
    // back to the per-profile default.
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);
    expect(onSetSetting).toHaveBeenCalledWith('notebook.root', '');
  });
});

describe('SettingsModal keeper', () => {
  function renderModal(
    settings: Record<string, string>,
    onSetSetting = vi.fn(),
  ) {
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
          claude_available: 'true',
          claude_cap_headless_task: 'true',
          codex_available: 'true',
          codex_cap_headless_task: 'true',
          ...settings,
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
      />,
    );
    return onSetSetting;
  }

  it('treats the master switch as on by default and toggles it off', async () => {
    const onSetSetting = renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    // Unset notebook.tasks_enabled reads as ON, so the action offers to disable.
    const toggle = await screen.findByTestId('settings-keeper-tasks-toggle');
    expect(toggle).toHaveTextContent('Disable');
    fireEvent.click(toggle);
    expect(onSetSetting).toHaveBeenCalledWith('notebook.tasks_enabled', 'false');
  });

  it('re-enables the master switch when it is off', async () => {
    const onSetSetting = renderModal({ 'notebook.tasks_enabled': 'false' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const toggle = await screen.findByTestId('settings-keeper-tasks-toggle');
    expect(toggle).toHaveTextContent('Enable');
    fireEvent.click(toggle);
    expect(onSetSetting).toHaveBeenCalledWith('notebook.tasks_enabled', 'true');
  });

  it('seeds always-on duties with their tier default and saves an override', async () => {
    const onSetSetting = renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    // Session summaries default to Claude Haiku; narration to Claude Sonnet.
    expect(await screen.findByTestId('settings-keeper-summarize-agent')).toHaveValue('claude');
    expect(screen.getByTestId('settings-keeper-summarize-model')).toHaveValue('haiku');
    expect(screen.getByTestId('settings-keeper-narrate-model')).toHaveValue('sonnet');

    // Switching the summarize agent re-seeds the model to that agent's recommended.
    fireEvent.change(screen.getByTestId('settings-keeper-summarize-agent'), {
      target: { value: 'codex' },
    });
    expect(screen.getByTestId('settings-keeper-summarize-model')).toHaveValue('gpt-5.4-mini');

    fireEvent.click(screen.getByTestId('settings-keeper-summarize-save'));
    expect(onSetSetting).toHaveBeenCalledWith(
      'notebook.summarize_session',
      '{"agent":"codex","model":"gpt-5.4-mini"}',
    );
  });

  it('offers no Disabled agent for always-on duties but does for compaction', async () => {
    renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const summarizeAgent = await screen.findByTestId('settings-keeper-summarize-agent');
    expect(
      Array.from(summarizeAgent.querySelectorAll('option')).map((o) => o.textContent),
    ).not.toContain('Disabled');

    const compactAgent = screen.getByTestId('settings-context-keeper-agent');
    expect(
      Array.from(compactAgent.querySelectorAll('option')).map((o) => o.textContent),
    ).toContain('Disabled');
  });

  it('reverts an always-on duty to its default by clearing the override', async () => {
    const onSetSetting = renderModal({
      'notebook.narrate_workspace': '{"agent":"claude","model":"opus"}',
    });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    // The saved override is shown, and "Use default" is enabled because one exists.
    expect(await screen.findByTestId('settings-keeper-narrate-model')).toHaveValue('opus');
    const useDefault = screen.getByTestId('settings-keeper-narrate-clear');
    expect(useDefault).toHaveTextContent('Use default');
    expect(useDefault).toBeEnabled();

    fireEvent.click(useDefault);
    expect(onSetSetting).toHaveBeenCalledWith('notebook.narrate_workspace', '');
  });
});

describe('SettingsModal chief settings', () => {
  function renderModal(
    settings: Record<string, string>,
    onSetSetting = vi.fn(),
  ) {
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
          claude_available: 'true',
          codex_available: 'true',
          ...settings,
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
      />,
    );
    return onSetSetting;
  }

  it('enables auto-approve when off', async () => {
    const onSetSetting = renderModal({ auto_approve_enabled: 'false' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const toggle = await screen.findByTestId('settings-auto-approve-toggle');
    expect(toggle).toHaveTextContent('Enable');
    fireEvent.click(toggle);
    expect(onSetSetting).toHaveBeenCalledWith('auto_approve_enabled', 'true');
  });

  it('disables auto-approve when on', async () => {
    const onSetSetting = renderModal({ auto_approve_enabled: 'true' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const toggle = await screen.findByTestId('settings-auto-approve-toggle');
    expect(toggle).toHaveTextContent('Disable');
    fireEvent.click(toggle);
    expect(onSetSetting).toHaveBeenCalledWith('auto_approve_enabled', 'false');
  });

  it('renders a chief-model input per supported agent and commits a typed model on blur', async () => {
    const onSetSetting = renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    // claude and codex each get a row; copilot does not (its launch ignores --model).
    const claudeInput = await screen.findByTestId('settings-chief-model-claude');
    expect(screen.getByTestId('settings-chief-model-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-chief-model-copilot')).toBeNull();

    fireEvent.change(claudeInput, { target: { value: 'opus' } });
    fireEvent.blur(claudeInput);
    expect(onSetSetting).toHaveBeenCalledWith('chief_model_claude', 'opus');
  });

  it('does not write when a chief-model input blurs unchanged', async () => {
    const onSetSetting = renderModal({ chief_model_claude: 'opus' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const claudeInput = await screen.findByTestId('settings-chief-model-claude');
    expect(claudeInput).toHaveValue('opus');
    fireEvent.blur(claudeInput);
    expect(onSetSetting).not.toHaveBeenCalledWith('chief_model_claude', expect.anything());
  });

  it('clears a chief-model override back to the agent default', async () => {
    const onSetSetting = renderModal({ chief_model_codex: 'gpt-5.4' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const codexInput = await screen.findByTestId('settings-chief-model-codex');
    expect(codexInput).toHaveValue('gpt-5.4');
    fireEvent.change(codexInput, { target: { value: '' } });
    fireEvent.blur(codexInput);
    expect(onSetSetting).toHaveBeenCalledWith('chief_model_codex', '');
  });

  it('renders a chief-effort select per supported agent and commits on change', async () => {
    const onSetSetting = renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const claudeEffort = await screen.findByTestId('settings-chief-effort-claude');
    expect(screen.getByTestId('settings-chief-effort-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-chief-effort-copilot')).toBeNull();

    fireEvent.change(claudeEffort, { target: { value: 'high' } });
    expect(onSetSetting).toHaveBeenCalledWith('chief_effort_claude', 'high');
  });

  it('shows a saved chief-effort override', async () => {
    renderModal({ chief_effort_codex: 'xhigh' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const codexEffort = await screen.findByTestId('settings-chief-effort-codex');
    expect(codexEffort).toHaveValue('xhigh');
  });

  it('keeps an agent visible when only its chief-effort override is saved', async () => {
    // codex is unavailable but has a saved effort override, so it should still show
    // (mirrors the chief-model re-inclusion rule).
    renderModal({ codex_available: 'false', chief_effort_codex: 'low' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    expect(await screen.findByTestId('settings-chief-effort-codex')).toHaveValue('low');
  });

  it('renders a default-model input per supported agent and commits a typed model on blur', async () => {
    const onSetSetting = renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    // claude and codex each get a row; copilot does not (its launch ignores --model).
    const claudeInput = await screen.findByTestId('settings-default-model-claude');
    expect(screen.getByTestId('settings-default-model-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-default-model-copilot')).toBeNull();

    fireEvent.change(claudeInput, { target: { value: 'opus' } });
    fireEvent.blur(claudeInput);
    expect(onSetSetting).toHaveBeenCalledWith('default_model_claude', 'opus');
  });

  it('does not write when a default-model input blurs unchanged', async () => {
    const onSetSetting = renderModal({ default_model_claude: 'opus' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const claudeInput = await screen.findByTestId('settings-default-model-claude');
    expect(claudeInput).toHaveValue('opus');
    fireEvent.blur(claudeInput);
    expect(onSetSetting).not.toHaveBeenCalledWith('default_model_claude', expect.anything());
  });

  it('clears a default-model override back to the agent default', async () => {
    const onSetSetting = renderModal({ default_model_codex: 'gpt-5.4' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const codexInput = await screen.findByTestId('settings-default-model-codex');
    expect(codexInput).toHaveValue('gpt-5.4');
    fireEvent.change(codexInput, { target: { value: '' } });
    fireEvent.blur(codexInput);
    expect(onSetSetting).toHaveBeenCalledWith('default_model_codex', '');
  });

  it('renders a default-effort select per supported agent and commits on change', async () => {
    const onSetSetting = renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const claudeEffort = await screen.findByTestId('settings-default-effort-claude');
    expect(screen.getByTestId('settings-default-effort-codex')).toBeInTheDocument();
    expect(screen.queryByTestId('settings-default-effort-copilot')).toBeNull();

    fireEvent.change(claudeEffort, { target: { value: 'high' } });
    expect(onSetSetting).toHaveBeenCalledWith('default_effort_claude', 'high');
  });

  it('shows a saved default-effort override', async () => {
    renderModal({ default_effort_codex: 'xhigh' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const codexEffort = await screen.findByTestId('settings-default-effort-codex');
    expect(codexEffort).toHaveValue('xhigh');
  });

  it('keeps an agent visible when only its default-effort override is saved', async () => {
    // codex is unavailable but has a saved effort override, so it should still show
    // (mirrors the chief-effort re-inclusion rule).
    renderModal({ codex_available: 'false', default_effort_codex: 'low' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    expect(await screen.findByTestId('settings-default-effort-codex')).toHaveValue('low');
  });

  it('shows the effective context-window caps and defaults to 128000 when unset', async () => {
    renderModal({ chief_context_window_cap: '120000', headless_context_window_cap: '90000' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    expect(await screen.findByTestId('settings-chief-context-cap')).toHaveValue(120000);
    expect(screen.getByTestId('settings-headless-context-cap')).toHaveValue(90000);
  });

  it('defaults both context-window caps to 128000 when unset', async () => {
    renderModal({});
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    expect(await screen.findByTestId('settings-chief-context-cap')).toHaveValue(128000);
    expect(screen.getByTestId('settings-headless-context-cap')).toHaveValue(128000);
  });

  it('commits a changed chief context-window cap on blur', async () => {
    const onSetSetting = renderModal({ chief_context_window_cap: '128000' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const input = await screen.findByTestId('settings-chief-context-cap');
    fireEvent.change(input, { target: { value: '100000' } });
    fireEvent.blur(input);
    expect(onSetSetting).toHaveBeenCalledWith('chief_context_window_cap', '100000');
  });

  it('commits a changed headless context-window cap on blur', async () => {
    const onSetSetting = renderModal({ headless_context_window_cap: '128000' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const input = await screen.findByTestId('settings-headless-context-cap');
    fireEvent.change(input, { target: { value: '200000' } });
    fireEvent.blur(input);
    expect(onSetSetting).toHaveBeenCalledWith('headless_context_window_cap', '200000');
  });

  it('does not re-commit an unchanged context-window cap on blur', async () => {
    const onSetSetting = renderModal({ chief_context_window_cap: '128000' });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const input = await screen.findByTestId('settings-chief-context-cap');
    fireEvent.blur(input);
    expect(onSetSetting).not.toHaveBeenCalledWith('chief_context_window_cap', expect.anything());
  });
});

describe('SettingsModal font size', () => {
  function renderModal(overrides: Record<string, unknown> = {}) {
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
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
        {...overrides}
      />,
    );
  }

  it('shows the app font scale and steps it through the handlers', async () => {
    const onIncrease = vi.fn();
    const onDecrease = vi.fn();
    renderModal({
      uiScale: 1.2,
      onIncreaseUIScale: onIncrease,
      onDecreaseUIScale: onDecrease,
    });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    expect(await screen.findByTestId('settings-app-font-scale-value')).toHaveTextContent('120%');

    fireEvent.click(screen.getByLabelText('Increase app font size'));
    fireEvent.click(screen.getByLabelText('Decrease app font size'));
    expect(onIncrease).toHaveBeenCalledTimes(1);
    expect(onDecrease).toHaveBeenCalledTimes(1);
  });

  it('offers an app reset only when the scale is not the default', async () => {
    const onReset = vi.fn();
    renderModal({ uiScale: 1.3, onResetUIScale: onReset });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    fireEvent.click(await screen.findByText('Reset'));
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it('shows the taskboard matching the app by default, without a Match app button', async () => {
    renderModal({ uiScale: 1, ticketBoardScale: null, effectiveTicketBoardScale: 1 });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    expect(await screen.findByTestId('settings-taskboard-font-scale-value')).toHaveTextContent(
      'Match app',
    );
    expect(screen.queryByText('Match app', { selector: 'button' })).not.toBeInTheDocument();
  });

  it('shows an overridden taskboard scale and reverts it via Match app', async () => {
    const onMatchApp = vi.fn();
    const onIncrease = vi.fn();
    renderModal({
      uiScale: 1,
      ticketBoardScale: 1.3,
      effectiveTicketBoardScale: 1.3,
      onIncreaseTicketBoardScale: onIncrease,
      onMatchAppTicketBoardScale: onMatchApp,
    });

    fireEvent.click(screen.getByTestId('settings-nav-general'));
    expect(await screen.findByTestId('settings-taskboard-font-scale-value')).toHaveTextContent(
      '130%',
    );

    fireEvent.click(screen.getByLabelText('Increase taskboard font size'));
    expect(onIncrease).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText('Match app', { selector: 'button' }));
    expect(onMatchApp).toHaveBeenCalledTimes(1);
  });
});

describe('SettingsModal automation handle', () => {
  function renderModal(overrides: Record<string, unknown> = {}) {
    return render(
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
        onSetSetting={vi.fn()}
        themePreference="system"
        onSetTheme={vi.fn()}
        {...overrides}
      />,
    );
  }

  it('registers the handle while mounted and clears it on unmount', async () => {
    const { unmount } = renderModal();
    await screen.findByText('Mobile Web Client');

    expect(getSettingsAutomationHandle()).not.toBeNull();

    unmount();
    expect(getSettingsAutomationHandle()).toBeNull();
  });

  it('reports open state, active section, and search text through getState', async () => {
    renderModal();
    await screen.findByText('Mobile Web Client');

    expect(getSettingsAutomationHandle()?.getState()).toEqual({
      open: true,
      activeSection: 'connectivity',
      search: '',
    });

    fireEvent.change(screen.getByLabelText('Search settings'), { target: { value: 'theme' } });
    fireEvent.click(screen.getByTestId('settings-nav-general'));

    expect(getSettingsAutomationHandle()?.getState()).toEqual({
      open: true,
      activeSection: 'general',
      search: 'theme',
    });
  });

  it('reports open: false when the modal is closed', async () => {
    renderModal({ isOpen: false });

    expect(getSettingsAutomationHandle()?.getState()).toEqual({
      open: false,
      activeSection: 'connectivity',
      search: '',
    });
  });

  it('selectSection switches the rendered section the same way a nav click does', async () => {
    renderModal();
    await screen.findByText('Mobile Web Client');

    act(() => {
      getSettingsAutomationHandle()?.selectSection('agents');
    });

    expect(await screen.findByTestId('settings-section-agents')).toBeInTheDocument();
    expect(getSettingsAutomationHandle()?.getState().activeSection).toBe('agents');
  });

  // Regression test: SettingsModal re-registers a fresh handle on every render
  // (its registration effect depends on selectedSection), so a handle
  // reference captured *before* calling selectSection closes over the
  // pre-selection state. A caller (like the bridge's settings_select_section
  // case) must re-read through getSettingsAutomationHandle() after the
  // section switch settles, not reuse the handle it already had.
  it('a handle captured before selectSection reports stale state; re-reading the module getter reports fresh state', async () => {
    renderModal();
    await screen.findByText('Mobile Web Client');

    const capturedHandle = getSettingsAutomationHandle();
    act(() => {
      capturedHandle?.selectSection('agents');
    });
    await screen.findByTestId('settings-section-agents');

    expect(capturedHandle?.getState().activeSection).toBe('connectivity');
    expect(getSettingsAutomationHandle()?.getState().activeSection).toBe('agents');
  });

  it('selectSection throws a clear error for an unknown section id', async () => {
    renderModal();
    await screen.findByText('Mobile Web Client');

    expect(() => getSettingsAutomationHandle()?.selectSection('nonexistent')).toThrow(
      /unknown settings section "nonexistent"/,
    );
  });
});
