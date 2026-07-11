#!/usr/bin/env node

/**
 * Real-app scenario: the ticket-nudge "deliver now" trigger button.
 *
 * The nudge feature gates every ticket doorbell behind a per-session countdown and
 * pauses it while the user is looking at that session, surfacing a "deliver now"
 * button. This scenario proves the *button* path end-to-end in the packaged app:
 *
 *   1. boot a real codex agent A (the nudge target) and select it,
 *   2. make A a ticket participant and produce unread activity from a second
 *      session B (a cheap shell) — A is selected, so the daemon arms the nudge
 *      PAUSED (no timer): nudge_fires_at is absent and the paused button renders,
 *   3. assert the doorbell has NOT been injected yet (the gate held), then
 *   4. click the real "deliver now" button and assert the verbatim doorbell prompt
 *      lands in A's pane — exercising the button onClick -> sendTriggerNudge -> WS
 *      -> handleTriggerNudge chain that no unit/tsc check covers.
 *
 * Because A is selected the nudge is paused with no armed timer, so the single
 * click is structurally the only delivery path — that is the exactly-once
 * guarantee (we do not count noisy rendered pane text).
 *
 * Prereqs: `codex` on PATH; a built `./attn` (or ATTN_HARNESS_BIN); a non-prod
 * profile install with the automation layer (defaults to the dev sibling).
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { ensureCodexPromptReadyViaPty } from './scenarioAgents.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

// The exact prompt the daemon doorbells into an idle agent's PTY
// (internal/daemon/ticket_notify.go: ticketNudgePrompt). The scenario asserts the
// FULL string lands after the click, and that a stable substring is ABSENT before.
const DOORBELL_PROMPT = '📋 New ticket activity — run `attn ticket inbox` to catch up.';
const DOORBELL_SUBSTRING = 'New ticket activity';
// The verbatim injected line minus the leading emoji (which the terminal grid may
// render across cells). This contiguous chunk — em-dash + backticked command +
// "to catch up." — is the real prompt, and an agent paraphrasing "new ticket
// activity" in its own reply will not reproduce it, so its presence is a precise
// "the doorbell was injected" signal.
const DOORBELL_CORE = 'New ticket activity — run `attn ticket inbox` to catch up.';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile },
    });
    const brace = stdout.indexOf('{');
    return { stdout, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
  };
}

const IDLE_STATES = new Set(['idle', 'waiting_input']);

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Collapse runs of whitespace so a match survives the terminal wrapping the
// injected line across rows (e.g. "catch\n  up." -> "catch up.").
const normalizeWs = (text) => text.replace(/\s+/g, ' ');

async function readPaneText(client, sessionId) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const res = await client
    .request('read_pane_text', { sessionId, paneId: pane.paneId }, { timeoutMs: 20_000 })
    .catch(() => null);
  return { paneId: pane.paneId, text: res?.text || '' };
}

// A freshly-booted agent sits at its prompt but is not yet `idle`/`waiting_input`
// — that state is only reached after a completed turn (boot -> working -> idle).
// This scenario uses an idle target to isolate the paused manual-trigger path; the
// shared nudge policy also permits active, launching, and unknown targets. The text
// and the Enter are sent as SEPARATE writes (a fast burst ending in CR is
// treated as a bracketed paste and never submits).
async function driveAgentToIdle(client, observer, sessionId, note) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const prompt = 'Reply with the single word: ok';
  await client.request('write_pane', { sessionId, paneId: pane.paneId, text: prompt, submit: false });
  await delay(1_200);
  await client.request('write_pane', { sessionId, paneId: pane.paneId, text: '\r', submit: false });
  const stateOf = () => observer.getSession(sessionId)?.state || 'unknown';
  await pollFor(() => (stateOf() === 'working' ? true : null), `${sessionId} to start working after the prompt`, 30_000, 500);
  note(`target started working (prompt accepted)`);
  const idle = await pollFor(
    () => (IDLE_STATES.has(stateOf()) ? stateOf() : null),
    `${sessionId} to finish the turn and go idle/waiting`,
    90_000,
    500,
  );
  return idle;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-nudge-trigger.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the nudge-trigger scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, 'nudge-trigger');

  // A real git repo for the target agent's cwd.
  const repoDir = path.join(sessionDir, 'target-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let targetId = null; // A — the codex agent that receives the nudge
  let authorId = null; // B — the shell that produces unread activity
  const evidence = { runId, profile, steps: [] };
  const note = (m, extra) => { console.log(`[nudge-trigger] ${m}`); evidence.steps.push({ t: Date.now(), m, ...(extra || {}) }); };
  const saveEvidence = (verdict) => {
    evidence.verdict = verdict;
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  };

  console.log(`[nudge-trigger] profile=${profile} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    // 1) Boot the target agent A (codex — the reported-bug path is the codex idle
    //    doorbell) and select it so the nudge will arm PAUSED, not counting.
    targetId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `nudge-target-${runId.slice(-6)}`,
      agent: 'codex',
      sessionWaitMs: 30_000,
      promptReadyFn: ensureCodexPromptReadyViaPty,
      promptReadyTimeoutMs: 90_000,
    });
    await client.request('select_session', { sessionId: targetId });
    const idleState = await driveAgentToIdle(client, observer, targetId, note);
    note(`target agent idle and selected`, { targetId, state: idleState });

    // 2) Make A a ticket participant: A creates an unbound backlog ticket (the
    //    creator is a participant), then a second session B produces activity A is
    //    notified about. B only needs to EXIST to author a comment, so it is a
    //    cheap shell, not a booted agent.
    const ticketTitle = `Nudge trigger fixture ${runId.slice(-6)}`;
    const created = runAttn(['ticket', 'new', '--title', ticketTitle, '--session', targetId, '--json']);
    const ticketId = created.json?.ticket_id;
    assert(typeof ticketId === 'string' && ticketId.length > 0, `ticket new returned an id (got ${JSON.stringify(created.json)})`);
    note(`ticket created by target (creator-participant)`, { ticketId });

    authorId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `nudge-author-${runId.slice(-6)}`,
      agent: 'shell',
      sessionWaitMs: 30_000,
    });
    // Re-select the target: creating B selects B, which would un-pause A's nudge.
    await client.request('select_session', { sessionId: targetId });
    note(`author shell ready`, { authorId });

    runAttn(['ticket', 'comment', ticketId, '-m', 'Please take a look when you can.', '--session', authorId]);
    note(`author commented on the ticket -> should notify the target participant`);

    // 3) The notify reaches A as unread. A is selected, so the daemon pauses the
    //    nudge: ticket_unread true, nudge_fires_at ABSENT (no armed timer). This
    //    single observation proves BOTH participation and pause-while-active.
    const unread = await pollFor(
      () => {
        const s = observer.getSession(targetId);
        return s && s.ticket_unread === true ? s : null;
      },
      `target ${targetId} to show unread ticket activity`,
      30_000,
    );
    note(`target shows unread ticket activity`, {
      ticket_unread: unread.ticket_unread,
      nudge_fires_at: unread.nudge_fires_at ?? null,
      state: unread.state,
    });
    assert(
      unread.nudge_fires_at === undefined || unread.nudge_fires_at === null,
      `selected target's nudge is paused (no armed countdown); got nudge_fires_at=${JSON.stringify(unread.nudge_fires_at)}`,
    );
    assert(IDLE_STATES.has(unread.state), `target is still idle/waiting while paused (got ${unread.state})`);

    // 4) Gate held: the doorbell must NOT have been injected yet.
    const beforeClick = await readPaneText(client, targetId);
    fs.writeFileSync(path.join(runDir, 'pane-before-click.txt'), beforeClick.text, 'utf8');
    assert(
      !beforeClick.text.includes(DOORBELL_SUBSTRING),
      `no doorbell injected before the click (the countdown gate held); pane unexpectedly contains "${DOORBELL_SUBSTRING}"`,
    );
    note(`gate held: no doorbell in target pane before click`);

    // Evidence: the paused "deliver now" button is actually rendered.
    try {
      const shot = await client.request('capture_screenshot_data', { selector: '.nudge-header-trigger' });
      if (shot?.pngBase64) {
        fs.writeFileSync(path.join(runDir, 'paused-trigger-button.png'), Buffer.from(shot.pngBase64, 'base64'));
      }
    } catch (error) {
      console.warn(`[nudge-trigger] paused-button screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
    }

    // 5) Click the REAL "deliver now" button. This exercises the button's onClick ->
    //    sendTriggerNudge -> WS -> handleTriggerNudge chain that no unit/tsc check
    //    covers. handleTriggerNudge doorbells immediately while A is idle + unread.
    const clickRes = await client.request('click_nudge_trigger', {});
    assert(clickRes?.clicked === true, `the trigger button was found and clicked (got ${JSON.stringify(clickRes)})`);
    note(`clicked the deliver-now trigger`, { surface: clickRes.surface });

    // 6) The verbatim doorbell prompt must now land in A's pane. A was PAUSED (no
    //    armed timer), so this single click is structurally the only delivery path
    //    — that is the exactly-once guarantee, without counting noisy rendered text.
    let afterText = '';
    const wantedCore = normalizeWs(DOORBELL_CORE);
    const delivered = await pollFor(
      async () => {
        afterText = (await readPaneText(client, targetId)).text;
        return normalizeWs(afterText).includes(wantedCore) ? afterText : null;
      },
      'the verbatim doorbell prompt to land in the target pane after the click',
      20_000,
      500,
    ).catch(() => null);
    fs.writeFileSync(path.join(runDir, 'pane-after-click.txt'), afterText, 'utf8');
    assert(
      delivered !== null,
      `the doorbell ("${DOORBELL_CORE}") landed in the target pane after clicking deliver-now (see pane-after-click.txt)`,
    );
    note(`doorbell delivered: verbatim prompt present in target pane after the click`);

    saveEvidence('passed');
    console.log('[nudge-trigger] Nudge trigger scenario passed: gate held, then the real button delivered the doorbell.');
    console.log(JSON.stringify(evidence, null, 2));
  } catch (error) {
    saveEvidence(`error: ${error instanceof Error ? error.message : String(error)}`);
    throw error;
  } finally {
    if (authorId) await client.request('close_session', { sessionId: authorId }).catch(() => {});
    if (targetId) await client.request('close_session', { sessionId: targetId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
