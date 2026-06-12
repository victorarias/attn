import { describe, expect, it } from 'vitest';
import { buildTerminalQueryResponses, stripDaemonOwnedResponses } from './terminalQueryResponses';

const ESC = String.fromCharCode(0x1b);

describe('stripDaemonOwnedResponses', () => {
  // The daemon owns CPR and DA1 replies; the frontend must not forward its own,
  // or the shell reads the duplicate ESC[r;cR / ESC[?...c as stray input.

  it('drops a standalone cursor position report', () => {
    expect(stripDaemonOwnedResponses(ESC + '[24;1R')).toBe('');
  });

  it('drops a standalone DA1 device-attributes report', () => {
    expect(stripDaemonOwnedResponses(ESC + '[?1;2c')).toBe('');
  });

  it('keeps non-CPR / non-DA1 responses untouched', () => {
    const osc11 = ESC + ']11;rgb:0000/0000/0000' + ESC + '\\';
    expect(stripDaemonOwnedResponses(osc11)).toBe(osc11);
  });

  it('removes CPR and DA1 embedded alongside other response bytes', () => {
    expect(stripDaemonOwnedResponses(ESC + '[5;7R' + ESC + '[?1;2c' + ESC + ']11;?')).toBe(
      ESC + ']11;?',
    );
  });
});

describe('buildTerminalQueryResponses', () => {
  it('answers OSC color queries but leaves DA1 to the daemon', () => {
    const write = ESC + ']11;?' + ESC + '\\' + ESC + '[0c';
    expect(buildTerminalQueryResponses(write, 'dark')).toEqual([
      ESC + ']11;rgb:1e1e/1e1e/1e1e' + ESC + '\\',
    ]);
  });

  it('uses the active terminal theme for OSC color query responses', () => {
    const write = ESC + ']10;?' + String.fromCharCode(0x07) + ESC + ']12;?' + String.fromCharCode(0x07);
    expect(buildTerminalQueryResponses(write, 'light')).toEqual([
      ESC + ']10;rgb:3b3b/3b3b/3b3b' + ESC + '\\',
      ESC + ']12;rgb:3b3b/3b3b/3b3b' + ESC + '\\',
    ]);
  });

  it('does not duplicate model-provided OSC responses', () => {
    const write = ESC + ']11;?' + ESC + '\\' + ESC + '[0c';
    expect(buildTerminalQueryResponses(
      write,
      'dark',
      [ESC + ']11;rgb:0000/0000/0000' + ESC + '\\', ESC + '[?62;4;6;22c'],
    )).toEqual([]);
  });

  it('detects terminal queries in byte writes', () => {
    const bytes = new TextEncoder().encode(ESC + ']11;?' + String.fromCharCode(0x07));
    expect(buildTerminalQueryResponses(bytes, 'light')).toEqual([
      ESC + ']11;rgb:ffff/ffff/ffff' + ESC + '\\',
    ]);
  });
});
