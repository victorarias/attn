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

function renderPicker(props?: Partial<React.ComponentProps<typeof LocationPicker>>) {
  const onSelect = vi.fn();
  render(
    <SettingsProvider settings={{}} setSetting={vi.fn()}>
      <LocationPicker
        isOpen
        onClose={vi.fn()}
        onSelect={onSelect}
        endpoints={[]}
        {...props}
      />
    </SettingsProvider>,
  );
  return { onSelect };
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
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects/remote-repo', 'claude', 'ep-1');
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
});
