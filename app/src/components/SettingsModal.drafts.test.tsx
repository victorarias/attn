// How the settings page's drafts behave now that each field owns one: a field
// reseeds from its own persisted value and not from any other's, a control that
// is whole on change writes at once, and opening the modal settles instead of
// re-rendering forever.

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal } from './SettingsModal';

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

function renderModal(overrides: Record<string, unknown> = {}) {
  const onSetSetting = vi.fn();
  const onListPlugins = vi.fn().mockResolvedValue({ plugins: [], issues: [] });
  const props = {
    isOpen: true,
    onClose: vi.fn(),
    mutedRepos: [],
    githubHosts: [],
    onUnmuteRepo: vi.fn(),
    mutedAuthors: [],
    onUnmuteAuthor: vi.fn(),
    settings: {},
    endpoints: [],
    plugins: [],
    pluginIssues: [],
    onAddEndpoint: vi.fn().mockResolvedValue({ success: true }),
    onUpdateEndpoint: vi.fn().mockResolvedValue({ success: true }),
    onRemoveEndpoint: vi.fn().mockResolvedValue({ success: true }),
    onSetEndpointRemoteWeb: vi.fn().mockResolvedValue({ success: true }),
    onListPlugins,
    onInstallPlugin: vi.fn().mockResolvedValue({ success: true }),
    onRemovePlugin: vi.fn().mockResolvedValue({ success: true }),
    onSetPluginPriority: vi.fn().mockResolvedValue({ success: true }),
    onSetSetting,
    themePreference: 'system' as const,
    onSetTheme: vi.fn(),
    ...overrides,
  };
  const view = render(<SettingsModal {...props} />);
  const rerender = (next: Record<string, unknown>) =>
    view.rerender(<SettingsModal {...props} {...next} />);
  return { onSetSetting, onListPlugins, rerender };
}

describe('SettingsModal drafts', () => {
  // Every draft seeds itself from a hook effect. One of those firing on every
  // render instead of on a real change renders forever, which shows up here as
  // a list call that never stops and a test that never finishes.
  it('settles when it opens: one plugin list, nothing written', async () => {
    const { onListPlugins, onSetSetting } = renderModal();

    await waitFor(() => expect(onListPlugins).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 50));

    expect(onListPlugins).toHaveBeenCalledTimes(1);
    expect(onSetSetting).not.toHaveBeenCalled();
  });

  it('keeps a half-typed field when some other setting changes underneath it', async () => {
    const { rerender } = renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    const input = await screen.findByTestId('settings-projects-directory-input');
    fireEvent.change(input, { target: { value: '/Users/you/half-typed' } });

    // A broadcast about a different setting arrives mid-edit.
    rerender({ settings: { reviewer_model: 'claude-opus-4-6' } });

    expect(await screen.findByTestId('settings-projects-directory-input'))
      .toHaveValue('/Users/you/half-typed');
  });

  it('reseeds a field when its own value changes', async () => {
    const { rerender } = renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    await screen.findByTestId('settings-projects-directory-input');
    rerender({ settings: { projects_directory: '/Users/you/code' } });

    await waitFor(() => {
      expect(screen.getByTestId('settings-projects-directory-input')).toHaveValue('/Users/you/code');
    });
  });

  it('drops a half-typed field when the modal is closed and opened again', async () => {
    const { rerender } = renderModal({ settings: { projects_directory: '/Users/you/code' } });
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    const input = await screen.findByTestId('settings-projects-directory-input');
    fireEvent.change(input, { target: { value: '/Users/you/half-typed' } });

    rerender({ isOpen: false });
    rerender({ isOpen: true });

    expect(await screen.findByTestId('settings-projects-directory-input'))
      .toHaveValue('/Users/you/code');
  });

  // The effort <select> is whole the moment it changes, so it writes without a
  // blur — and raises the mark on the model input it shares a row with rather
  // than a second one of its own.
  it('writes an effort override on change, under the model field’s mark', async () => {
    const { onSetSetting } = renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-agents'));

    const effort = await screen.findByTestId('settings-chief-effort-claude');
    fireEvent.change(effort, { target: { value: 'high' } });

    expect(onSetSetting).toHaveBeenCalledWith('chief_effort_claude', 'high');
    expect(await screen.findByTestId('settings-chief-model-saved-claude')).toBeInTheDocument();
  });
});
