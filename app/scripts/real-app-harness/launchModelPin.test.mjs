import { afterEach, describe, expect, it } from 'vitest';
import { launchFreshAppAndConnect, restoreHarnessSettings } from './common.mjs';

// Every scenario boots through launchFreshAppAndConnect, so what it pins is
// what the whole catalog spends. These cover the pin and the promise that goes
// with it: the profile is left as it was found.

function fakeClient() {
  const calls = [];
  return {
    calls,
    settingWrites: () => calls.filter((call) => call.verb === 'set_setting').map((call) => call.payload),
    launchFreshApp: async () => {},
    waitForManifest: async () => {},
    waitForReady: async () => {},
    waitForFrontendResponsive: async () => {},
    request: async (verb, payload) => {
      calls.push({ verb, payload });
      return {};
    },
  };
}

function fakeObserver(settings = {}) {
  return {
    connect: async () => {},
    getSetting: (key) => settings[key] ?? '',
  };
}

// Stands in for the daemon connection the real restore opens.
function recordingWriter() {
  const written = [];
  const write = async (entries) => { written.push(...entries); };
  return { written, write };
}

const ENV_KEYS = ['ATTN_HARNESS_LAUNCH_MODEL_CLAUDE', 'ATTN_HARNESS_LAUNCH_MODEL_CODEX'];

afterEach(async () => {
  for (const key of ENV_KEYS) delete process.env[key];
  await restoreHarnessSettings({ write: async () => {} });
});

describe('launch model pinning', () => {
  it('pins every agent to its cheap model without the scenario asking', async () => {
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_model_claude', value: 'haiku' },
      { key: 'default_model_codex', value: 'gpt-5.4-mini' },
    ]);
  });

  it('puts back what it found, not a blank', async () => {
    const client = fakeClient();
    const observer = fakeObserver({ default_model_claude: 'opus', default_model_codex: '' });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    const writer = recordingWriter();
    expect(await restoreHarnessSettings({ write: writer.write })).toBe(2);
    expect(writer.written).toEqual([
      { key: 'default_model_claude', value: 'opus' },
      { key: 'default_model_codex', value: '' },
    ]);
  });

  it('restores the value the run started with, not its own pin, after a relaunch', async () => {
    const client = fakeClient();
    const observer = fakeObserver({ default_model_claude: 'sonnet', default_model_codex: 'gpt-5.5' });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
    // The relaunch sees the harness's own pin in place; recording that would
    // leave haiku behind for good.
    const afterPin = fakeObserver({ default_model_claude: 'haiku', default_model_codex: 'gpt-5.4-mini' });
    await launchFreshAppAndConnect(client, afterPin, { sweepStaleSessions: false });

    const writer = recordingWriter();
    await restoreHarnessSettings({ write: writer.write });
    expect(writer.written).toEqual([
      { key: 'default_model_claude', value: 'sonnet' },
      { key: 'default_model_codex', value: 'gpt-5.5' },
    ]);
  });

  it('lets one agent be overridden without unpinning the other', async () => {
    process.env.ATTN_HARNESS_LAUNCH_MODEL_CLAUDE = 'sonnet';
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_model_claude', value: 'sonnet' },
      { key: 'default_model_codex', value: 'gpt-5.4-mini' },
    ]);
  });

  it('leaves a setting alone only when inheriting is asked for by name', async () => {
    process.env.ATTN_HARNESS_LAUNCH_MODEL_CLAUDE = 'inherit';
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_model_codex', value: 'gpt-5.4-mini' },
    ]);
  });

  it('writes nothing when the pin is already what it wants', async () => {
    const client = fakeClient();
    const observer = fakeObserver({ default_model_claude: 'haiku', default_model_codex: 'gpt-5.4-mini' });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([]);
    expect(await restoreHarnessSettings({ write: async () => {} })).toBe(0);
  });
});
