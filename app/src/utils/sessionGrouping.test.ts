import { describe, expect, it } from 'vitest';
import { getVisualSessionOrder, groupSessionsByDirectory } from './sessionGrouping';

interface TestSession {
  id: string;
  label: string;
  cwd?: string;
  branch?: string;
  endpointId?: string;
  endpointName?: string;
}

describe('sessionGrouping', () => {
  it('groups non-contiguous sessions from the same directory in first-seen group order', () => {
    const sessions: TestSession[] = [
      { id: 'a1', label: 'A1', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', cwd: '/repo/b' },
      { id: 'a2', label: 'A2', cwd: '/repo/a' },
      { id: 'n1', label: 'N1' },
    ];

    const groups = groupSessionsByDirectory(sessions);

    expect(groups.map((group) => group.directory)).toEqual(['/repo/a', '/repo/b', 'n1']);
    expect(groups.map((group) => group.sessions.map((session) => session.id))).toEqual([
      ['a1', 'a2'],
      ['b1'],
      ['n1'],
    ]);
  });

  it('returns a flattened visual order that matches grouped sidebar rendering', () => {
    const sessions: TestSession[] = [
      { id: 'a1', label: 'A1', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', cwd: '/repo/b' },
      { id: 'a2', label: 'A2', cwd: '/repo/a' },
      { id: 'n1', label: 'N1' },
    ];

    const visualOrder = getVisualSessionOrder(sessions);
    expect(visualOrder.map((session) => session.id)).toEqual(['a1', 'a2', 'b1', 'n1']);
  });

  it('keeps same-directory sessions from different endpoints in separate groups', () => {
    const sessions: TestSession[] = [
      { id: 'local-1', label: 'Local', cwd: '/repo/a' },
      { id: 'remote-1', label: 'Remote A', cwd: '/repo/a', endpointId: 'ep-a', endpointName: 'gpu-box' },
      { id: 'remote-2', label: 'Remote B', cwd: '/repo/a', endpointId: 'ep-b', endpointName: 'dev-box' },
    ];

    const groups = groupSessionsByDirectory(sessions);

    expect(groups).toHaveLength(3);
    expect(groups.map((group) => group.endpointName || 'local')).toEqual(['local', 'gpu-box', 'dev-box']);
    expect(groups.map((group) => group.sessions.map((session) => session.id))).toEqual([
      ['local-1'],
      ['remote-1'],
      ['remote-2'],
    ]);
  });
});
