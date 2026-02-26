import { describe, expect, it } from 'vitest';
import { getVisualSessionOrder, groupSessionsByDirectory } from './sessionGrouping';

interface TestSession {
  id: string;
  label: string;
  cwd?: string;
  branch?: string;
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
});
