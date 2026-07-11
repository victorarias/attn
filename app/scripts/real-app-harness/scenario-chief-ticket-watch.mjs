#!/usr/bin/env node

/**
 * Real-agent benchmark: does a chief of staff, given the always-on ChiefGuidance
 * system prompt, ACTUALLY delegate and react proactively when a delegated ticket
 * changes — with no further human prompting? A runtime may optionally arm
 * `attn ticket inbox --watch`, but every non-approval chief shares the same ticket
 * nudge path when unread activity remains.
 *
 * This is an instruction-following benchmark, not a unit test: it stands up a REAL
 * Claude (or Codex) chief in an isolated packaged app, types a human-sounding
 * delegation request into its terminal exactly as a person would, and then only
 * OBSERVES. It never coaches the chief toward the "right" behavior.
 *
 * Flow:
 *   1. Seed a small git repo (CHANGELOG.md + README.md) as the chief's cwd so the
 *      delegated task is concrete.
 *   2. Create a real <agent> session ALREADY as chief (the "create as chief" toggle:
 *      create_session with chief_of_staff:true). The daemon assigns the chief role
 *      BEFORE spawn, so the very first launch injects ChiefGuidance — no promote, no
 *      reload (an empty zero-turn session can't be resumed, which is why the first
 *      launch, not a post-launch reload, must carry the guidance).
 *   3. GATE: confirm the daemon holds the role for this session AND its agent process
 *      was actually launched with the runtime-specific guidance carried by its
 *      --append-system-prompt / developer_instructions. Fail fast here so a setup
 *      miss never masquerades as a behavioral failure.
 *   4. Type the human prompt. Observe (no coaching):
 *        - did the chief DELEGATE? (a ticket bound to a NEW worker session appears)
 *        - did it optionally arm a watch, or leave unread activity for the shared
 *          nudge path?
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
 * profile install built from this branch (so its bundled attn has the runtime-aware
 * guidance and the create-as-chief spawn path) — e.g. `make install PROFILE=uat`.
 *
 * Repeatability: the chief role is profile-wide and persists in the profile DB, and
 * create-as-chief SKIPS when a chief already exists (it never transfers). So this
 * benchmark demotes any leftover chief at startup and again on teardown, leaving the
 * profile with no chief for the next run.
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

// Pin the chief's model per agent so the benchmark measures the ChiefGuidance on a
// known model rather than whatever the agent defaults to. Empty => agent default.
// Set via the chief_model_<agent> setting before create_session; cleared in teardown.
const CHIEF_MODELS = {
  claude: 'opus',
  codex: 'gpt-5.5',
};

// --no-watch variant: tell the chief to delegate but explicitly NOT arm a watch /
// Monitor. This isolates the shared daemon nudge path for either runtime.
const NO_WATCH_PROMPTS = {
  claude:
    'hey can you get someone going on a quick CHANGELOG audit? skim the last couple ' +
    'weeks of entries and flag any that read like they were written for maintainers ' +
    'instead of users, or that are just vague. have them list the worst ones with a ' +
    "suggested rewrite. don't bother setting up any watch or monitor on it — just hand " +
    "it off and i'll check back myself. stepping out",
  codex:
    'morning — can you hand a small thing off to someone: go through the README ' +
    'quickstart and check every command still exists in the CLI (nothing renamed or ' +
    "dropped). just want a list of anything stale. don't set up any watch on it, i'll " +
    "circle back myself. i'm in meetings most of the day",
};

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  let agent = 'claude';
  let noWatch = false;
  const rest = [];
  for (let i = 0; i < args.length; i += 1) {
    if (args[i] === '--agent') agent = args[++i];
    else if (args[i] === '--no-watch') noWatch = true;
    else rest.push(args[i]);
  }
  const options = parseCommonArgs(rest);
  return { options, agent, noWatch, help: args.includes('--help') || args.includes('-h') };
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
    const env = { ...process.env, ATTN_PROFILE: profile };
    delete env.ATTN_SOCKET_PATH;
    delete env.ATTN_SESSION_ID;
    delete env.ATTN_WRAPPER_PATH;
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env,
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

// Live `attn ticket inbox --watch` processes as "pid command" lines. The agent's
// OWN launch process also contains "ticket inbox --watch" (inside the guidance
// blob), so exclude anything carrying the guidance or the system-prompt flags —
// what remains is a genuine watch invocation, not the agent reading about it.
function watchProcessLines() {
  return shell(
    `ps -Awwo pid=,command= | grep -- 'ticket inbox --watch'` +
    ` | grep -v 'arm a harness Monitor' | grep -v 'append-system-prompt'` +
    ` | grep -v 'developer_instructions' | grep -v grep`,
  ).split('\n').map((l) => l.trim()).filter(Boolean);
}

const pidOf = (line) => line.split(/\s+/)[0];

// A watch the CHIEF armed = a `--watch` process whose pid was not already running
// at baseline (captured before the prompt). This filters out a stray watch left by
// a prior/other run — e.g. a `smoke-fake-*` Monitor — that would otherwise read as
// a false "armed: YES".
function freshWatchProcesses(baselinePids) {
  return watchProcessLines().filter((l) => !baselinePids.has(pidOf(l))).join('\n');
}

// Did the chief's agent get launched WITH its runtime-specific ChiefGuidance? Each
// marker appears only in the guidance text, not in a live watch command.
function chiefGuidanceProcesses(agent) {
  const marker = agent === 'claude'
    ? 'arm a harness Monitor running'
    : 'ticket nudges are the supported wake-up mechanism';
  return shell(`ps -Awwo pid=,command= | grep -- '${marker}' | grep -v grep`).trim();
}

// Drive a session to the desired chief state via the same UI flow a user would
// (open actions, toggle, confirm any transfer). Idempotent: a no-op when already in
// the wanted state. Used only for teardown/reset here — the chief itself is born via
// create-as-chief, not promotion.
async function setChiefOfStaff(client, sessionId, want) {
  const before = await client.request('chief_of_staff_get_state');
  const isChief = Boolean(before.sessions.find((s) => s.id === sessionId)?.chiefOfStaff);
  if (isChief === want) return;
  await client.request('chief_of_staff_open_actions', { sessionId });
  await client.request('chief_of_staff_toggle');
  const afterToggle = await client.request('chief_of_staff_get_state');
  if (want && afterToggle.transferPrompt) await client.request('chief_of_staff_confirm_transfer');
  const ok = await pollFor(
    async () => {
      const state = await client.request('chief_of_staff_get_state');
      return Boolean(state.sessions.find((s) => s.id === sessionId)?.chiefOfStaff) === want ? state : null;
    },
    `session ${sessionId} chief=${want}`,
    15_000,
  );
  assert(ok, `chief role set to ${want} for ${sessionId}`);
}

// Demote any session that currently holds the chief role, so create-as-chief (which
// skips when a chief exists) starts from a clean slate. Catches a chief left live by
// a prior run that crashed before its teardown.
async function clearAnyChief(client) {
  const state = await client.request('chief_of_staff_get_state').catch(() => ({ sessions: [] }));
  const chief = (state.sessions || []).find((s) => s.chiefOfStaff);
  if (!chief) return null;
  await setChiefOfStaff(client, chief.id, false);
  return chief.id;
}

async function readChiefPane(client, chiefId) {
  const pane = await waitForFirstWorkspacePane(client, chiefId, `chief pane ${chiefId}`, 20_000);
  const res = await client.request('read_pane_text', { sessionId: chiefId, paneId: pane.paneId }, { timeoutMs: 20_000 }).catch(() => null);
  return { paneId: pane.paneId, text: res?.text || '' };
}

async function main() {
  const { options, agent, noWatch, help } = parseArgs(process.argv.slice(2));
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

    // 0) Clean slate: demote any chief left over from a crashed prior run, since
    //    create-as-chief skips (never transfers) when a chief already exists.
    const leftover = await clearAnyChief(client);
    if (leftover) note(`demoted leftover chief from a prior run`, { leftover });

    // 0b) Enable auto-approve BEFORE create_session so the spawn reads it: the chief
    //     must run unattended (the "keep me posted, I'm out" premise), and a default
    //     permission posture stalls it on the first gate. set_setting + create ride
    //     the same ordered WS, so the store has it before spawn (same pattern as
    //     scenario-codex-resume-mapping's set_setting+create). Restored in teardown.
    await client.request('set_setting', { key: 'auto_approve_enabled', value: 'true' });
    note('enabled auto_approve_enabled for the benchmark');

    // 0c) Pin the chief's model (chief_model_<agent>) BEFORE create so the launch
    //     picks it up via --model. Same ordered-WS guarantee as auto_approve above.
    const chiefModel = CHIEF_MODELS[agent] || '';
    if (chiefModel) {
      await client.request('set_setting', { key: `chief_model_${agent}`, value: chiefModel });
      note(`pinned chief model: ${agent}=${chiefModel}`);
    }

    // 1) Create the real agent session ALREADY as chief (the create-as-chief toggle).
    //    The daemon assigns the role before spawn, so the first launch carries
    //    ChiefGuidance — no promote, no in-place reload.
    const created = await client.request('create_session', {
      cwd: repoDir,
      label: `chief-${runId.slice(-6)}`,
      agent,
      chief_of_staff: true,
    });
    chiefId = created.sessionId;
    await observer.waitForSession({ id: chiefId, timeoutMs: 30_000 });
    note(`chief session created (create-as-chief)`, { chiefId });
    await ensureReady(client, chiefId, 90_000);
    note(`chief agent booted to prompt`);

    // 2) GATE (role): the daemon must actually hold the chief role for THIS session.
    //    If it doesn't, the create-as-chief role-set was skipped (e.g. a stale chief
    //    still held the role) — fail fast and distinctly so it never reads as a
    //    behavioral result.
    const roleState = await pollFor(
      async () => {
        const state = await client.request('chief_of_staff_get_state').catch(() => null);
        return state?.sessions?.find((s) => s.id === chiefId)?.chiefOfStaff ? state : null;
      },
      'daemon to hold the chief role for the new session',
      20_000,
    );
    evidence.daemonHoldsRole = Boolean(roleState);
    if (!roleState) {
      await dumpPane('00-chief-no-role');
      saveEvidence('setup-failed-no-role');
      throw new Error('SETUP FAILED: the daemon did not assign the chief role to the new session (create-as-chief was skipped — likely a stale chief still holds the role; reset the profile and re-run).');
    }

    // 3) GATE (guidance): the agent process must actually carry its runtime-specific
    //    guidance in the launch args.
    await pollFor(
      () => Boolean(chiefGuidanceProcesses(agent)),
      'chief agent launched with ChiefGuidance',
      30_000,
    );
    const guidanceProc = chiefGuidanceProcesses(agent);
    evidence.guidanceProcess = guidanceProc;
    if (!guidanceProc) {
      await dumpPane('00-chief-no-guidance');
      saveEvidence('setup-failed-no-guidance');
      throw new Error(`SETUP FAILED: ${agent} chief was not launched with its runtime-specific ChiefGuidance. The create-as-chief role-set did not reach the launch path.`);
    }
    note(`role + guidance verified at first launch`);
    const readyPane = (await dumpPane('01-chief-ready-with-guidance')) || '';

    // 3b) GATE (model): if we pinned a model, the chief's status line must show it.
    //     A silent mispin (e.g. --model not threaded) would otherwise mislabel a
    //     default-model result as the pinned one. Fail fast and distinctly.
    if (chiefModel) {
      if (!new RegExp(chiefModel, 'i').test(readyPane)) {
        console.log('\n=== SETUP FAILED: chief model did not pin ===');
        console.log(readyPane.split('\n').slice(-12).join('\n'));
        saveEvidence('setup-model-not-pinned');
        throw new Error(`SETUP FAILED: chief was not launched on the pinned model "${chiefModel}" (status line never mentions it); the chief_model_${agent} setting did not reach --model.`);
      }
      note(`chief model pinned: ${chiefModel}`);
    }

    // Snapshot any pre-existing `--watch` processes (a stray one left by a prior
    // run) so only a watch the chief arms AFTER the prompt counts as armed.
    const baselineWatchPids = new Set(watchProcessLines().map(pidOf));

    // 4) Type the human prompt — then only observe.
    const prompt = (noWatch ? NO_WATCH_PROMPTS : PROMPTS)[agent];
    const pane = await waitForFirstWorkspacePane(client, chiefId, `chief pane ${chiefId}`, 20_000);
    // write_pane goes straight to the worker PTY (sendRuntimeInput) — no DOM
    // focus needed, so no click_pane gate. This is what a human's keystrokes
    // become on stdin. CRUCIAL: send the text and the Enter as SEPARATE writes
    // with a gap between them. Claude's TUI treats a fast burst ending in CR as a
    // bracketed paste — the CR lands as a literal newline in the buffer and never
    // submits (observed: prompt sat in the input at "0 tokens"). A standalone CR
    // arriving after the paste settles is a genuine Enter, which submits.
    await client.request('write_pane', { sessionId: chiefId, paneId: pane.paneId, text: prompt, submit: false });
    await delay(1_200);
    await client.request('write_pane', { sessionId: chiefId, paneId: pane.paneId, text: '\r', submit: false });
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

    // 5) Observe: delegation (a NEW ticket bound to a non-chief session) and any
    // optional watch arming. A watch is evidence, never a prerequisite.
    // No coaching. Snapshot the pane along the way so we can read the chief's reasoning.
    let delegation = null;
    let armedWatch = '';
    const observeUntil = Date.now() + 240_000;
    let snap = 0;
    while (Date.now() < observeUntil && !delegation) {
      const { tickets } = await client.request('ticket_list').catch(() => ({ tickets: [] }));
      const bound = (tickets || []).find((tk) => !baselineIds.has(tk.id) && tk.assignee && tk.assignee !== chiefId);
      if (bound) { delegation = bound; break; }
      const w = freshWatchProcesses(baselineWatchPids);
      if (w && !armedWatch) { armedWatch = w; note(`watch armed`, { processes: w.split('\n').length }); }
      if (snap % 6 === 0) await dumpPane(`02-observe-${String(snap).padStart(2, '0')}`);
      snap += 1;
      await delay(2_500);
    }
    if (!armedWatch) armedWatch = freshWatchProcesses(baselineWatchPids);
    evidence.armedWatch = armedWatch;
    evidence.delegated = Boolean(delegation);

    const chiefText = await dumpPane('03-chief-after-observe');

    if (!delegation) {
      const finalState = chiefState();
      evidence.chiefState = finalState;
      if (finalState === 'pending_approval') {
        // BLOCKED on a permission-approval gate, not a refusal. A non-yolo chief
        // runs in claude's default permission mode (attn adds
        // --dangerously-skip-permissions only for yolo, and its settings carry no
        // allow-list), so it stalls at the first gated command and cannot proceed
        // unattended. This is a distinct verdict so a blocked chief never reads as
        // the behavioral "did-not-delegate" finding.
        note(`observe window elapsed with chief BLOCKED on approval`, { finalState });
        saveEvidence('blocked-on-approval');
        console.log('\n=== BLOCKED: chief is stuck on a permission-approval prompt ===');
        console.log('Not a behavioral finding — the chief never got to decide. A human (or');
        console.log('yolo mode) would unblock it. Decide the permission posture, then re-run.');
        console.log('--- chief pane (tail) ---');
        console.log(chiefText.split('\n').slice(-40).join('\n'));
        return;
      }
      if (finalState === 'working' || finalState === 'launching') {
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

    if (noWatch) {
      // === SHARED DAEMON NUDGE PATH (--no-watch) ===
      // The chief was told NOT to arm a Monitor. Any state except pending approval
      // can receive the bounded nudge once it is not the selected pane.
      const chiefEligible = () => chiefState() !== 'pending_approval';

      // 6a) Do not send into an approval prompt, then select the worker so the
      //     chief's countdown is allowed to run even if it remains active/green.
      const eligible = await pollFor(() => (chiefEligible() ? true : null), 'chief to leave pending approval (no-watch)', 120_000, 1_500);
      if (!eligible) {
        await dumpPane('06-nowatch-pending-approval');
        note(`chief remained pending approval in the no-watch window`, { finalState: chiefState() });
        saveEvidence('nowatch-blocked-on-approval');
        console.log('\n=== INCONCLUSIVE: chief remained at an approval prompt ===');
        return;
      }
      await client.request('select_session', { sessionId: workerId });
      note(`selected worker so the chief's shared nudge countdown can run`);
      const strayWatch = freshWatchProcesses(baselineWatchPids);
      if (strayWatch) {
        // The chief armed a watch despite being told not to — it may consume the
        // event before the countdown, so report the alternate consumer outcome.
        note(`chief ARMED A WATCH despite the no-watch prompt`, { processes: strayWatch.split('\n').length });
      } else {
        note(`chief is nudge-eligible with no watch armed`);
      }

      // 6b) Now fire the producer event. With unread activity and no watch, the
      //     daemon schedules the shared countdown and doorbells the chief.
      runAttn(['ticket', 'status', 'ready_for_review', '--comment', 'Audit done — 3 entries flagged, rewrites in the report.', '--session', workerId]);
      note(`worker reported ready_for_review`);

      // 6c) Wait for the daemon doorbell to land in the chief pane. The exact text is
      //     the fixed ticketNudgePrompt the daemon types in ("New ticket activity").
      const nudgeUntil = Date.now() + 90_000;
      let nudged = null;
      let reactedNw = null;
      let nsnap = 0;
      while (Date.now() < nudgeUntil) {
        const text = await dumpPane(`06-nudge-${String(nsnap).padStart(2, '0')}`);
        if (!nudged && /New ticket activity/i.test(text)) { nudged = text; note(`shared daemon nudge delivered to chief`); }
        if (nudged && /ticket inbox|ready[ _]for[ _]review|in review|review the|audit/i.test(text.split('\n').slice(-25).join('\n'))) { reactedNw = text; break; }
        nsnap += 1;
        await delay(3_000);
      }
      evidence.sharedNudgeDelivered = Boolean(nudged);
      evidence.reacted = Boolean(reactedNw);
      evidence.strayWatch = Boolean(strayWatch);
      const finalText = await dumpPane('07-chief-final-nowatch');

      const verdict = !nudged
        ? (strayWatch ? 'nowatch-chief-self-armed-watch' : 'shared-nudge-not-delivered')
        : (reactedNw ? 'shared-nudge-and-reacted' : 'shared-nudge-no-reaction');
      saveEvidence(verdict);
      console.log(`\n=== VERDICT: ${verdict} ===`);
      console.log(`delegated: yes (worker=${workerId} ticket=${ticketId})`);
      console.log(`chief armed its own watch despite no-watch prompt: ${strayWatch ? 'YES' : 'no'}`);
      console.log(`shared daemon nudge delivered: ${nudged ? 'YES' : 'no'}`);
      console.log(`visible reaction to the nudge: ${reactedNw ? 'YES' : 'no'}`);
      console.log('--- chief pane (tail) ---');
      console.log(finalText.split('\n').slice(-45).join('\n'));
      return;
    }

    // The controlled report may land while the chief is active: the shared policy
    // permits every state except pending approval. Select the worker so a countdown
    // is not intentionally paused by the user's current pane.
    const chiefEligible = () => chiefState() !== 'pending_approval';
    const settled = await pollFor(() => {
      if (!armedWatch) armedWatch = freshWatchProcesses(baselineWatchPids);
      return chiefEligible() ? true : null;
    }, 'chief to leave pending approval', 120_000, 1_500);
    if (!settled) {
      await dumpPane('04-chief-pending-approval');
      evidence.armedWatch = armedWatch;
      evidence.chiefState = chiefState();
      saveEvidence('inconclusive-chief-pending-approval');
      console.log('\n=== INCONCLUSIVE: chief remained at an approval prompt ===');
      console.log(`chief state: ${chiefState()}`);
      console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
      return;
    }
    await client.request('select_session', { sessionId: workerId });
    note(`selected worker so the chief's shared nudge countdown can run`);

    // 6) Drive the worker to report ready_for_review — the real producer path. The
    // chief cannot tell this from the sub-agent finishing on its own.
    await delay(2_000);
    runAttn(['ticket', 'status', 'ready_for_review', '--comment', 'Audit done — 3 entries flagged, rewrites in the report.', '--session', workerId]);
    note(`worker reported ready_for_review`);

    // 7) Observe whether the chief surfaces the update ON ITS OWN. An optional watch
    // may consume it before the shared countdown rings; otherwise the fixed nudge
    // must arrive. Evidence = the chief runs `attn ticket inbox` / mentions the
    // review without us prompting it again.
    const reactUntil = Date.now() + 240_000;
    let reacted = null;
    let nudged = null;
    let rsnap = 0;
    while (Date.now() < reactUntil) {
      const text = await dumpPane(`04-react-${String(rsnap).padStart(2, '0')}`);
      const recent = text.split('\n').slice(-35).join('\n');
      if (!nudged && /New ticket activity/i.test(text)) {
        nudged = text;
        note(`shared daemon ticket nudge delivered to chief`);
      }
      const handledUpdate = /ticket inbox|ready[ _]for[ _]review|in review|review the report/i.test(recent);
      if (!reacted && handledUpdate && (armedWatch || nudged)) {
        reacted = text;
      }
      if (!armedWatch) {
        const w = freshWatchProcesses(baselineWatchPids);
        if (w) {
          armedWatch = w;
          note(`optional watch armed (post-delegation)`, { processes: w.split('\n').length });
        }
      }
      if (reacted) break;
      rsnap += 1;
      await delay(3_000);
    }
    evidence.armedWatch = armedWatch;
    evidence.nudged = Boolean(nudged);
    evidence.reacted = Boolean(reacted);
    const finalText = await dumpPane('05-chief-final');

    const verdict = reacted
      ? 'delegated-and-reacted'
      : (armedWatch ? 'watch-no-visible-reaction' : 'shared-nudge-not-delivered');
    saveEvidence(verdict);
    console.log(`\n=== VERDICT: ${evidence.verdict} ===`);
    console.log(`delegated: yes (worker=${workerId} ticket=${ticketId})`);
    console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
    console.log(`daemon nudge delivered: ${nudged ? 'YES' : 'no'}`);
    console.log(`visible reaction to the report: ${reacted ? 'YES' : 'no'}`);
    console.log('--- chief pane (tail) ---');
    console.log(finalText.split('\n').slice(-45).join('\n'));
  } catch (error) {
    saveEvidence(evidence.verdict || 'error');
    throw error;
  } finally {
    if (workerId) await client.request('close_session', { sessionId: workerId }).catch(() => {});
    // The chief is protected from closing while it holds the role, so demote it
    // first (this also clears the role so the next run's create-as-chief is not
    // blocked), then close.
    if (chiefId) {
      await setChiefOfStaff(client, chiefId, false).catch(() => {});
      await client.request('close_session', { sessionId: chiefId }).catch(() => {});
    }
    // Restore the global auto-approve setting so it never leaks past the benchmark.
    await client.request('set_setting', { key: 'auto_approve_enabled', value: 'false' }).catch(() => {});
    // Clear the pinned chief model so it never leaks past the benchmark.
    if (CHIEF_MODELS[agent]) {
      await client.request('set_setting', { key: `chief_model_${agent}`, value: '' }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
    console.log(`[chief-watch] artifacts in ${runDir}`);
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
