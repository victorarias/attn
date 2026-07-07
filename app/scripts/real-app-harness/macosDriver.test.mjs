import { describe, expect, it } from 'vitest';
import { withWindowTitleArgs } from './macosDriver.mjs';

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
