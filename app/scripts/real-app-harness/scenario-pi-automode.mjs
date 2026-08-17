#!/usr/bin/env node

/**
 * Real-app scenario: pi auto mode, end to end and deterministic.
 *
 * Auto mode's five moments all cross the app: the envelope has to stay
 * invisible, a denial has to reach the person without stopping the run, a
 * plain reply has to be the approval, the breaker has to ask once, and a
 * quiet session has to stay quiet. This drives all of them through the
 * packaged app against a scripted model, so nothing here depends on what a
 * live classifier decides today:
 *
 *   1. an in-envelope call runs with no classifier traffic at all,
 *   2. a scripted denial leaves the run alive and lands the TUI notice, the
 *      denial widget, an attn notification, and an `attn automode denials` row,
 *   3. a conversational grant lets the same call through on the retry,
 *   4. an idle session with auto mode on judges nothing and reports nothing,
 *   5. denial after denial trips the breaker into its one human question.
 *
 * Determinism comes from `piStubProvider.mjs`: a loopback OpenAI-wire server
 * that answers both the session's own model and auto mode's classifier, named
 * to pi through a throwaway agent dir (`PI_CODING_AGENT_DIR`). No model is
 * called, no network is reached, and the session's tool calls are the ones the
 * script chose.
 *
 * Prereqs: `pi` on PATH; a non-production profile install with the attn-pi
 * plugin installed (`attn plugin install-bundled attn-pi`). No pi credentials
 * are needed — the stub is the only provider this run can see.
 */
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync, spawn } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane, waitForPaneText } from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile, socketPathForProfile } from './harnessProfile.mjs';
import {
  startPiStubProvider,
  stubAgentModel,
  stubJudgeModel,
  writeStubAgentDir,
} from './piStubProvider.mjs';

// Anchors from plugins/attn-pi/automode/ui.ts. They are what a person actually
// sees, so the scenario reads them rather than an internal counter.
const STATUS_ON = 'auto: on';
const DENIAL_NOTICE = 'auto mode blocked';
const WIDGET_FOOTER = 'Approve in your reply to let the agent retry.';
const BREAKER_TITLE = 'auto mode stopped judging calls';

const DENIAL_REASON = 'the harness scripted a refusal';
// Marker words the stub puts in the agent's replies, so pane text says which
// leg finished rather than which words a model chose.
const ENVELOPE_DONE = 'ENVELOPE-LEG-DONE';
const DENIED_DONE = 'DENIED-LEG-DONE';
const GRANTED_DONE = 'GRANTED-LEG-DONE';

