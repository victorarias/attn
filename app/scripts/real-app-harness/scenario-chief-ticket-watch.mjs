#!/usr/bin/env node

/**
 * Real-agent benchmark: does a chief of staff, given the always-on ChiefGuidance
 * system prompt, ACTUALLY delegate, arm `attn ticket inbox --watch`, and react
 * proactively when a delegated ticket changes — with no further human prompting?
 *
 * This is an instruction-following benchmark, not a unit test: it stands up a REAL
 * Claude (or Codex) chief in an isolated packaged app, types a human-sounding
 * delegation request into its terminal exactly as a person would, and then only
 * OBSERVES. It never coaches the chief toward the "right" behavior.
 *
 * Flow:
 *   1. Seed a small git repo (CHANGELOG.md + README.md) as the chief's cwd so the
 *      delegated task is concrete.
 *   2. Create a real <agent> session, then promote it to chief. The daemon now
 *      RELOADS the agent in place (kill + resume-respawn) on chief-assign, so the
 *      relaunched process carries ChiefGuidance — no app relaunch needed.
 *   3. GATE: confirm the chief's agent process was actually launched with the new
 *      guidance (its --append-system-prompt carries "attn ticket inbox --watch").
 *      Fail fast here so a setup miss never masquerades as a behavioral failure.
 *   4. Type the human prompt. Observe (no coaching):
 *        - did the chief DELEGATE? (a ticket bound to a NEW worker session appears)
 *        - did it ARM THE WATCH? (a live `attn ticket inbox --watch` process)
 *      If the chief does the task ITSELF instead of delegating, capture the evidence
 *      and STOP with verdict=did-not-delegate — that is a finding to discuss, not a
 *      thing to auto-correct.
 *   5. If it delegated: drive the worker to report ready_for_review (the real
 *      producer path; the chief can't tell this from the sub-agent finishing), then
 *      observe whether the chief surfaces the update on its own.
 *
 * Run serially (packaged-app scenarios are single-tenant), one agent at a time:
 *   ATTN_HARNESS_PROFILE=uat node scripts/real-app-harness/scenario-chief-ticket-watch.mjs --agent claude
 *   ATTN_HARNESS_PROFILE=uat node scripts/real-app-harness/scenario-chief-ticket-watch.mjs --agent codex
 *
 * Prereqs: claude/codex on PATH; a built ./attn (or ATTN_HARNESS_BIN); a non-prod
 * profile install built from this branch (so its bundled attn has `--watch` and the
 * new guidance) — e.g. `make install PROFILE=uat`.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createRunContext,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import {
  preTrustClaudeFolder,
  ensureClaudePromptReadyViaPty,
  ensureCodexPromptReadyViaPty,
} from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

// The two human prompts (approved by Victor). Casual, no mention of tests, tickets,
// watching, or the feature — a person handing work to their chief of staff.
const PROMPTS = {
  claude:
    'hey can you get someone going on a quick CHANGELOG audit? skim the last couple ' +
    'weeks of entries and flag any that read like they were written for maintainers ' +
    'instead of users, or that are just vague. have them list the worst ones with a ' +
    'suggested rewrite. stepping out, keep me posted',
  codex:
    'morning — can you hand a small thing off to someone: go through the README ' +
    'quickstart and check every command still exists in the CLI (nothing renamed or ' +
    'dropped). just want a list of anything stale. i\'m in meetings most of the day, ' +
    'ping me when there\'s something to look at',
};

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  let agent = 'claude';
  const rest = [];
  for (let i = 0; i < args.length; i += 1) {
    if (args[i] === '--agent') agent = args[++i];
    else rest.push(args[i]);
  }
  const options = parseCommonArgs(rest);
  return { options, agent, help: args.includes('--help') || args.includes('-h') };
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

const delay = (ms) => new Promise((r) => setTimeout(r, ms));

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 500) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  return null; // caller decides whether a miss is fatal
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

// Tolerant shell: returns stdout (or '' on non-zero exit) so probes never throw.
function shell(cmd) {
  try {
    return execFileSync('bash', ['-lc', cmd], { encoding: 'utf8' });
  } catch (error) {
    return error.stdout ? String(error.stdout) : '';
  }
}

// Is a live `attn ticket inbox --watch` Monitor running (the chief armed the watch)?
// The agent's OWN launch process also contains "ticket inbox --watch" (inside the
// guidance blob), so exclude anything carrying the guidance or the system-prompt
// flags — what remains is a genuine watch invocation, not the agent reading about it.
function watchProcesses() {
  return shell(
    `ps -Awwo pid=,command= | grep -- 'ticket inbox --watch'` +
    ` | grep -v 'arm a harness Monitor' | grep -v 'append-system-prompt'` +
    ` | grep -v 'developer_instructions' | grep -v grep`,
  ).trim();
}

// Did the chief's agent get launched WITH the new ChiefGuidance? "arm a harness
// Monitor running" is a phrase that appears ONLY in the guidance text (not in any
// watch command), and it rides in via --append-system-prompt (Claude) or
// developer_instructions (Codex) — so a match is agent-agnostic proof the chief
// carries this branch's guidance.
function chiefGuidanceProcesses() {
  return shell(`ps -Awwo pid=,command= | grep -- 'arm a harness Monitor running' | grep -v grep`).trim();
}

async function setChiefOfStaff(client, sessionId) {
  const before = await client.request('chief_of_staff_get_state');
  if (before.sessions.find((s) => s.id === sessionId)?.chiefOfStaff) return;
  await client.request('chief_of_staff_open_actions', { sessionId });
  await client.request('chief_of_staff_toggle');
  const afterToggle = await client.request('chief_of_staff_get_state');
  if (afterToggle.transferPrompt) await client.request('chief_of_staff_confirm_transfer');
  const ok = await pollFor(
    async () => {
      const state = await client.request('chief_of_staff_get_state');
      return state.sessions.find((s) => s.id === sessionId)?.chiefOfStaff ? state : null;
    },
    `session ${sessionId} to become chief-of-staff`,
    15_000,
  );
  assert(ok, `chief role applied to ${sessionId}`);
}

async function readChiefPane(client, chiefId) {
  const pane = await waitForFirstWorkspacePane(client, chiefId, `chief pane ${chiefId}`, 20_000);
  const res = await client.request('read_pane_text', { sessionId: chiefId, paneId: pane.paneId }, { timeoutMs: 20_000 }).catch(() => null);
  return { paneId: pane.paneId, text: res?.text || '' };
}

async function main() {
  const { options, agent, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-chief-ticket-watch.mjs');
    console.log('\n  --agent claude|codex   which agent the chief runs as (default claude)');
    return;
  }
  assert(agent === 'claude' || agent === 'codex', `--agent must be claude or codex (got ${agent})`);

  const profile = currentHarnessProfile();
  if (!profile) throw new Error('this benchmark never runs against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, `chief-watch-${agent}`);

  // Seed the chief's cwd: a git repo with files the delegated task can plausibly act
  // on, so "delegate this audit" is concrete rather than vacuous.
  const repoDir = path.join(sessionDir, 'chief-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  fs.writeFileSync(path.join(repoDir, 'CHANGELOG.md'),
    '# Changelog\n\n## [2026-06-28]\n- Refactored the FooManager to use the new BarAdapter interface.\n' +
    '- Fixed a bug.\n- Bumped internal protocol to v3 and migrated the store schema.\n\n' +
    '## [2026-06-27]\n- Users can now pin workspaces so they stay in the sidebar when empty.\n' +
    '- Various improvements.\n', 'utf8');
  fs.writeFileSync(path.join(repoDir, 'README.md'),
    '# demo\n\n## Quickstart\n\n```\nattn list\nattn delegate --brief "..."\nattn dispatch update\nattn ticket status ready_for_review\n```\n', 'utf8');
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['add', '-A'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '-m', 'seed'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });
  if (agent === 'claude') preTrustClaudeFolder(repoDir);

  const ensureReady = agent === 'claude' ? ensureClaudePromptReadyViaPty : ensureCodexPromptReadyViaPty;
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let chiefId = null;
  let workerId = null;
  const evidence = { runId, profile, agent, steps: [] };
  const note = (m, extra) => { console.log(`[chief-watch] ${m}`); evidence.steps.push({ t: Date.now(), m, ...(extra || {}) }); };
  const saveEvidence = (verdict) => {
    evidence.verdict = verdict;
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  };
  const dumpPane = async (name) => {
    if (!chiefId) return;
    const { text } = await readChiefPane(client, chiefId).catch(() => ({ text: '' }));
    fs.writeFileSync(path.join(runDir, `${name}.txt`), text, 'utf8');
    return text;
  };

  console.log(`[chief-watch] profile=${profile} agent=${agent} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    // 1) Create the real agent session and wait until it is at its prompt.
    const created = await client.request('create_session', { cwd: repoDir, label: `chief-${runId.slice(-6)}`, agent });
    chiefId = created.sessionId;
    await observer.waitForSession({ id: chiefId, timeoutMs: 30_000 });
    note(`session created`, { chiefId });
    await ensureReady(client, chiefId, 90_000);
    note(`agent booted to prompt`);

    // 2) Promote to chief. The daemon reloads the agent in place (kill + resume-
    //    respawn) so the relaunched process carries ChiefGuidance — no app relaunch.
    await client.request('select_session', { sessionId: chiefId });
    await setChiefOfStaff(client, chiefId);
    note(`promoted to chief-of-staff; daemon reloading agent in place`);

    // 3) GATE: the reload is async server-side, so wait for a guidance-bearing
    //    worker to come up, then for the resumed agent to reach its prompt. The
    //    chief must actually carry the new guidance or the behavioral result is
    //    meaningless — fail fast and distinctly on a setup miss.
    await pollFor(
      () => Boolean(chiefGuidanceProcesses()),
      'chief agent reloaded in place with ChiefGuidance',
      60_000,
    );
    await observer.waitForSession({ id: chiefId, timeoutMs: 30_000 });
    await ensureReady(client, chiefId, 90_000);
    note(`chief reloaded in place; agent respawned and ready`);

    const guidanceProc = chiefGuidanceProcesses();
    evidence.guidanceProcess = guidanceProc;
    if (!guidanceProc) {
      await dumpPane('00-chief-after-reload');
      saveEvidence('setup-failed-no-guidance');
      throw new Error('SETUP FAILED: chief agent was not reloaded with ChiefGuidance (no process carries the "ticket inbox --watch" system prompt). The chief-assign reload did not inject guidance.');
    }
    note(`guidance verified in chief launch args`);
    await dumpPane('01-chief-ready-with-guidance');

    // 4) Type the human prompt — then only observe.
    const prompt = PROMPTS[agent];
    const pane = await waitForFirstWorkspacePane(client, chiefId, `chief pane ${chiefId}`, 20_000);
    // write_pane goes straight to the worker PTY (sendRuntimeInput) — no DOM
    // focus needed, so no click_pane gate. This is what a human's keystrokes
    // become on stdin; the trailing CR submits.
    await client.request('write_pane', { sessionId: chiefId, paneId: pane.paneId, text: `${prompt}\r`, submit: false });
    note(`human prompt sent`, { prompt });

    // Live chief state from the daemon (kept current via session_state_changed).
    const chiefState = () => observer.getSession(chiefId)?.state || 'unknown';
    evidence.stateBeforePrompt = chiefState();

    // 4a) Confirm the prompt actually LANDED before we judge behavior: the chief
    // must start working. If it never leaves idle/launching, the write didn't take
    // (still booting, CR didn't submit, ...) — a harness glitch, NOT the behavioral
    // "did-not-delegate" finding. Distinguishing this protects Victor's "don't
    // auto-decide" instruction from firing on a setup problem.
    const started = await pollFor(
      () => (chiefState() === 'working' ? true : null),
      'chief to start working after the prompt',
      30_000,
      500,
    );
    if (!started) {
      await dumpPane('02-prompt-not-accepted');
      evidence.chiefState = chiefState();
      saveEvidence('prompt-not-accepted');
      console.log('\n=== HARNESS ISSUE: chief never started working after the prompt ===');
      console.log(`chief state: ${chiefState()} (expected to pass through "working")`);
      console.log('Setup/timing problem, not a behavioral finding. Re-run.');
      return;
    }
    note(`chief started working (prompt accepted)`);

    // 4b) Snapshot existing tickets so a PRIOR run's delegation can't masquerade as
    // this run's (the uat ticket DB persists across runs). Only a NEW ticket counts.
    const baseline = await client.request('ticket_list').catch(() => ({ tickets: [] }));
    const baselineIds = new Set((baseline.tickets || []).map((tk) => tk.id));
    note(`ticket baseline captured`, { existing: baselineIds.size });

    // 5) Observe: delegation (a NEW ticket bound to a non-chief session) + watch-arming.
    // No coaching. Snapshot the pane along the way so we can read the chief's reasoning.
    let delegation = null;
    let armedWatch = '';
    const observeUntil = Date.now() + 240_000;
    let snap = 0;
    while (Date.now() < observeUntil && !delegation) {
      const { tickets } = await client.request('ticket_list').catch(() => ({ tickets: [] }));
      const bound = (tickets || []).find((tk) => !baselineIds.has(tk.id) && tk.assignee && tk.assignee !== chiefId);
      if (bound) { delegation = bound; break; }
      const w = watchProcesses();
      if (w && !armedWatch) { armedWatch = w; note(`watch armed`, { processes: w.split('\n').length }); }
      if (snap % 6 === 0) await dumpPane(`02-observe-${String(snap).padStart(2, '0')}`);
      snap += 1;
      await delay(2_500);
    }
    if (!armedWatch) armedWatch = watchProcesses();
    evidence.armedWatch = armedWatch;
    evidence.delegated = Boolean(delegation);

    const chiefText = await dumpPane('03-chief-after-observe');

    if (!delegation) {
      const finalState = chiefState();
      evidence.chiefState = finalState;
      if (finalState === 'working') {
        // Still deliberating at the deadline — Opus at xhigh can take a while. This
        // is NOT a refusal; don't trip the discuss-this path on a slow-but-active
        // chief. Bump the window or re-run.
        note(`observe window elapsed while chief STILL WORKING`);
        saveEvidence('inconclusive-still-working');
        console.log('\n=== INCONCLUSIVE: chief still working at the deadline ===');
        console.log('Not a finding — the chief never finished. Bump the window or re-run.');
        console.log('--- chief pane (tail) ---');
        console.log(chiefText.split('\n').slice(-40).join('\n'));
        return;
      }
      // Chief FINISHED (idle/waiting_input/...) without delegating — per Victor's
      // caveat, the real finding to DISCUSS. Stop here; do not coax it.
      note(`chief FINISHED without delegating`, { finalState });
      saveEvidence('did-not-delegate');
      console.log('\n=== VERDICT: chief did NOT delegate ===');
      console.log(`chief final state: ${finalState}`);
      console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
      console.log('--- chief pane (tail) ---');
      console.log(chiefText.split('\n').slice(-40).join('\n'));
      console.log('\nStopping for discussion (not auto-deciding the next step).');
      return;
    }

    workerId = delegation.assignee;
    const ticketId = delegation.id;
    note(`DELEGATED`, { workerId, ticketId, armedWatch: Boolean(armedWatch) });
    await observer.waitForSession({ id: workerId, timeoutMs: 30_000 }).catch(() => {});

    // 6) Drive the worker to report ready_for_review — the real producer path. The
    // chief cannot tell this from the sub-agent finishing on its own.
    await delay(2_000);
    runAttn(['ticket', 'status', 'ready_for_review', '--comment', 'Audit done — 3 entries flagged, rewrites in the report.', '--session', workerId]);
    note(`worker reported ready_for_review`);

    // 7) Observe whether the chief surfaces the update ON ITS OWN. Claude: via its
    // armed --watch Monitor or the daemon backstop. Codex (not a self-monitor): via
    // the daemon's direct nudge. Evidence = the chief runs `attn ticket inbox` /
    // mentions the review without us prompting it again.
    const reactUntil = Date.now() + 150_000;
    let reacted = null;
    let rsnap = 0;
    while (Date.now() < reactUntil && !reacted) {
      const text = await dumpPane(`04-react-${String(rsnap).padStart(2, '0')}`);
      if (/ticket inbox|ready[ _]for[ _]review|in review|review the|delegate|worker|audit/i.test(text.split('\n').slice(-25).join('\n'))) {
        // Heuristic: recent pane mentions the inbox/review. Confirmed by reading below.
        reacted = text;
        break;
      }
      rsnap += 1;
      await delay(3_000);
    }
    evidence.reacted = Boolean(reacted);
    const finalText = await dumpPane('05-chief-final');

    saveEvidence(reacted ? 'delegated-and-reacted' : 'delegated-no-visible-reaction');
    console.log(`\n=== VERDICT: ${evidence.verdict} ===`);
    console.log(`delegated: yes (worker=${workerId} ticket=${ticketId})`);
    console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
    console.log(`visible reaction to the report: ${reacted ? 'YES' : 'no'}`);
    console.log('--- chief pane (tail) ---');
    console.log(finalText.split('\n').slice(-45).join('\n'));
  } catch (error) {
    saveEvidence(evidence.verdict || 'error');
    throw error;
  } finally {
    if (workerId) await client.request('close_session', { sessionId: workerId }).catch(() => {});
    if (chiefId) await client.request('close_session', { sessionId: chiefId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
    console.log(`[chief-watch] artifacts in ${runDir}`);
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
