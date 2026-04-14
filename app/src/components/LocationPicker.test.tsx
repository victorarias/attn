import { useState, type ComponentProps } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { LocationPicker } from './LocationPicker';
import { SettingsProvider } from '../contexts/SettingsContext';
import type { RecentLocation } from '../hooks/useDaemonSocket';

const useFilesystemSuggestionsMock = vi.fn();

vi.mock('../hooks/useFilesystemSuggestions', () => ({
  useFilesystemSuggestions: (...args: unknown[]) => useFilesystemSuggestionsMock(...args),
}));

type LocationPickerProps = ComponentProps<typeof LocationPicker>;

function buildRecentLocation(overrides?: Partial<RecentLocation>) {
  return {
    label: 'Recent Repo',
    path: '/home/remote/projects/recent-repo',
    last_seen: '2026-04-11T08:30:00Z',
    use_count: 4,
    ...overrides,
  };
}

function buildRepoInfo(overrides?: Partial<{
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Array<{ path: string; branch: string }>;
}>) {
  return {
    repo: '/home/remote/projects/exsin',
    current_branch: 'main',
    current_commit_hash: 'abcdef1234567890',
    current_commit_time: '2026-04-03T18:00:00Z',
    default_branch: 'main',
    worktrees: [{ path: '/home/remote/projects/exsin--feat-images', branch: 'feat-images' }],
    ...overrides,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function renderPicker(
  props?: Partial<LocationPickerProps>,
  options?: { settings?: Record<string, string>; setSetting?: (key: string, value: string) => void },
) {
  const onSelect = vi.fn();
  const onClose = vi.fn();
  const setSetting = options?.setSetting ?? vi.fn();
  render(
    <SettingsProvider settings={options?.settings ?? {}} setSetting={setSetting}>
      <LocationPicker
        isOpen
        onClose={onClose}
        onSelect={onSelect}
        endpoints={[]}
        {...props}
      />
    </SettingsProvider>,
  );
  return { onClose, onSelect, setSetting };
}

function renderClosablePicker(props?: Partial<LocationPickerProps>) {
  const onSelect = vi.fn();

  function Wrapper() {
    const [isOpen, setIsOpen] = useState(true);
    return (
      <SettingsProvider settings={{}} setSetting={vi.fn()}>
        <button type="button" onClick={() => setIsOpen(true)}>
          Reopen
        </button>
        <LocationPicker
          isOpen={isOpen}
          onClose={() => setIsOpen(false)}
          onSelect={onSelect}
          endpoints={[]}
          {...props}
        />
      </SettingsProvider>
    );
  }

  render(<Wrapper />);
  return { onSelect };
}

describe('LocationPicker', () => {
  beforeEach(() => {
    useFilesystemSuggestionsMock.mockReset();
    useFilesystemSuggestionsMock.mockImplementation((inputPath: string) => ({
      suggestions: inputPath.startsWith('~/pro')
        ? [
            { name: 'projects', path: '~/projects' },
            { name: 'project-archive', path: '~/project-archive' },
          ]
        : inputPath.startsWith('~/projects')
          ? [
              { name: 'alpha', path: '~/projects/alpha' },
              { name: 'beta', path: '~/projects/beta' },
            ]
          : [],
      loading: false,
      error: null,
      currentDir: inputPath.startsWith('~/projects') ? '~/' : '',
    }));
  });

  it('spawns a remote session with the selected endpoint id', async () => {
    const onGetRecentLocations = vi.fn(async () => ({ locations: [], home_path: '/home/remote' }));
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
    const onGetRepoInfo = vi.fn();
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
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('~/projects/remote-repo', 'ep-1');
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects/remote-repo', 'claude', 'ep-1', false);
    });
    expect(onGetRepoInfo).not.toHaveBeenCalled();
  });

  it('arrow navigation keeps the input stable and Enter opens the highlighted row', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects',
        resolved_path: '/home/remote/projects',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect } = renderPicker({ onInspectPath });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });
    fireEvent.keyDown(input, { key: 'ArrowDown' });

    await waitFor(() => {
      expect(input.value).toBe('~/pro');
      expect(screen.getByTestId('location-picker-item-0')).toHaveClass('selected');
    });

    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('~/projects', undefined);
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects', 'claude', undefined, false);
    });
  });

  it('clicking a row selects it immediately', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects',
        resolved_path: '/home/remote/projects',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect } = renderPicker({ onInspectPath });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });
    fireEvent.click(screen.getByTestId('location-picker-item-0'));

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('~/projects', undefined);
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects', 'claude', undefined, false);
    });
  });

  it('hover does not change the input or selection', () => {
    renderPicker();

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });

    const firstItem = screen.getByTestId('location-picker-item-0');
    fireEvent.mouseEnter(firstItem);

    expect(input.value).toBe('~/pro');
    expect(firstItem).not.toHaveClass('selected');
  });

  it('ArrowDown on window does not affect picker input or item selection', () => {
    renderPicker();

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });

    fireEvent.keyDown(window, { key: 'ArrowDown' });
    expect(input.value).toBe('~/pro');
    expect(screen.queryByTestId('location-picker-item-0')).not.toHaveClass?.('selected');
  });

  it('Escape first deselects highlighted item then closes', async () => {
    const { onClose } = renderPicker();

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    await waitFor(() => {
      expect(screen.getByTestId('location-picker-item-0')).toHaveClass('selected');
    });

    // First Escape deselects without closing
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByTestId('location-picker-item-0')).not.toHaveClass('selected');

    // Second Escape closes
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('tabs ghost text into the input query', async () => {
    renderPicker();

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });

    await waitFor(() => {
      expect(screen.getByText('jects')).toBeInTheDocument();
    });

    fireEvent.keyDown(input, { key: 'Tab' });

    expect(input.value).toBe('~/projects');
    expect(screen.queryByText('jects')).not.toBeInTheDocument();
  });

  it('tabs the highlighted row into the input query before submit', async () => {
    renderPicker();

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/pro' } });
    fireEvent.keyDown(input, { key: 'ArrowDown' });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-item-0')).toHaveClass('selected');
      expect(input.value).toBe('~/pro');
    });

    fireEvent.keyDown(input, { key: 'Tab' });

    expect(input.value).toBe('~/projects');
    expect(screen.getByTestId('location-picker-item-0')).not.toHaveClass('selected');
  });

  it('opens repo options with the matching worktree preselected for exact worktree paths with or without trailing slash', async () => {
    const onInspectPath = vi.fn(async (inputPath: string) => ({
      success: true,
      inspection: {
        input_path: inputPath,
        resolved_path: '/home/remote/projects/exsin--feat-images',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn(async () => ({
      success: true,
      info: buildRepoInfo(),
    }));

    for (const typedPath of ['~/projects/exsin--feat-images', '~/projects/exsin--feat-images/']) {
      const { onSelect } = renderPicker({ onInspectPath, onGetRepoInfo });
      const input = screen.getByRole('textbox');
      fireEvent.change(input, { target: { value: typedPath } });
      fireEvent.keyDown(input, { key: 'Enter' });

      await waitFor(() => {
        expect(screen.getByTestId('repo-options')).toBeInTheDocument();
        expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
      });

      expect(onSelect).not.toHaveBeenCalled();
    }
  });

  it('submits root paths without rewriting slash to the current directory', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '/',
        resolved_path: '/',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect } = renderPicker({ onInspectPath });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '/' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('/', undefined);
      expect(onSelect).toHaveBeenCalledWith('/', 'claude', undefined, false);
    });
  });

  it('opens a typed directory directly instead of implicitly selecting a child like .claude', async () => {
    useFilesystemSuggestionsMock.mockImplementation((inputPath: string) => ({
      suggestions: inputPath === '/tmp/project/'
        ? [{ name: '.claude', path: '/tmp/project/.claude' }]
        : [],
      loading: false,
      error: null,
      currentDir: inputPath === '/tmp/project/' ? '/tmp/project' : '',
    }));

    const onInspectPath = vi.fn(async (inputPath: string) => ({
      success: true,
      inspection: {
        input_path: inputPath,
        resolved_path: '/tmp/project',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
    }));
    const { onSelect } = renderPicker({ onInspectPath });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '/tmp/project/' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('/tmp/project', undefined);
      expect(onSelect).toHaveBeenCalledWith('/tmp/project', 'claude', undefined, false);
    });
  });

  it('filters recent locations for ~/ and absolute queries without rewriting the input', async () => {
    const onGetRecentLocations = vi.fn(async () => ({
      locations: [buildRecentLocation()],
      home_path: '/home/remote',
    }));
    renderPicker({ onGetRecentLocations });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/projects/recent-repo' } });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-item-0')).toBeInTheDocument();
      expect(screen.getByTestId('location-picker-item-0')).not.toHaveClass('selected');
      expect(input.value).toBe('~/projects/recent-repo');
    });

    fireEvent.change(input, { target: { value: '/home/remote/projects/recent-repo' } });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-item-0')).toBeInTheDocument();
      expect(screen.getByTestId('location-picker-item-0')).not.toHaveClass('selected');
      expect(input.value).toBe('/home/remote/projects/recent-repo');
    });
  });

  it('creates a worktree from the selected worktree branch and opens it immediately', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin--feat-images',
        resolved_path: '/home/remote/projects/exsin--feat-images',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn(async () => ({
      success: true,
      info: buildRepoInfo(),
    }));
    const onCreateWorktree = vi.fn(async () => ({
      success: true,
      path: '/home/remote/projects/exsin--feat-more',
    }));
    const { onSelect } = renderPicker({ onInspectPath, onGetRepoInfo, onCreateWorktree });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin--feat-images' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(screen.getByTestId('repo-options')).toBeInTheDocument();
      expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    });

    fireEvent.click(screen.getByTestId('repo-option-2'));
    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feat-more' } });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => {
      expect(onCreateWorktree).toHaveBeenCalledWith(
        '/home/remote/projects/exsin',
        'feat-more',
        undefined,
        'feat-images',
        undefined,
      );
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects/exsin--feat-more', 'claude', undefined, false);
    });
  });

  it('supports dialog-level shortcuts while focus is in the chooser', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin--feat-images',
        resolved_path: '/home/remote/projects/exsin--feat-images',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn(async () => ({
      success: true,
      info: buildRepoInfo(),
    }));
    renderPicker({
      onInspectPath,
      onGetRepoInfo,
      agentAvailability: {
        claude: true,
        codex: true,
        copilot: false,
        pi: false,
      },
    });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin--feat-images' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(screen.getByTestId('repo-options')).toBeInTheDocument();
    });

    const chooser = screen.getByTestId('repo-options');
    chooser.focus();
    fireEvent.keyDown(chooser, { key: '™', code: 'Digit2', altKey: true });

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /codex/i })).toHaveAttribute('aria-checked', 'true');
    });
  });

  it('keeps adjacent worktree selection after deletion', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin--feat-b',
        resolved_path: '/home/remote/projects/exsin--feat-b',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn()
      .mockResolvedValueOnce({
        success: true,
        info: buildRepoInfo({
          worktrees: [
            { path: '/home/remote/projects/exsin--feat-a', branch: 'feat-a' },
            { path: '/home/remote/projects/exsin--feat-b', branch: 'feat-b' },
          ],
        }),
      })
      .mockResolvedValueOnce({
        success: true,
        info: buildRepoInfo({
          worktrees: [{ path: '/home/remote/projects/exsin--feat-a', branch: 'feat-a' }],
        }),
      });
    const onDeleteWorktree = vi.fn(async () => ({ success: true }));
    renderPicker({ onInspectPath, onGetRepoInfo, onDeleteWorktree });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin--feat-b' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(screen.getByTestId('repo-option-2')).toHaveClass('selected');
    });

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'y' });

    await waitFor(() => {
      expect(onDeleteWorktree).toHaveBeenCalledWith('/home/remote/projects/exsin--feat-b', undefined);
      expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    });
  });

  it('keeps the same committed worktree selected after refresh while the chooser is open', async () => {
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin--feat-images',
        resolved_path: '/home/remote/projects/exsin--feat-images',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn()
      .mockResolvedValueOnce({
        success: true,
        info: buildRepoInfo(),
      })
      .mockResolvedValueOnce({
        success: true,
        info: buildRepoInfo(),
      });
    renderPicker({ onInspectPath, onGetRepoInfo });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin--feat-images' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    });

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'r' });

    await waitFor(() => {
      expect(onGetRepoInfo).toHaveBeenCalledTimes(2);
      expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    });
  });

  // The Playwright harness drives a single local daemon today, so remote parity is
  // proven at the component-integration level here rather than full browser E2E.
  it('applies the same exact-path worktree behavior on remote endpoints', async () => {
    const onInspectPath = vi.fn(async (inputPath: string, endpointId?: string) => ({
      success: true,
      inspection: {
        input_path: inputPath,
        resolved_path: '/home/remote/projects/exsin--feat-images',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
      endpoint_id: endpointId,
    }));
    const onGetRepoInfo = vi.fn(async (_repo: string, endpointId?: string) => ({
      success: true,
      info: buildRepoInfo(),
      endpoint_id: endpointId,
    }));
    renderPicker({
      onInspectPath,
      onGetRepoInfo,
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
    fireEvent.change(input, { target: { value: '~/projects/exsin--feat-images/' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('~/projects/exsin--feat-images', 'ep-1');
      expect(onGetRepoInfo).toHaveBeenCalledWith('/home/remote/projects/exsin', 'ep-1');
      expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    });
  });

  it('preserves root-path submission on remote endpoints too', async () => {
    const onInspectPath = vi.fn(async (inputPath: string, endpointId?: string) => ({
      success: true,
      inspection: {
        input_path: inputPath,
        resolved_path: '/',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
      },
      endpoint_id: endpointId,
    }));
    const { onSelect } = renderPicker({
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
    fireEvent.change(input, { target: { value: '/' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onInspectPath).toHaveBeenCalledWith('/', 'ep-1');
      expect(onSelect).toHaveBeenCalledWith('/', 'claude', 'ep-1', false);
    });
  });

  it('ignores stale inspect responses after the target changes', async () => {
    const inspectGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onInspectPath']>>>>();
    const onInspectPath = vi.fn(() => inspectGate.promise);
    const onGetRepoInfo = vi.fn();
    renderPicker({
      onInspectPath,
      onGetRepoInfo,
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

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    fireEvent.click(screen.getByRole('radio', { name: /gpu-box/i }));
    inspectGate.resolve({
      success: true,
      inspection: {
        input_path: '~/projects/exsin',
        resolved_path: '/home/remote/projects/exsin',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    });

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /gpu-box/i })).toHaveAttribute('aria-checked', 'true');
    });

    expect(onGetRepoInfo).not.toHaveBeenCalled();
    expect(screen.queryByTestId('repo-options')).not.toBeInTheDocument();
  });

  it('ignores stale inspect responses after the user changes the input', async () => {
    const inspectGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onInspectPath']>>>>();
    const onInspectPath = vi.fn(() => inspectGate.promise);
    const onGetRepoInfo = vi.fn();
    renderPicker({ onInspectPath, onGetRepoInfo });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    fireEvent.change(screen.getByRole('textbox'), { target: { value: '/tmp/other' } });
    inspectGate.resolve({
      success: true,
      inspection: {
        input_path: '~/projects/exsin',
        resolved_path: '/home/remote/projects/exsin',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    });

    await waitFor(() => {
      expect(screen.getByRole('textbox')).toHaveValue('/tmp/other');
    });

    expect(onGetRepoInfo).not.toHaveBeenCalled();
    expect(screen.queryByTestId('repo-options')).not.toBeInTheDocument();
  });

  it('ignores stale inspect responses after the dialog closes and reopens', async () => {
    const inspectGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onInspectPath']>>>>();
    const onInspectPath = vi.fn(() => inspectGate.promise);
    const onGetRepoInfo = vi.fn();
    renderClosablePicker({ onInspectPath, onGetRepoInfo });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    fireEvent.click(screen.getByTestId('location-picker-overlay'));
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));

    inspectGate.resolve({
      success: true,
      inspection: {
        input_path: '~/projects/exsin',
        resolved_path: '/home/remote/projects/exsin',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-title')).toBeInTheDocument();
    });

    expect(onGetRepoInfo).not.toHaveBeenCalled();
    expect(screen.queryByTestId('repo-options')).not.toBeInTheDocument();
  });

  it('ignores stale repo-info responses after the dialog closes and reopens', async () => {
    const repoInfoGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onGetRepoInfo']>>>>();
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin',
        resolved_path: '/home/remote/projects/exsin',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn(() => repoInfoGate.promise);
    renderClosablePicker({ onInspectPath, onGetRepoInfo });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onGetRepoInfo).toHaveBeenCalledWith('/home/remote/projects/exsin', undefined);
    });

    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));

    repoInfoGate.resolve({
      success: true,
      info: buildRepoInfo(),
    });

    await waitFor(() => {
      expect(screen.getByTestId('location-picker-title')).toBeInTheDocument();
    });

    expect(screen.queryByTestId('repo-options')).not.toBeInTheDocument();
  });

  it('suppresses stale inspect errors after the picker state has changed', async () => {
    const inspectGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onInspectPath']>>>>();
    const onInspectPath = vi.fn(() => inspectGate.promise);
    const onError = vi.fn();
    renderPicker({
      onInspectPath,
      onError,
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

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    fireEvent.click(screen.getByRole('radio', { name: /gpu-box/i }));
    inspectGate.reject(new Error('stale failure'));

    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /gpu-box/i })).toHaveAttribute('aria-checked', 'true');
    });

    expect(onError).not.toHaveBeenCalled();
  });

  it('ignores stale worktree creation results after the picker closes', async () => {
    const createGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onCreateWorktree']>>>>();
    const onInspectPath = vi.fn(async () => ({
      success: true,
      inspection: {
        input_path: '~/projects/exsin--feat-images',
        resolved_path: '/home/remote/projects/exsin--feat-images',
        home_path: '/home/remote',
        exists: true,
        is_directory: true,
        repo_root: '/home/remote/projects/exsin',
      },
    }));
    const onGetRepoInfo = vi.fn(async () => ({
      success: true,
      info: buildRepoInfo(),
    }));
    const onCreateWorktree = vi.fn(() => createGate.promise);
    const { onSelect } = renderClosablePicker({ onInspectPath, onGetRepoInfo, onCreateWorktree });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin--feat-images' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(screen.getByTestId('repo-options')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByTestId('repo-option-2'));
    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feat-more' } });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });
    fireEvent.click(screen.getByTestId('location-picker-overlay'));

    createGate.resolve({
      success: true,
      path: '/home/remote/projects/exsin--feat-more',
    });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument();
    });

    expect(onSelect).not.toHaveBeenCalled();
  });

  it('ignores stale repo-info responses after a newer submission launches a different path', async () => {
    const firstRepoInfoGate = deferred<Awaited<ReturnType<NonNullable<LocationPickerProps['onGetRepoInfo']>>>>();
    const onInspectPath = vi.fn(async (inputPath: string) => {
      if (inputPath === '~/projects/exsin') {
        return {
          success: true,
          inspection: {
            input_path: inputPath,
            resolved_path: '/home/remote/projects/exsin',
            home_path: '/home/remote',
            exists: true,
            is_directory: true,
            repo_root: '/home/remote/projects/exsin',
          },
        };
      }
      return {
        success: true,
        inspection: {
          input_path: inputPath,
          resolved_path: '/tmp/other',
          home_path: '/home/remote',
          exists: true,
          is_directory: true,
        },
      };
    });
    const onGetRepoInfo = vi.fn(async () => firstRepoInfoGate.promise);
    const { onSelect } = renderClosablePicker({ onInspectPath, onGetRepoInfo });

    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '~/projects/exsin' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onGetRepoInfo).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByRole('textbox'), { target: { value: '/tmp/other' } });
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('/tmp/other', 'claude', undefined, false);
    });

    firstRepoInfoGate.resolve({
      success: true,
      info: buildRepoInfo(),
    });

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledTimes(1);
    });

    expect(screen.queryByTestId('repo-options')).not.toBeInTheDocument();
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
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('/home/remote/projects/remote-repo', 'claude', 'ep-1', true);
    });
  });
});