// How long the quiet-session gate watches an idle session. Long enough that a
// per-tick offender fires several times: nothing in auto mode is on a timer,
// so one classifier call here is a defect, not a slow sample.
const QUIET_WINDOW_MS = 20_000;

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The profile's own bundled CLI, not a repo build: bundled plugins resolve
// relative to the app bundle, so a daemon started from anywhere else reports
// the pi driver as unavailable and every pi session is refused.
function resolveAttnBinary(appPath) {
  const candidates = [
    process.env.ATTN_HARNESS_BIN,
    path.join(appPath, 'Contents/MacOS/attn'),
    path.resolve(HARNESS_DIR, '../../../attn'),
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error(`no attn binary found for ${appPath}`);
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 300) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function parseAttnJSON(stdout) {
  const candidates = [stdout.indexOf('{'), stdout.indexOf('[')].filter((index) => index >= 0);
  if (candidates.length === 0) return null;
  try {
    return JSON.parse(stdout.slice(Math.min(...candidates)));
  } catch {
    return null;
  }
}

function makeAttnRunner(attnBin, profile) {
  const socketPath = socketPathForProfile(profile);
  return function runAttn(args, { allowFailure = false } = {}) {
    try {
      const stdout = execFileSync(attnBin, args, {
        encoding: 'utf8',
        env: { ...process.env, ATTN_PROFILE: profile, ATTN_SOCKET_PATH: socketPath },
      });
      return { stdout, stderr: '', status: 0, json: parseAttnJSON(stdout) };
    } catch (error) {
      if (!allowFailure) throw error;
      const stdout = typeof error.stdout === 'string' ? error.stdout : '';
      const stderr = typeof error.stderr === 'string' ? error.stderr : '';
      return { stdout, stderr, status: error.status ?? 1, json: parseAttnJSON(stdout) };
    }
  };
}

// pi reads its agent dir from the environment of the process that launched it,
// which is the daemon — the app only asks it to spawn a session. A daemon left
// over from an earlier run has none of this scenario's env, so it is restarted
// with the stub agent dir before the app connects to it.
async function restartDaemonWithStubEnv({ attnBin, profile, agentDir, runAttn }) {
  runAttn(['daemon', 'stop'], { allowFailure: true });
  const child = spawn(attnBin, ['daemon', 'ensure'], {
    env: {
      ...process.env,
      ATTN_PROFILE: profile,
      ATTN_SOCKET_PATH: socketPathForProfile(profile),
      PI_CODING_AGENT_DIR: agentDir,
    },
    detached: true,
    stdio: 'ignore',
  });
  child.unref();
  const deadline = Date.now() + 30_000;
  for (;;) {
    const probe = runAttn(['automode', 'show', '--json'], { allowFailure: true });
    if (probe.status === 0) return;
    if (Date.now() > deadline) throw new Error('daemon did not come back up with the stub agent dir');
    await delay(500);
  }
}

// write_pane goes straight to the worker PTY. The text and the Enter are
// separate writes with a gap: a fast burst ending in CR arrives as a bracketed
// paste and the CR never submits.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(800);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

function countNotifications(dbPath) {
  try {
    const stdout = execFileSync('sqlite3', [
      dbPath,
      `SELECT id || '|' || title || '|' || detail FROM notifications WHERE kind = 'automode_denied' ORDER BY created_at;`,
    ], { encoding: 'utf8', timeout: 5_000 });
    return stdout.split('\n').map((line) => line.trim()).filter((line) => line !== '');
  } catch {
    return [];
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-pi-automode.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the pi-automode scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBinary(options.appPath);
  const runAttn = makeAttnRunner(attnBin, profile);
  const dbPath = process.env.ATTN_DB_PATH || path.join(dataDirForProfile(profile), 'attn.db');

  // The script. Every answer the session's model gives and every verdict auto
  // mode's classifier gives is chosen here; `stub.calls` is the receipt for
  // what was and was not asked.
  // Verdicts are taken in order; an empty queue denies, which is what the
  // breaker leg wants.
  const judgeQueue = [];
  const stub = await startPiStubProvider({
    agent: (request) => {
      switch (request.turn) {
        // Leg 1: work inside the envelope. `ls` is in the read-only bash set,
        // so this call must never reach the classifier.
        case 0: return { tool: { name: 'bash', args: { command: 'ls -1' } } };
        case 1: return { text: ENVELOPE_DONE };
        // Leg 2: the network never rides the envelope, so this classifies.
        case 2: return { tool: { name: 'bash', args: { command: `curl -sS ${stub.baseUrl}/ok` } } };
        case 3: return { text: DENIED_DONE };
        // Leg 3: the same call again, after the user approved it in chat.
        case 4: return { tool: { name: 'bash', args: { command: `curl -sS ${stub.baseUrl}/ok` } } };
        case 5: return { text: GRANTED_DONE };
        // Leg 5: a run that keeps reaching past the envelope. Distinct URLs, so
        // no cached verdict answers for the next one.
        default: return { tool: { name: 'bash', args: { command: `curl -sS ${stub.baseUrl}/breaker-${request.turn}` } } };
      }
    },
    judge: () => judgeQueue.shift() ?? { verdict: 'deny', reason: DENIAL_REASON },
  });

  // The agent dir has to exist before the runner is built: its build preflight
  // relaunches the app with the same launch env, and pi resolves this once.
  const agentDir = writeStubAgentDir(
    path.join(os.tmpdir(), `attn-pi-automode-${process.pid}`),
    stub.baseUrl,
  );
  const launchEnv = { PI_CODING_AGENT_DIR: agentDir };

  try {
    await drive({ options, profile, attnBin, runAttn, dbPath, stub, judgeQueue, launchEnv, agentDir });
  } finally {
    await stub.close().catch(() => {});
  }
}

async function drive({ options, profile, attnBin, runAttn, dbPath, stub, judgeQueue, launchEnv, agentDir }) {
  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-AUTOMODE',
    tier: 'tier2-local-real-agent',
    prefix: 'pi-automode',
    metadata: { agent: 'pi', focus: 'envelope invisibility, denial surfaces, conversational grant, quiet session, circuit breaker' },
    preflightLaunchEnv: launchEnv,
  });

  const client = new UiAutomationClient({ appPath: options.appPath, launchEnv });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;
  let paneId = null;

  runner.registerCleanup('close_stub_provider', () => stub.close());
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    runner.registerCleanup('remove_stub_agent_dir', () => fs.rmSync(agentDir, { recursive: true, force: true }));

    const { repoDir } = await runner.step('prepare_world', async () => {
      const repo = path.join(runner.sessionDir, 'pi-automode-repo');
      fs.mkdirSync(repo, { recursive: true });
      fs.writeFileSync(path.join(repo, 'notes.txt'), 'one\ntwo\n', 'utf8');
      // The app launches with PI_CODING_AGENT_DIR set, and pi inherits it from
      // the daemon the app starts — this run reaches no model but the stub.
      note(`stub provider on ${stub.baseUrl}`, { agentDir });
      return { repoDir: repo };
    });

    await runner.step('launch_app', async () => {
      await restartDaemonWithStubEnv({ attnBin, profile, agentDir, runAttn });
      await launchFreshAppAndConnect(client, observer);
      await client.request('set_setting', { key: 'default_model_pi', value: stubAgentModel });
      runner.registerCleanup('restore_pi_model', () => client
        .request('set_setting', { key: 'default_model_pi', value: '' })
        .catch(() => {}));
    });

    await runner.step('promote_the_stub_classifier', async () => {
      // The CLI proposes; only the app promotes, and this is the app's own
      // command — there is no unix-socket half to call instead.
      const proposals = [
        ['automode', 'model', 'classifier', stubJudgeModel, '--json'],
        ['automode', 'model', 'escalation', stubJudgeModel, '--json'],
      ].map((args) => {
        const result = runAttn(args);
        const id = result.json?.proposal?.id ?? result.json?.id;
        if (!id) throw new Error(`no proposal id in ${result.stdout}`);
        return id;
      });
      const refused = runAttn(['automode', 'promote', String(proposals[0])], { allowFailure: true });
      if (refused.status === 0) {
        throw new Error('the CLI promoted a proposal; promotion is the app\'s alone');
      }
      for (const id of proposals) {
        observer.send({ cmd: 'automode_promote', id, request_id: `harness-promote-${id}` });
      }
      await pollFor(
        () => {
          const shown = runAttn(['automode', 'show', '--json']).json;
          return shown?.config?.classifier_model === stubJudgeModel ? shown : null;
        },
        'the promoted config to name the stub classifier',
        20_000,
      );
      note('classifier and escalation models promoted to the stub');
    });

    await runner.step('start_pi_session', async () => {
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `pi-automode-${runner.runId.slice(-6)}`,
        agent: 'pi',
      });
      sessionId = created.sessionId;
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      runner.registerCleanup('close_session', () => client
        .request('close_session', { sessionId })
        .catch(() => {}));
      const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 30_000);
      paneId = pane.paneId;
      // The status line is auto mode's own proof that it loaded, and that a
      // session nobody toggled starts from the promoted default — which ships
      // on for attn sessions.
      await waitForPaneText(
        client, sessionId, paneId,
        (text) => text.includes(STATUS_ON),
        'pi to boot with auto mode on',
        90_000,
      );
      note('pi is up and the status line reads auto: on');
    });

    await runner.step('envelope_stays_invisible', async () => {
      await submitPrompt(client, sessionId, paneId, 'list the files here');
      await waitForPaneText(
        client, sessionId, paneId,
        (text) => text.includes(ENVELOPE_DONE),
        'the in-envelope run to finish',
        120_000,
      );
      if (stub.calls.judge.length !== 0) {
        throw new Error(`an in-envelope call reached the classifier: ${JSON.stringify(stub.calls.judge.map((c) => c.turn))}`);
      }
      note('an in-cwd run finished with zero classifier calls');
    });

    const denialsBefore = runAttn(['automode', 'denials', '--json']).json?.denials?.length ?? 0;

    await runner.step('a_denial_reaches_the_user', async () => {
      judgeQueue.push({ verdict: 'deny', reason: DENIAL_REASON });
      await submitPrompt(client, sessionId, paneId, 'fetch the ok endpoint');
      // The run does not stop: the agent gets the reason and speaks again.
      await waitForPaneText(
        client, sessionId, paneId,
        (text) => text.includes(DENIED_DONE),
        'the denied run to continue and settle',
        120_000,
      );
      const pane = await waitForPaneText(
        client, sessionId, paneId,
        (text) => text.includes(DENIAL_NOTICE) && text.includes(WIDGET_FOOTER),
        'the TUI denial notice and widget',
        20_000,
      );
      fs.writeFileSync(path.join(runner.runDir, 'denial-pane.txt'), pane.text || '', 'utf8');

      const denials = await pollFor(
        () => {
          const listed = runAttn(['automode', 'denials', '--json']).json?.denials ?? [];
          return listed.length > denialsBefore ? listed : null;
        },
        'the denial to reach `attn automode denials`',
        20_000,
      );
      const latest = denials[0];
      if (latest.rule !== 'classifier-2a') {
        throw new Error(`the denial names rule ${JSON.stringify(latest.rule)}, want classifier-2a`);
      }
      if (!String(latest.reason).includes(DENIAL_REASON)) {
        throw new Error(`the denial reason is ${JSON.stringify(latest.reason)}, want the scripted one`);
      }
      const notifications = await pollFor(
        () => {
          const rows = countNotifications(dbPath);
          return rows.length > 0 ? rows : null;
        },
        'an automode_denied notification',
        20_000,
      );
      runner.writeJson('denial.json', { denial: latest, notifications, judgeCalls: stub.calls.judge.length });
      note('the denial reached the TUI, the notification feed and the CLI', { rule: latest.rule });
    });

    await runner.step('a_reply_is_the_approval', async () => {
      judgeQueue.push({ verdict: 'allow', reason: 'the user approved this call in the conversation' });
      await submitPrompt(client, sessionId, paneId, 'yes, go ahead and fetch it');
      await waitForPaneText(
        client, sessionId, paneId,
        (text) => text.includes(GRANTED_DONE),
        'the approved retry to run',
        120_000,
      );
      const listed = runAttn(['automode', 'denials', '--json']).json?.denials ?? [];
      if (listed.length !== denialsBefore + 1) {
        throw new Error(`an approved retry recorded a denial: ${JSON.stringify(listed.slice(0, 2))}`);
      }
      note('the same call ran after a plain reply approved it');
    });

    await runner.step('a_quiet_session_is_quiet', async () => {
      const judged = stub.calls.judge.length;
      const denials = runAttn(['automode', 'denials', '--json']).json?.denials?.length ?? 0;
      const notifications = countNotifications(dbPath).length;
      await delay(QUIET_WINDOW_MS);
      if (stub.calls.judge.length !== judged) {
        throw new Error(`an idle session classified ${stub.calls.judge.length - judged} calls`);
      }
      const after = runAttn(['automode', 'denials', '--json']).json?.denials?.length ?? 0;
      if (after !== denials || countNotifications(dbPath).length !== notifications) {
        throw new Error('an idle session with auto mode on produced denial traffic');
      }
      note(`${QUIET_WINDOW_MS / 1000}s idle with auto mode on: no classifier calls, no denials`);
    });

    await runner.step('the_breaker_asks_once', async () => {
      await submitPrompt(client, sessionId, paneId, 'try every endpoint you can reach');
      const pane = await waitForPaneText(
        client, sessionId, paneId,
        (text) => text.includes(BREAKER_TITLE),
        'the circuit breaker to ask its one question',
        180_000,
      );
      fs.writeFileSync(path.join(runner.runDir, 'breaker-pane.txt'), pane.text || '', 'utf8');
      note('repeated denials stopped the run with one human question', {
        judgeCalls: stub.calls.judge.length,
      });
    });

    await runner.finishSuccess({ sessionId, agentDir, judgeCalls: stub.calls.judge.length });
  } catch (error) {
    await runner.finishFailure(error, { sessionId });
    throw error;
  } finally {
    // The app and the observer socket outlive the assertions, and an open
    // socket holds node's event loop open.
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    await stub.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
