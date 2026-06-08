import { describe, expect, it } from 'vitest';
import { computeWarmWorkspaceIds } from './terminalVirtualization';

describe('computeWarmWorkspaceIds', () => {
  it('returns null (all live) when virtualization is disabled', () => {
    expect(computeWarmWorkspaceIds(['a', 'b', 'c'], 'a', -1)).toBeNull();
  });

  it('keeps only the active workspace at limit 0', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c'], 'a', 0);
    expect(warm).toEqual(new Set(['a']));
  });

  it('keeps active + N most-recent at the given limit', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c', 'd', 'e'], 'a', 3);
    expect(warm).toEqual(new Set(['a', 'b', 'c', 'd']));
    expect(warm?.size).toBe(4); // active + 3
  });

  it('always includes the active workspace even if absent from recents', () => {
    const warm = computeWarmWorkspaceIds(['b', 'c'], 'z', 1);
    expect(warm?.has('z')).toBe(true);
    // active(z) + 1 recent(b)
    expect(warm).toEqual(new Set(['z', 'b']));
  });

  it('does not double-count the active workspace when it appears in recents', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c'], 'a', 2);
    // active(a) + 2 others (b, c) = 3, not a duplicated
    expect(warm).toEqual(new Set(['a', 'b', 'c']));
    expect(warm?.size).toBe(3);
  });

  it('handles a null active workspace by filling the budget from recents', () => {
    // No active workspace to reserve a slot for, so the full budget (limit + 1)
    // is taken from the most-recent workspaces.
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c'], null, 1);
    expect(warm).toEqual(new Set(['a', 'b']));
  });
});
