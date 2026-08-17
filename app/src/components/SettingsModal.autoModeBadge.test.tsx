import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal } from './SettingsModal';
import type { AutoModeState } from '../hooks/daemonAutoModeEvents';

const daemonApi = vi.hoisted(() => ({
  sendGetSettings: vi.fn(),
  sendBusStatusGet: vi.fn(() => new Promise(() => {})),
  sendBusSetConsumerEnabled: vi.fn(),
  sendAutoModeGet: vi.fn(),
  sendAutoModePromote: vi.fn(),
  sendAutoModeDiscard: vi.fn(),
}));
vi.mock('../contexts/DaemonApiContext', () => ({ useDaemonApi: () => daemonApi }));

const autoModeState = (proposals: number): AutoModeState => ({
  config: {
    enabled_default: true,
    environment: [],
    allow: [],
    hard_deny: [],
    classifier_models: ['opencode-go/glm-5.3'],
    escalation_models: ['opencode-go/qwen3.8-max'],
  },
  proposals: Array.from({ length: proposals }, (_unused, index) => ({
    id: index + 1,
    kind: 'allow',
    target: '',
    value: `curl https://example.com/${index}*`,
    proposed_by: 'session-a',
    state: 'pending',
    created_at: '2026-08-16T10:00:00Z',
    resolved_at: '',
  })),
  denials: [],
});

function renderModal(isOpen = true) {
  return render(
    <SettingsModal
      isOpen={isOpen}
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
    />,
  );
}

describe('SettingsModal auto mode badge', () => {
  // Proposals nobody sees are proposals that rot: the count has to be on the
  // nav row before anyone opens the section.
  it('counts waiting proposals on the nav row without opening the section', async () => {
    daemonApi.sendAutoModeGet.mockResolvedValue(autoModeState(3));
    renderModal();

    const nav = await screen.findByTestId('settings-nav-autoMode');
    await waitFor(() => expect(nav).toHaveTextContent('3'));
    expect(nav.querySelector('.settings-nav-count')).toHaveClass('waiting');
    // Reading happens once for both surfaces, not once per surface.
    expect(daemonApi.sendAutoModeGet).toHaveBeenCalledTimes(1);
    // Still on whichever section was open — the badge did not navigate anyone.
    expect(screen.queryByTestId('settings-automode-config')).toBeNull();
  });

  it('leaves the count unmarked when nothing is waiting', async () => {
    daemonApi.sendAutoModeGet.mockResolvedValue(autoModeState(0));
    renderModal();

    const nav = await screen.findByTestId('settings-nav-autoMode');
    await waitFor(() => expect(nav).toHaveTextContent('0'));
    expect(nav.querySelector('.settings-nav-count')).not.toHaveClass('waiting');
  });

  it('opens the section from the nav row', async () => {
    daemonApi.sendAutoModeGet.mockResolvedValue(autoModeState(1));
    renderModal();

    fireEvent.click(await screen.findByTestId('settings-nav-autoMode'));
    await screen.findByTestId('automode-proposals');
    expect(screen.getByTestId('settings-section-autoMode')).toBeInTheDocument();
  });

  // A closed panel holds nothing: no read while it is shut, and no stale
  // snapshot kept across a close.
  it('reads nothing while the modal is closed', async () => {
    daemonApi.sendAutoModeGet.mockClear();
    daemonApi.sendAutoModeGet.mockResolvedValue(autoModeState(2));
    renderModal(false);

    await new Promise((done) => setTimeout(done, 20));
    expect(daemonApi.sendAutoModeGet).not.toHaveBeenCalled();
  });
});
