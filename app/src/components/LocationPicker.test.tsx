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
    await waitFor(() => {
      expect(screen.getByText(/Browsing, repo inspection, and worktree actions run on gpu-box/i)).toBeInTheDocument();
    });
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
});
