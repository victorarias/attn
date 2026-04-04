import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { LocationPicker } from './LocationPicker';
import { SettingsProvider } from '../contexts/SettingsContext';

vi.mock('../hooks/useFilesystemSuggestions', () => ({
  useFilesystemSuggestions: vi.fn(() => ({
    suggestions: [],
    loading: false,
    error: null,
    currentDir: '',
    homePath: '/home/remote',
  })),
}));

function renderPicker(
  props?: Partial<React.ComponentProps<typeof LocationPicker>>,
  options?: { settings?: Record<string, string>; setSetting?: (key: string, value: string) => void },
) {
  const onSelect = vi.fn();
  const setSetting = options?.setSetting ?? vi.fn();
  render(
    <SettingsProvider settings={options?.settings ?? {}} setSetting={setSetting}>
      <LocationPicker
        isOpen
        onClose={vi.fn()}
        onSelect={onSelect}
        endpoints={[]}
        {...props}
      />
    </SettingsProvider>,
  );
  return { onSelect, setSetting };
}

describe('LocationPicker', () => {
  it('spawns a remote session with the selected endpoint id', async () => {
    const onGetRecentLocations = vi.fn(async () => ({ locations: [], home_path: '/home/remote' }));
    const onGetRepoInfo = vi.fn();
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/remote-repo',
        resolved_path: '/home/remote/projects/remote-repo',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect } = renderPicker({
      onGetRecentLocations,
      onGetRepoInfo,
      onInspectPath,
      endpoints: [{
        id: 'ep-1',
        name: 'gpu-box',
        ssh_target: 'ai-sandbox',
        status: 'connected',
        enabled: true,
        capabilities: {
          protocol_version: '46',
          agents_available: ['codex'],
          projects_directory: '/srv/projects',
        },
      }],
    });

    fireEvent.click(screen.getByRole('radio', { name: /gpu-box/i }));
    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/remote-repo' } });
    fireEvent.focus(input);
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('~/projects/remote-repo', 'ep-1');
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects/remote-repo', 'claude', 'ep-1', false);
    });
    expect(onGetRepoInfo).not.toHaveBeenCalled();
  });

  it('switches targets with alt key shortcuts in UI order', async () => {
    renderPicker({
      projectsDirectory: '/Users/victor/projects',
      endpoints: [
        {
          id: 'ep-1',
          name: 'gpu-box',
          ssh_target: 'ai-sandbox',
          status: 'connected',
          enabled: true,
          capabilities: {
            protocol_version: '46',
            agents_available: ['codex'],
            projects_directory: '/srv/projects',
          },
        },
        {
          id: 'ep-2',
          name: 'lab-box',
          ssh_target: 'lab-box',
          status: 'connected',
          enabled: true,
          capabilities: {
            protocol_version: '46',
            agents_available: ['codex'],
            projects_directory: '/opt/work',
          },
        },
      ],
    });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(input.value).toBe('/Users/victor/projects/');

    input.focus();

    fireEvent.keyDown(input, { key: '∑', code: 'KeyW', altKey: true });
    await waitFor(() => {
      expect(input.value).toBe('/srv/projects/');
      expect(screen.getByRole('radio', { name: /gpu-box/i })).toHaveAttribute('aria-checked', 'true');
    });

    fireEvent.keyDown(input, { key: '€', code: 'KeyE', altKey: true });
    await waitFor(() => {
      expect(input.value).toBe('/opt/work/');
      expect(screen.getByRole('radio', { name: /lab-box/i })).toHaveAttribute('aria-checked', 'true');
    });

    fireEvent.keyDown(input, { key: 'œ', code: 'KeyQ', altKey: true });
    await waitFor(() => {
      expect(input.value).toBe('/Users/victor/projects/');
      expect(screen.getByRole('radio', { name: /local/i })).toHaveAttribute('aria-checked', 'true');
    });
  });

  it('does not advertise or apply shortcuts for disconnected endpoints', async () => {
    renderPicker({
      projectsDirectory: '/Users/victor/projects',
      endpoints: [
        {
          id: 'ep-1',
          name: 'gpu-box',
          ssh_target: 'ai-sandbox',
          status: 'disconnected',
          enabled: true,
          capabilities: {
            protocol_version: '46',
            agents_available: ['codex'],
            projects_directory: '/srv/projects',
          },
        },
      ],
    });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(input.value).toBe('/Users/victor/projects/');
    expect(screen.queryByText('⌥W')).not.toBeInTheDocument();

    input.focus();
    fireEvent.keyDown(input, { key: '∑', code: 'KeyW', altKey: true });

    await waitFor(() => {
      expect(input.value).toBe('/Users/victor/projects/');
      expect(screen.getByRole('radio', { name: /local/i })).toHaveAttribute('aria-checked', 'true');
      expect(screen.getByRole('radio', { name: /gpu-box/i })).toHaveAttribute('aria-checked', 'false');
    });
  });

  it('does not advertise or apply shortcuts for unavailable agents', async () => {
    renderPicker({
      agentAvailability: {
        claude: true,
        codex: false,
        copilot: true,
        pi: false,
      },
    });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(screen.queryByText('⌥2')).not.toBeInTheDocument();

    input.focus();
    fireEvent.keyDown(input, { key: '™', code: 'Digit2', altKey: true });

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /claude/i })).toHaveAttribute('aria-checked', 'true');
      expect(screen.getByRole('radio', { name: /copilot/i })).toHaveAttribute('aria-checked', 'false');
    });
  });

  it('persists yolo preference per remote daemon and launches with attn yolo enabled', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/remote-repo',
        resolved_path: '/home/remote/projects/remote-repo',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect, setSetting } = renderPicker({
      onInspectPath,
      endpoints: [{
        id: 'ep-1',
        name: 'gpu-box',
        ssh_target: 'ai-sandbox',
        status: 'connected',
        enabled: true,
        capabilities: {
          protocol_version: '47',
          daemon_instance_id: 'daemon-remote-1',
          agents_available: ['claude'],
          projects_directory: '/srv/projects',
        },
      }],
    }, {
      settings: {
        claude_cap_yolo: 'true',
      },
    });

    const endpoint = screen.getByRole('radio', { name: /gpu-box/i });
    fireEvent.click(endpoint);
    fireEvent.click(endpoint);

    expect(setSetting).toHaveBeenCalledWith('new_session_yolo_daemon_daemon-remote-1', 'true');

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/remote-repo' } });
    fireEvent.focus(input);
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects/remote-repo', 'claude', 'ep-1', true);
    });
  });
});
