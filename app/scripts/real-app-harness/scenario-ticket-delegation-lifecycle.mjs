#!/usr/bin/env node

/**
 * Real-app scenario: the full ticket lifecycle, driven end-to-end with no human
 * clicks.
 *
 * Bootstraps a chief-of-staff, delegates a real codex worker (which mints a bound
 * ticket), drives the WORKER side via the agent CLI verbs (`attn ticket
 * status|attach`), then opens and drives the CHIEF side entirely through the
 * packaged app's TicketDetailPanel via the new `ticket_*` automation actions —
 * which click the real controls, so the panel's own gating runs. Asserts the
 * rendered panel reflects every step (status moves, comment, edited description,
 * artifacts, resumable agent, and that `crashed` is NOT a manual destination),
 * and captures an occlusion-proof screenshot of the populated panel.
 *
 * Bootstrap (chief + delegate) is self-contained: no existing scenario sets up a
 * chief or a delegation, so this one does it. The worker just needs to *spawn*
 * (codex sitting at its prompt is fine) — the ticket is created by delegation.
 *
 * Prereqs: `codex` on PATH; a built `./attn` (or ATTN_HARNESS_BIN); a non-prod
 * profile install with the automation layer (`make install PROFILE=<name>` —
 * automation is on for every named profile, see profile::automation_enabled).
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
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

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

// Resolve the attn binary the same way harnessProfile does (ATTN_HARNESS_BIN, then
// repo-root ./attn). Used for the real delegation + agent-side ticket verbs.
function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function makeAttnRunner(attnBin, profile) {
  // ATTN_SOCKET_PATH is injected into attn-managed sessions and takes precedence
  // over ATTN_PROFILE, so set both explicitly when the harness targets a sibling
  // profile. The leading `[attn profile=…]` banner is stripped so JSON parses.
  const socketPath = socketPathForProfile(profile);
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile, ATTN_SOCKET_PATH: socketPath },
    });
    const brace = stdout.indexOf('{');
    return { stdout, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
  };
}

// Set `sessionId` as chief-of-staff through the real sidebar UI (open the session
// actions, click the chief toggle), confirming a transfer prompt if a previous
// chief still exists. Idempotent enough for reruns against a dirty profile.
async function setChiefOfStaff(client, sessionId) {
  const before = await client.request('chief_of_staff_get_state');
  if (before.sessions.find((session) => session.id === sessionId)?.chiefOfStaff) {
    return;
  }
  await client.request('chief_of_staff_open_actions', { sessionId });
  await client.request('chief_of_staff_toggle');
  const afterToggle = await client.request('chief_of_staff_get_state');
  if (afterToggle.transferPrompt) {
    await client.request('chief_of_staff_confirm_transfer');
  }
  await pollFor(
    async () => {
      const state = await client.request('chief_of_staff_get_state');
      return state.sessions.find((session) => session.id === sessionId)?.chiefOfStaff ? state : null;
    },
    `session ${sessionId} to become chief-of-staff`,
    15_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-ticket-delegation-lifecycle.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the ticket lifecycle scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, 'ticket-lifecycle');

  // Minimal git repo for the chief's cwd (delegation places the worker here).
  const repoDir = path.join(sessionDir, 'chief-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });
  const reportPath = path.join(sessionDir, 'report.md');
  const rolloutPath = path.join(sessionDir, 'rollout.md');
  fs.writeFileSync(reportPath, 'ticket attach report\nsecond line\n', 'utf8');
  fs.writeFileSync(rolloutPath, 'ticket attach rollout\n', 'utf8');

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let chiefId = null;
  let workerId = null;

  console.log(`[RealAppHarness] profile=${profile} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    // 1) Chief session + chief-of-staff role.
    chiefId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `chief-${runId}`,
      agent: 'shell',
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId: chiefId });
    await setChiefOfStaff(client, chiefId);

    // 2) Delegate a real codex worker → mints the bound ticket. The agent/workspace
    // name has a 16-char cap; keep it short and unique-enough across reruns. The
    // scenario binds to the ticket by worker session id, not by name, so name
    // collisions across reruns are harmless.
    const workerName = `tkt-${Date.now().toString(36).slice(-8)}`;
    const brief = 'Slice-4 ticket lifecycle QA fixture. Please wait for direction; do not start coding.';
    const delegate = runAttn(['delegate', '--source-session', chiefId, '--agent', 'codex', '--brief', brief, '--name', workerName]);
    workerId = delegate.json?.session_id;
    assert(typeof workerId === 'string' && workerId.length > 0, `delegate returned a worker session id (got ${JSON.stringify(delegate.json)})`);
    await observer.waitForSession({ id: workerId, timeoutMs: 30_000 });

    // The FE learns about the ticket via broadcasts; wait until it is bound.
    const boundList = await pollFor(
      async () => {
        const { tickets } = await client.request('ticket_list');
        const bound = tickets.find((ticket) => ticket.assignee === workerId);
        return bound ? { bound, tickets } : null;
      },
      'the delegated ticket to appear bound to the worker',
      30_000,
    );
    const ticketId = boundList.bound.id;
    console.log(`[RealAppHarness] worker=${workerId} ticket=${ticketId}`);

    // 3) Worker side via the real agent CLI verbs.
    runAttn(['ticket', 'status', 'in_progress', '--session', workerId]);
    const attachArgs = ['ticket', 'attach', '--file', reportPath, '--file', rolloutPath, '--state', 'ready_for_review', '--comment', 'Please review the handed-over plan.', '--session', workerId, '--json'];
    const attach = runAttn(attachArgs);
    assert(attach.json?.artifacts?.length === 2, `multi-file attach returned two artifacts (got ${JSON.stringify(attach.json)})`);
    const retry = runAttn(attachArgs);
    assert(retry.json?.deduplicated === true && retry.json?.event_seq === attach.json?.event_seq, `retry returned the existing receipt (got ${JSON.stringify(retry.json)})`);

    const canonicalReport = attach.json.artifacts.find((artifact) => artifact.filename === 'report.md')?.path;
    const canonicalRollout = attach.json.artifacts.find((artifact) => artifact.filename === 'rollout.md')?.path;
    assert(canonicalReport && canonicalRollout, 'attach returned canonical report and rollout paths');
    fs.appendFileSync(canonicalReport, 'updated after attach\n', 'utf8');
    const canonicalImplementation = path.join(path.dirname(canonicalRollout), 'implementation.md');
    fs.renameSync(canonicalRollout, canonicalImplementation);
    runAttn(['ticket', 'comment', ticketId, '-m', 'Updated report.md and renamed rollout.md to implementation.md.', '--session', workerId]);

    // 4) Open the panel the real way — the Dashboard "View ticket" link.
    await client.request('ticket_open_via_dashboard', { sessionId: workerId });
    const afterWorker = await pollFor(
      async () => {
        const state = await client.request('ticket_detail_get_state');
        return state.present && state.status === 'in_review' ? state : null;
      },
      'panel to reflect the worker reaching in_review',
      20_000,
    );

    assert(afterWorker.ticketId === ticketId, `panel shows the bound ticket (got ${afterWorker.ticketId})`);
    assert(afterWorker.canResume === true, 'panel offers Resume (ticket carries a bound agent session)');
    assert(!afterWorker.statusOptions.includes('crashed'), `crashed is not a selectable status (options: ${afterWorker.statusOptions.join(', ')})`);
    assert(
      afterWorker.artifacts.some((artifact) => artifact.filename === 'report.md')
        && afterWorker.artifacts.some((artifact) => artifact.filename === 'implementation.md'),
      `filesystem-current artifacts are rendered (got ${JSON.stringify(afterWorker.artifacts)})`,
    );
    assert(
      afterWorker.activity.some((entry) => entry.comment.includes('Please review the handed-over plan')),
      'attach decision context is in the activity history',
    );

    // 5) Chief side — drive the real panel controls. Each mutation drives the
    // actual DOM control (exercising the panel's own gating), then we assert the
    // result via a fresh panel re-fetch. The panel's *live* refresh is broadcast
    // (tickets_updated) driven; under this harness's broadcast traffic that
    // delivery lags, so rather than race it we close+reopen the panel — a real
    // user action that re-runs the panel's get_ticket fetch (a direct store read)
    // independent of broadcast timing. That deterministically proves each mutation
    // reached the server and the panel renders it.
    // Reopen the panel (close → open) so its get_ticket fetch re-runs, then wait
    // for that fetch to settle (the loading row clears) before reading — so a read
    // never races the in-flight fetch and reports stale state.
    const freshPanelState = async () => {
      await client.request('ticket_close_detail');
      await client.request('ticket_open_detail', { ticketId });
      let last = null;
      const startedAt = Date.now();
      while (Date.now() - startedAt < 8_000) {
        last = await client.request('ticket_detail_get_state');
        if (last.present && !last.loading) return last;
        await new Promise((r) => setTimeout(r, 200));
      }
      return last;
    };
    const pollPanel = (predicate, description) =>
      pollFor(
        async () => {
          const state = await freshPanelState();
          return state && predicate(state) ? state : null;
        },
        description,
        30_000,
        500,
      );

    await client.request('ticket_submit_comment', { comment: 'Chief: looks good, one tweak needed.' });
    await pollPanel(
      (state) => state.activity.some((entry) => entry.comment.includes('one tweak needed')),
      'chief comment to render in activity',
    );

    await client.request('ticket_edit_description', { description: 'Slice-4 lifecycle QA (edited by chief).' });
    await pollPanel(
      (state) => state.description.includes('edited by chief'),
      'chief description edit to render',
    );

    await client.request('ticket_set_status', { status: 'blocked' });
    const afterBlocked = await pollPanel(
      (state) => state.status === 'blocked',
      'chief status move to blocked',
    );

    // 6) Crashed guard — the panel never offers it as a manual destination.
    let crashedRejected = false;
    try {
      await client.request('ticket_set_status', { status: 'crashed' });
    } catch {
      crashedRejected = true;
    }
    assert(crashedRejected, 'ticket_set_status(crashed) is rejected (not a selectable destination)');

    // 7) Occlusion-proof screenshot of the populated panel.
    const screenshotPath = path.join(runDir, 'ticket-detail-panel.png');
    let screenshotCaptured = false;
    try {
      const shot = await client.request('capture_screenshot_data', { selector: '[data-testid="ticket-detail-panel"]' });
      if (shot?.pngBase64) {
        fs.writeFileSync(screenshotPath, Buffer.from(shot.pngBase64, 'base64'));
        screenshotCaptured = true;
      }
    } catch (error) {
      console.warn(`[RealAppHarness] Panel screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
    }

    fs.rmSync(canonicalReport);
    runAttn(['ticket', 'comment', ticketId, '-m', 'Removed report.md; implementation.md remains canonical.', '--session', workerId]);
    const afterDelete = await pollPanel(
      (state) => state.artifacts.length === 1 && state.artifacts[0].filename === 'implementation.md',
      'ticket artifact list to reflect direct deletion',
    );

    const summary = {
      ok: true,
      runId,
      profile,
      chiefId,
      workerId,
      ticketId,
      finalStatus: afterBlocked.status,
      description: afterBlocked.description,
      activityCount: afterBlocked.activity.length,
      artifacts: afterDelete.artifacts,
      statusOptions: afterBlocked.statusOptions,
      crashedRejected,
      screenshot: screenshotCaptured ? screenshotPath : null,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Ticket lifecycle scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (workerId) {
      await client.request('close_session', { sessionId: workerId }).catch(() => {});
    }
    if (chiefId) {
      await client.request('close_session', { sessionId: chiefId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
