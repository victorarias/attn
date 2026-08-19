#!/usr/bin/env node

/**
 * Watch a recorded nisse reply stream into the real conversation pane.
 *
 * This is the "look at it yourself" half of the streaming-markdown spike: it
 * launches the profile's app, opens a conversation session, and replays a
 * recorded envelope stream at the pacing it was recorded at. Nothing is
 * scripted below the socket — the store, the pane, the markdown pipeline and
 * the scroll container are the shipped ones. No model is called.
 *
 *   ATTN_PROFILE=<name> node app/scripts/real-app-harness/nisse-markdown-demo.mjs
 *   ... --theme light          # relaunch in the light theme first
 *   ... --recording md-long    # the 27,540-char reply instead of the tour
 *   ... --keep-open            # leave the app up when the replay ends (default)
 *
 * The app is left running on purpose: scroll back through the transcript, or
 * scroll away mid-replay and watch the view hold still.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const RECORDINGS = path.join(HERE, '../../src/components/ConversationPane/__recordings__');

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function recordedEnvelopes(name) {
  const rows = fs.readFileSync(path.join(RECORDINGS, `${name}.jsonl`), 'utf8')
    .trim().split('\n').map((line) => JSON.parse(line));
  let previous = rows[0].at;
  return rows.map((row) => {
    const afterMs = Math.max(0, Math.round(row.at - previous));
    previous = row.at;
    return { seq: row.envelope.seq, kind: row.envelope.kind, body: row.envelope.body, afterMs };
  });
}

async function main() {
  const argv = process.argv.slice(2);
  const flag = (name, fallback) => {
    const at = argv.indexOf(`--${name}`);
    return at >= 0 && argv[at + 1] ? argv[at + 1] : fallback;
  };
  const own = new Set(['--theme', '--recording']);
  const passthrough = argv.filter((entry, at) => !own.has(entry) && !own.has(argv[at - 1]));
  const options = parseCommonArgs(passthrough);
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a non-production profile');

  const recording = flag('recording', 'md-tour');
  const theme = flag('theme', null);
  const envelopes = recordedEnvelopes(recording);

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  await launchFreshAppAndConnect(client, observer);

  // The pane reads the theme setting once, at startup, so a switch means a
  // second launch.
  if (theme) {
    await client.request('set_setting', { key: 'theme', value: theme });
    await client.quitApp();
    await delay(1500);
    await launchFreshAppAndConnect(client, observer);
  }

  const cwd = fs.mkdtempSync(path.join(process.env.TMPDIR ?? '/tmp', 'nisse-md-demo-'));
  const created = await client.request('create_session', { cwd, label: 'nisse-markdown-demo', agent: 'nisse' });
  const sessionId = created.sessionId;
  await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const state = await client.request('conversation_get_state', { sessionId }).catch(() => null);
    if (state && state.inputDisabled === false) break;
    await delay(500);
  }

  console.log(`replaying ${recording} (${envelopes.length} envelopes) into ${sessionId}`);
  await client.request('conversation_replay_envelopes', { sessionId, envelopes });
  const runtimeMs = envelopes.reduce((total, entry) => total + entry.afterMs, 0);
  await delay(runtimeMs + 3000);
  console.log('replay done; the app is still up — scroll around, then quit it yourself');
  observer.close();
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
