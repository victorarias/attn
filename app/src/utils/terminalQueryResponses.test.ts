import { describe, expect, it } from 'vitest';
import { buildTerminalQueryResponses } from './terminalQueryResponses';

describe('buildTerminalQueryResponses', () => {
  it('answers shell prompt terminal queries not handled by ghostty-web', () => {
    expect(buildTerminalQueryResponses('\u001b]11;?\u001b\\\u001b[0c', 'dark')).toEqual([
      '\u001b]11;rgb:1e1e/1e1e/1e1e\u001b\\',
      '\u001b[?1;2c',
    ]);
  });

  it('uses the active terminal theme for OSC color query responses', () => {
    expect(buildTerminalQueryResponses('\u001b]10;?\u0007\u001b]12;?\u0007', 'light')).toEqual([
      '\u001b]10;rgb:3b3b/3b3b/3b3b\u001b\\',
      '\u001b]12;rgb:3b3b/3b3b/3b3b\u001b\\',
    ]);
  });

  it('does not duplicate model-provided responses', () => {
    expect(buildTerminalQueryResponses(
      '\u001b]11;?\u001b\\\u001b[0c',
      'dark',
      ['\u001b]11;rgb:0000/0000/0000\u001b\\', '\u001b[?62;4;6;22c'],
    )).toEqual([]);
  });

  it('detects terminal queries in byte writes', () => {
    const bytes = new TextEncoder().encode('\u001b]11;?\u0007');
    expect(buildTerminalQueryResponses(bytes, 'light')).toEqual([
      '\u001b]11;rgb:ffff/ffff/ffff\u001b\\',
    ]);
  });
});
