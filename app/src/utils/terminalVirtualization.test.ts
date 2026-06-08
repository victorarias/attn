import { describe, expect, it } from 'vitest';
import { computeWarmWorkspaceIds } from './terminalVirtualization';

describe('computeWarmWorkspaceIds', () => {
  it('returns null (all live) when virtualization is disabled', () => {
    expect(computeWarmWorkspaceIds(['a', 'b', 'c'], ['a', 'b', 'c'], 'a', -1)).toBeNull();
  });

  it('keeps all live when workspace count is within the warm budget', () => {
    // budget = limit + 1 = 4; with <= 4 workspaces there is nothing to reclaim.
    expect(computeWarmWorkspaceIds(['a'], ['a'], 'a', 3)).toBeNull();
    expect(computeWarmWorkspaceIds(['a', 'b', 'c', 'd'], ['a'], 'a', 3)).toBeNull();
  });

  it('keeps all live at startup (no active workspace) while within budget', () => {
    // Regression guard: before an active workspace is established, a small set of
    // workspaces must stay mounted rather than collapsing to an empty live-set.
    expect(computeWarmWorkspaceIds(['a', 'b'], [], null, 3)).toBeNull();
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

  it('fills the budget from current workspaces before recency is established', () => {
    // active set but no recency yet: never leave warm smaller than the budget
    // while extra workspaces exist.
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c', 'd', 'e'], [], 'a', 2);
    expect(warm).toEqual(new Set(['a', 'b', 'c']));
    expect(warm?.size).toBe(3);
  });

  it('ignores stale active/recent ids no longer among current workspaces', () => {
    const warm = computeWarmWorkspaceIds(['a', 'b', 'c', 'd'], ['x', 'b'], 'y', 1);
    // active(y) and recent(x) are gone; keep present recent(b) then fill (a).
    expect(warm).toEqual(new Set(['b', 'a']));
    expect(warm?.size).toBe(2);
  });

  it('keeps protected workspaces live even when they are cold', () => {
    const warm = computeWarmWorkspaceIds(
      ['a', 'b', 'c', 'd', 'e'],
      ['a', 'b'],
      'a',
      1,
      ['e'],
    );
    expect(warm).toEqual(new Set(['e', 'a']));
  });

  it('lets protected workspaces exceed the warm budget', () => {
    const warm = computeWarmWorkspaceIds(
      ['a', 'b', 'c', 'd', 'e'],
      ['a', 'b'],
      'a',
      1,
      ['d', 'e'],
    );
    expect(warm).toEqual(new Set(['d', 'e', 'a']));
  });

  it('ignores stale protected workspace ids', () => {
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
