// The settings page's shape: every section id the automation surface publishes
// has a nav item that renders something, the ids that moved still resolve, and
// a field that applies on blur says so.

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal } from './SettingsModal';
import { assertValidSettingsSectionID, getSettingsAutomationHandle } from './settingsAutomation';

const daemonApi = vi.hoisted(() => ({
  sendGetSettings: vi.fn(),
  sendBusStatusGet: vi.fn(() => new Promise(() => {})),
  sendBusSetConsumerEnabled: vi.fn(),
  sendAutoModeGet: vi.fn(() => new Promise(() => {})),
  sendAutoModePromote: vi.fn(),
  sendAutoModeDiscard: vi.fn(),
  sendAutoModePatternAdd: vi.fn(),
  sendAutoModePatternRemove: vi.fn(),
}));
vi.mock('../contexts/DaemonApiContext', () => ({ useDaemonApi: () => daemonApi }));

// Every id the modal answers to. A dangling one is a broken deep link, not a
// rename, so the list lives here as well as in settingsAutomation.
const SECTION_IDS = [
  'general',
  'workspace',
  'hygiene',
  'agents',
  'keeper',
  'autoMode',
  'connectivity',
  'plugins',
  'backgroundTasks',
  'eventBus',
  'data',
] as const;

function renderModal(overrides: Record<string, unknown> = {}) {
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
      {...overrides}
    />,
  );
  return onSetSetting;
}

describe('SettingsModal sections', () => {
  it('gives every published section id a nav item that renders a block', async () => {
    renderModal();

    for (const id of SECTION_IDS) {
      assertValidSettingsSectionID(id);
      const item = screen.getByTestId(`settings-nav-${id}`);
      fireEvent.click(item);
      await waitFor(() => {
        expect(getSettingsAutomationHandle()?.getState().activeSection).toBe(id);
      });
      expect(document.querySelector('.settings-block')).not.toBeNull();
    }
  });

  it('refuses the retired review id by name', () => {
    expect(() => assertValidSettingsSectionID('review')).toThrow(/unknown settings section "review"/);
  });

  it('keeps the reviewer model with the other model overrides, under agents', async () => {
    renderModal({ settings: { reviewer_model: 'claude-opus-4-6' } });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    expect(await screen.findByLabelText('Reviewer model')).toHaveValue('claude-opus-4-6');
    expect(screen.getByTestId('settings-chief-model-claude')).toBeInTheDocument();
  });

  it('puts the attention queue timings with the rest of hygiene', async () => {
    renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-hygiene'));

    expect(await screen.findByTestId('settings-auto-settle-arm')).toBeInTheDocument();
    expect(screen.getByTestId('settings-auto-settle-countdown')).toBeInTheDocument();
  });

  it('raises a saved mark when a blur-committed field lands, and takes it away', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const onSetSetting = renderModal();
      fireEvent.click(screen.getByTestId('settings-nav-workspace'));

      const input = await screen.findByTestId('settings-projects-directory-input');
      fireEvent.change(input, { target: { value: '/Users/you/code' } });
      fireEvent.blur(input);

      expect(onSetSetting).toHaveBeenCalledWith('projects_directory', '/Users/you/code');
      expect(
        await screen.findByTestId('settings-projects-directory-saved'),
      ).toBeInTheDocument();

      await act(async () => {
        vi.advanceTimersByTime(2000);
      });
      expect(screen.queryByTestId('settings-projects-directory-saved')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('says nothing when a blur-committed field has not changed', async () => {
    const onSetSetting = renderModal({ settings: { projects_directory: '/Users/you/code' } });
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    const input = await screen.findByTestId('settings-projects-directory-input');
    fireEvent.blur(input);

    expect(onSetSetting).not.toHaveBeenCalled();
    expect(screen.queryByTestId('settings-projects-directory-saved')).toBeNull();
  });
});
