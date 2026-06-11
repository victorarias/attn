import { describe, expect, it } from 'vitest';
import { computeWarmWorkspaceIds } from './terminalVirtualization';

describe('computeWarmWorkspaceIds', () => {
  it('returns null (all live) when virtualization is disabled', () => {
    expect(computeWarmWorkspaceIds(['a', 'b', 'c'], ['a', 'b', 'c'], 'a', -1)).toBeNull();
  });

  it('keeps only selected and recent workspaces live even within the warm budget', () => {
    expect(computeWarmWorkspaceIds(['a'], ['a'], 'a', 3)).toEqual(new Set(['a']));
    expect(computeWarmWorkspaceIds(['a', 'b', 'c', 'd'], ['a'], 'a', 3)).toEqual(new Set(['a']));
  });

  it('keeps terminals cold on the dashboard before a workspace is selected', () => {
    expect(computeWarmWorkspaceIds(['a', 'b'], [], null, 3)).toEqual(new Set());
  });

  it('keeps only the active workspace at limit 0 once over budget', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c'], ['a', 'b', 'c'], 'a', 0);
    expect(warm).toEqual(new Set(['a']));
  });

  it('keeps active + N most-recent at the given limit', () => {
    const warm = computeWarmWorkspaceIds(
      ['a', 'b', 'c', 'd', 'e'],
      ['a', 'b', 'c', 'd', 'e'],
      'a',
      3,
    );
    expect(warm).toEqual(new Set(['a', 'b', 'c', 'd']));
    expect(warm?.size).toBe(4); // active + 3
  });

  it('always includes the active workspace even if absent from recents', () => {
    const warm = computeWarmWorkspaceIds(['z', 'b', 'c'], ['b', 'c'], 'z', 1);
    expect(warm?.has('z')).toBe(true);
    // active(z) + 1 recent(b)
    expect(warm).toEqual(new Set(['z', 'b']));
  });

  it('does not double-count the active workspace when it appears in recents', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c', 'd'], ['a', 'b', 'c'], 'a', 2);
    // active(a) + 2 others (b, c) = 3, a not duplicated
    expect(warm).toEqual(new Set(['a', 'b', 'c']));
    expect(warm?.size).toBe(3);
  });

  it('handles a null active workspace by filling the budget from recents', () => {
    // No active workspace to reserve a slot for, so the full budget (limit + 1)
    // is taken from the most-recent workspaces.
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c'], ['a', 'b', 'c'], null, 1);
    expect(warm).toEqual(new Set(['a', 'b']));
  });

  it('does not eagerly fill unused warm slots before recency is established', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c', 'd', 'e'], [], 'a', 2);
    expect(warm).toEqual(new Set(['a']));
  });

  it('ignores stale active/recent ids no longer among current workspaces', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c', 'd'], ['x', 'b'], 'y', 1);
    expect(warm).toEqual(new Set(['b']));
  });

  it('keeps required visible workspaces live even when they are cold', () => {
    const warm = computeWarmWorkspaceIds(
      ['a', 'b', 'c', 'd', 'e'],
      ['a', 'b'],
      'a',
      1,
      ['e'],
    );
    expect(warm).toEqual(new Set(['e', 'a']));
  });

  it('lets required visible workspaces exceed the warm budget', () => {
    const warm = computeWarmWorkspaceIds(
      ['a', 'b', 'c', 'd', 'e'],
      ['a', 'b'],
      'a',
      1,
      ['d', 'e'],
    );
    expect(warm).toEqual(new Set(['d', 'e', 'a']));
  });

  it('ignores stale required workspace ids', () => {
    const warm = computeWarmWorkspaceIds(
      ['a', 'b', 'c', 'd'],
      ['a', 'b', 'c'],
      'a',
      1,
      ['missing'],
    );
    expect(warm).toEqual(new Set(['a', 'b']));
  });
});
