import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { LocationPicker } from './LocationPicker';
import { SettingsProvider } from '../contexts/SettingsContext';

const useFilesystemSuggestionsMock = vi.fn();

vi.mock('../hooks/useFilesystemSuggestions', () => ({
  useFilesystemSuggestions: () => useFilesystemSuggestionsMock(),
}));

// A plugin-driver agent that advertises auto_mode, the way attn-pi does. The
// per-session toggle is offered for those and nothing else.
const PI_SETTINGS = {
  snipe_available: 'true',
  snipe_cap_auto_mode: 'true',
  claude_cap_auto_mode: 'false',
};

const inspectsTo = (resolved: string) => vi.fn(async (inputPath: string) => ({
  success: true,
  inspection: {
    input_path: inputPath,
    resolved_path: resolved,
    home_path: '/home/remote',
    exists: true,
    is_directory: true,
  },
}));

function renderPicker(settings: Record<string, string>) {
  const onSelect = vi.fn();
  const onInspectPath = inspectsTo('/home/remote/projects');
  render(
    <SettingsProvider settings={settings} setSetting={vi.fn()}>
      <LocationPicker
        isOpen
        purpose="workspace"
        onClose={vi.fn()}
        onSelect={onSelect}
        onInspectPath={onInspectPath}
        agentAvailability={{ shell: true, claude: true, codex: true, snipe: true }}
        endpoints={[]}
      />
    </SettingsProvider>,
  );
  return { onSelect };
}

const launch = () => {
  const input = screen.getByTestId('location-picker-path-input');
  fireEvent.change(input, { target: { value: '/home/remote/projects' } });
  fireEvent.keyDown(input, { key: 'Enter' });
};

const pickSnipe = () => fireEvent.click(screen.getByRole('radio', { name: /snipe/i }));

describe('LocationPicker auto mode toggle', () => {
  beforeEach(() => {
    useFilesystemSuggestionsMock.mockReset();
    useFilesystemSuggestionsMock.mockReturnValue({
      suggestions: [], loading: false, error: null, currentDir: '',
    });
  });

  it('offers the toggle only for an agent whose driver advertises auto mode', () => {
    renderPicker(PI_SETTINGS);
    expect(screen.queryByTestId('location-picker-automode-toggle')).not.toBeInTheDocument();

    pickSnipe();
    expect(screen.getByTestId('location-picker-automode-toggle')).toBeInTheDocument();
  });

  // The launcher sees what the session will actually get, which means starting
  // from the promoted default rather than from a hardcoded guess.
  it('starts from the promoted default', () => {
    renderPicker({ ...PI_SETTINGS, automode_enabled_default: 'false' });
    pickSnipe();

    expect(screen.getByTestId('location-picker-automode-toggle')).toHaveAttribute('aria-checked', 'false');
  });

  it('sends the default through when the launcher leaves it alone', async () => {
    const { onSelect } = renderPicker(PI_SETTINGS);
    pickSnipe();
    launch();

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects', 'snipe', undefined, false, false, undefined, true);
    });
  });

  // Turning it off is the whole point of a per-session override, and "off" has
  // to travel as an answer — not as an absent field the daemon reads as
  // "follow the default", which is what it was just overridden away from.
  it('sends an explicit off when the launcher turns it off', async () => {
    const { onSelect } = renderPicker(PI_SETTINGS);
    pickSnipe();
    fireEvent.click(screen.getByTestId('location-picker-automode-toggle'));
    launch();

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects', 'snipe', undefined, false, false, undefined, false);
    });
  });

  it('says nothing about auto mode for an agent that has none', async () => {
    const { onSelect } = renderPicker(PI_SETTINGS);
    launch();

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects', 'claude', undefined, false, false, undefined, undefined);
    });
  });
});
