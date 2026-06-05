import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { parseCommonArgs } from './common.mjs';

const originalProfile = process.env.ATTN_HARNESS_PROFILE;
const originalAppPath = process.env.ATTN_REAL_APP_PATH;
const originalWsUrl = process.env.ATTN_REAL_APP_WS_URL;

afterEach(() => {
  for (const [name, value] of [
    ['ATTN_HARNESS_PROFILE', originalProfile],
    ['ATTN_REAL_APP_PATH', originalAppPath],
    ['ATTN_REAL_APP_WS_URL', originalWsUrl],
  ]) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
});

describe('parseCommonArgs production safety', () => {
  it('defaults every real-app command to the isolated dev target', () => {
    delete process.env.ATTN_HARNESS_PROFILE;
    delete process.env.ATTN_REAL_APP_PATH;
    delete process.env.ATTN_REAL_APP_WS_URL;

    const options = parseCommonArgs([]);

    expect(options.appPath).toBe(path.join(os.homedir(), 'Applications', 'attn-dev.app'));
    expect(options.wsUrl).toBe('ws://127.0.0.1:29849/ws');
  });

  it('refuses the production profile without the explicit acknowledgement', () => {
    process.env.ATTN_HARNESS_PROFILE = '';

    expect(() => parseCommonArgs([])).toThrow(
      'Refusing to run the real-app harness against production',
    );
  });

  it('allows the production profile only with the explicit acknowledgement', () => {
    process.env.ATTN_HARNESS_PROFILE = '';

    expect(() => parseCommonArgs(['--run-against-prod'])).not.toThrow();
  });

  it('refuses an explicit production websocket while using the dev app', () => {
    delete process.env.ATTN_HARNESS_PROFILE;

    expect(() => parseCommonArgs(['--ws-url', 'ws://127.0.0.1:9849/ws'])).toThrow(
      'Refusing to run the real-app harness against production',
    );
  });
});
