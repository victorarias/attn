import { describe, expect, it } from 'vitest';
import { stripDaemonOwnedResponses } from './terminalQueryResponses';

const ESC = String.fromCharCode(0x1b);
const BEL = String.fromCharCode(0x07);

describe('stripDaemonOwnedResponses', () => {
  // The daemon owns CPR, DA1, and OSC 10/11/12 color replies; the frontend
  // must not forward its own, or the shell reads the duplicate reply as
  // stray input.

  it('drops a standalone cursor position report', () => {
    expect(stripDaemonOwnedResponses(ESC + '[24;1R')).toBe('');
  });

  it('drops a standalone DA1 device-attributes report', () => {
    expect(stripDaemonOwnedResponses(ESC + '[?1;2c')).toBe('');
  });

  it('drops an ST-terminated OSC11 background color response', () => {
    const osc11 = ESC + ']11;rgb:0000/0000/0000' + ESC + '\\';
    expect(stripDaemonOwnedResponses(osc11)).toBe('');
  });

  it('drops a BEL-terminated OSC10 foreground color response', () => {
    const osc10 = ESC + ']10;rgb:ffff/ffff/ffff' + BEL;
    expect(stripDaemonOwnedResponses(osc10)).toBe('');
  });

  it('drops an OSC12 cursor color response', () => {
    const osc12 = ESC + ']12;rgb:3b3b/3b3b/3b3b' + ESC + '\\';
    expect(stripDaemonOwnedResponses(osc12)).toBe('');
  });

  it('keeps a DSR response untouched', () => {
    const dsr = ESC + '[0n';
    expect(stripDaemonOwnedResponses(dsr)).toBe(dsr);
  });

  it('keeps an OSC52 clipboard write untouched', () => {
    const osc52 = ESC + ']52;c;aGVsbG8=' + ESC + '\\';
    expect(stripDaemonOwnedResponses(osc52)).toBe(osc52);
  });

  it('strips CPR, DA1, and OSC11 embedded in a mixed response, leaving plain text', () => {
    const mixed = 'hello' + ESC + '[5;7R' + ESC + '[?1;2c' + ESC + ']11;rgb:0000/0000/0000' + ESC + '\\' + 'world';
    expect(stripDaemonOwnedResponses(mixed)).toBe('helloworld');
  });
});
