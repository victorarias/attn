import { describe, expect, it } from 'vitest';
import { describeInputDriverFailure, withWindowTitleArgs } from './macosDriver.mjs';

describe('describeInputDriverFailure', () => {
  it('names a dark display as the cause, and says it is not the product', () => {
    const hint = describeInputDriverFailure(
      '[RealAppHarness] Input cannot be delivered: the screen is locked or the display is off. Wake the display (and unlock the screen) before running packaged-app scenarios.',
    );
    expect(hint).toContain('display was off');
    expect(hint).toContain('not a product failure');
  });

  it('still names the accessibility grant when that is what failed', () => {
    expect(
      describeInputDriverFailure('[RealAppHarness] Accessibility permission is required for the real app harness input driver.'),
    ).toContain('Grant Accessibility access');
  });

  it('falls back to the generic hint for anything else', () => {
    expect(describeInputDriverFailure('[RealAppHarness] App is not running for bundle id x')).toBe('macOS automation failed.');
    expect(describeInputDriverFailure()).toBe('macOS automation failed.');
  });
});

describe('withWindowTitleArgs', () => {
  it('returns the input args unchanged when no windowTitle is given', () => {
    expect(withWindowTitleArgs(['click', '--relative-x', '0.5'])).toEqual([
      'click',
      '--relative-x',
      '0.5',
    ]);
    expect(withWindowTitleArgs(['click', '--relative-x', '0.5'], {})).toEqual([
      'click',
      '--relative-x',
      '0.5',
    ]);
  });

  it('appends --window-title when opts.windowTitle is set', () => {
    expect(withWindowTitleArgs(['windowid'], { windowTitle: 'present' })).toEqual([
      'windowid',
      '--window-title',
      'present',
    ]);
  });

  it('does not mutate the input args array', () => {
    const args = ['windowid'];
    withWindowTitleArgs(args, { windowTitle: 'present' });
    expect(args).toEqual(['windowid']);
  });

  it('ignores an empty-string windowTitle', () => {
    expect(withWindowTitleArgs(['windowid'], { windowTitle: '' })).toEqual(['windowid']);
  });
});
