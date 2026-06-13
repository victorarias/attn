import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { parseCommonArgs } from './common.mjs';

const TEST_DIR = path.dirname(fileURLToPath(import.meta.url));

// The named-profile path resolves via the real attn binary; skip (don't fail)
// when ./attn is absent so the unit suite never depends on a built binary.
function attnBinary() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(TEST_DIR, '../../../attn')]
    .filter(Boolean);
  return candidates.find((candidate) => fs.existsSync(candidate)) ?? null;
}
const ATTN_BIN = attnBinary();
const describeWithBinary = ATTN_BIN ? describe : describe.skip;

const originalHarnessProfile = process.env.ATTN_HARNESS_PROFILE;
const originalProfile = process.env.ATTN_PROFILE;
const originalAppPath = process.env.ATTN_REAL_APP_PATH;
const originalWsUrl = process.env.ATTN_REAL_APP_WS_URL;

// Clean slate so the safe dev default is exercised regardless of the shell's
// ATTN_PROFILE (which the harness now follows when no override is set).
beforeEach(() => {
  delete process.env.ATTN_HARNESS_PROFILE;
  delete process.env.ATTN_PROFILE;
  delete process.env.ATTN_REAL_APP_PATH;
  delete process.env.ATTN_REAL_APP_WS_URL;
});

afterEach(() => {
  for (const [name, value] of [
    ['ATTN_HARNESS_PROFILE', originalHarnessProfile],
    ['ATTN_PROFILE', originalProfile],
    ['ATTN_REAL_APP_PATH', originalAppPath],
    ['ATTN_REAL_APP_WS_URL', originalWsUrl],
  ]) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
});

describe('parseCommonArgs production safety', () => {
  it('defaults every real-app command to the isolated dev target', () => {
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

  it('derives the production daemon from an acknowledged production app path', () => {
    delete process.env.ATTN_HARNESS_PROFILE;
    delete process.env.ATTN_REAL_APP_WS_URL;

    const options = parseCommonArgs([
      '--app-path',
      path.join(os.homedir(), 'Applications', 'attn.app'),
      '--run-against-prod',
    ]);

    expect(options.wsUrl).toBe('ws://127.0.0.1:9849/ws');
  });

  it('refuses an explicit production websocket while using the dev app', () => {
    expect(() => parseCommonArgs(['--ws-url', 'ws://127.0.0.1:9849/ws'])).toThrow(
      'Refusing to run the real-app harness against production',
    );
  });
});

describeWithBinary('parseCommonArgs one-knob (ATTN_PROFILE)', () => {
  it('targets the named profile from ATTN_PROFILE with no extra flags', () => {
    process.env.ATTN_PROFILE = 'agent7';

    const resolved = JSON.parse(
      execFileSync(ATTN_BIN, ['profile', 'resolve', '--profile', 'agent7', '--json'], {
        encoding: 'utf8',
      }),
    );

    const options = parseCommonArgs([]);

    expect(options.appPath).toBe(path.join(os.homedir(), 'Applications', 'attn-agent7.app'));
    expect(options.wsUrl).toBe(`ws://127.0.0.1:${resolved.wsPort}/ws`);
    // A named profile is an isolated world — never refused as prod.
    expect(options.runAgainstProd).toBe(false);
  });

  it('lets ATTN_HARNESS_PROFILE override ATTN_PROFILE', () => {
    process.env.ATTN_PROFILE = 'agent7';
    process.env.ATTN_HARNESS_PROFILE = 'agent9';

    const options = parseCommonArgs([]);

    expect(options.appPath).toBe(path.join(os.homedir(), 'Applications', 'attn-agent9.app'));
  });
});
