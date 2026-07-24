import { describe, it, expect } from 'vitest';
import { resolveMarkdownOpenerTarget } from './openerTarget';

describe('resolveMarkdownOpenerTarget', () => {
  it('searches a local session’s working directory', () => {
    expect(resolveMarkdownOpenerTarget({ id: 's1', cwd: '/repo' }, '/notebook')).toEqual({
      root: '/repo',
      sessionId: 's1',
    });
  });

  it('never indexes or binds a remote session’s directory', () => {
    // The remote cwd names a path on another machine: handing it to fs_index
    // would enumerate this machine's filesystem under a remote label, and
    // open_markdown would bind a local file to a session the local daemon does
    // not own. Fall back to the local notebook root, with no session binding.
    const target = resolveMarkdownOpenerTarget(
      { id: 's1', cwd: '/repo', endpointId: 'orb-remote' },
      '/notebook',
    );
    expect(target).toEqual({ root: '/notebook', sessionId: null });
  });

  it('falls back to recents alone when a remote session is selected and there is no notebook root', () => {
    expect(resolveMarkdownOpenerTarget({ id: 's1', cwd: '/repo', endpointId: 'orb' }, '')).toEqual({
      root: null,
      sessionId: null,
    });
  });

  it('uses the notebook root when no session is selected', () => {
    expect(resolveMarkdownOpenerTarget(undefined, '/notebook')).toEqual({
      root: '/notebook',
      sessionId: '',
    });
  });

  it('falls back to the notebook root when a local session has no cwd', () => {
    expect(resolveMarkdownOpenerTarget({ id: 's1' }, '/notebook')).toEqual({
      root: '/notebook',
      sessionId: 's1',
    });
  });

  it('reports no root when nothing local is known', () => {
    expect(resolveMarkdownOpenerTarget(undefined, null)).toEqual({ root: null, sessionId: '' });
  });
});
