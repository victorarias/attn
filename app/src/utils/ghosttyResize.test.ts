import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createResizeCoalescer,
  resizeGhosttyWithoutReflow,
} from './ghosttyResize';

afterEach(() => {
  vi.useRealTimers();
});

describe('createResizeCoalescer', () => {
  it('applies the leading size immediately and the latest size after 250ms', () => {
    vi.useFakeTimers();
    const apply = vi.fn();
    const coalescer = createResizeCoalescer(apply);

    coalescer.submit({ cols: 100, rows: 30 }, true);
    coalescer.submit({ cols: 110, rows: 31 }, true);
    coalescer.submit({ cols: 120, rows: 32 }, true);

    expect(apply).toHaveBeenCalledTimes(1);
    expect(apply).toHaveBeenLastCalledWith({ cols: 100, rows: 30 });

    vi.advanceTimersByTime(249);
    expect(apply).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(1);
    expect(apply).toHaveBeenCalledTimes(2);
    expect(apply).toHaveBeenLastCalledWith({ cols: 120, rows: 32 });
  });

  it('flushes final non-coalesced geometry immediately and cancels the trailing timer', () => {
    vi.useFakeTimers();
    const apply = vi.fn();
    const coalescer = createResizeCoalescer(apply);

    coalescer.submit({ cols: 100, rows: 30 }, true);
    coalescer.submit({ cols: 110, rows: 31 }, true);
    coalescer.submit({ cols: 125, rows: 34 }, false);

    expect(apply).toHaveBeenCalledTimes(2);
    expect(apply).toHaveBeenLastCalledWith({ cols: 125, rows: 34 });

    vi.advanceTimersByTime(250);
    expect(apply).toHaveBeenCalledTimes(2);
  });

  it('drops pending geometry when cancelled', () => {
    vi.useFakeTimers();
    const apply = vi.fn();
    const coalescer = createResizeCoalescer(apply);

    coalescer.submit({ cols: 100, rows: 30 }, true);
    coalescer.submit({ cols: 110, rows: 31 }, true);
    coalescer.cancel();
    vi.advanceTimersByTime(250);

    expect(apply).toHaveBeenCalledTimes(1);
  });
});

describe('resizeGhosttyWithoutReflow', () => {
  it('temporarily disables DEC wraparound while resizing', () => {
    const calls: string[] = [];
    const terminal = {
      getMode: vi.fn(() => true),
      write: vi.fn((data: string) => calls.push(`write:${JSON.stringify(data)}`)),
      resize: vi.fn((cols: number, rows: number) => calls.push(`resize:${cols}x${rows}`)),
    };

    resizeGhosttyWithoutReflow(terminal, 120, 40);

    expect(calls).toEqual([
      'write:"\\u001b[?7l"',
      'resize:120x40',
      'write:"\\u001b[?7h"',
    ]);
  });

  it('preserves an already-disabled wraparound mode', () => {
    const terminal = {
      getMode: vi.fn(() => false),
      write: vi.fn(),
      resize: vi.fn(),
    };

    resizeGhosttyWithoutReflow(terminal, 90, 30);

    expect(terminal.resize).toHaveBeenCalledWith(90, 30);
    expect(terminal.write).not.toHaveBeenCalled();
  });

  it('restores wraparound if resize throws', () => {
    const terminal = {
      getMode: vi.fn(() => true),
      write: vi.fn(),
      resize: vi.fn(() => {
        throw new Error('resize failed');
      }),
    };

    expect(() => resizeGhosttyWithoutReflow(terminal, 100, 35)).toThrow('resize failed');
    expect(terminal.write).toHaveBeenNthCalledWith(1, '\x1b[?7l');
    expect(terminal.write).toHaveBeenNthCalledWith(2, '\x1b[?7h');
  });
});
