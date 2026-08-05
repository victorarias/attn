import { describe, expect, it, vi } from 'vitest';
import { CooperativeReplayBudget } from './cooperativeReplay';

describe('CooperativeReplayBudget', () => {
  it('keeps cheap restore writes in one browser task', async () => {
    let now = 10;
    const yieldTask = vi.fn(async () => {});
    const budget = new CooperativeReplayBudget(6, () => now, yieldTask);

    await expect(budget.beforeOperation()).resolves.toBe(false);
    now += 2;
    await expect(budget.beforeOperation()).resolves.toBe(false);
    now += 3;
    await expect(budget.beforeOperation()).resolves.toBe(false);

    expect(yieldTask).not.toHaveBeenCalled();
  });

  it('yields once after accumulated model work consumes the slice', async () => {
    let now = 20;
    const yieldTask = vi.fn(async () => {
      now += 1;
    });
    const budget = new CooperativeReplayBudget(6, () => now, yieldTask);

    await budget.beforeOperation();
    now += 7;
    await expect(budget.beforeOperation()).resolves.toBe(true);
    now += 5;
    await expect(budget.beforeOperation()).resolves.toBe(false);

    expect(yieldTask).toHaveBeenCalledTimes(1);
  });

  it('starts a fresh slice after replay drains', async () => {
    let now = 30;
    const yieldTask = vi.fn(async () => {});
    const budget = new CooperativeReplayBudget(6, () => now, yieldTask);

    await budget.beforeOperation();
    now += 20;
    budget.reset();
    await expect(budget.beforeOperation()).resolves.toBe(false);

    expect(yieldTask).not.toHaveBeenCalled();
  });
});
