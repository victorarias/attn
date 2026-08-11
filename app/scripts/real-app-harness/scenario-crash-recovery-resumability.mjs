#!/usr/bin/env node

// A machine crash, reproduced: every pty worker and the daemon are SIGKILLed
// at once, so nothing gets to say goodbye and no worker can be re-adopted on
// the way back. What must survive is every session attn can still bring back —
// whatever agent it is and whatever it was last seen doing — and what must not
// is a session with nothing left to reopen.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  relaunchAppAndConnect,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeInitialPanePromptReady,
  ensureCodexInitialPanePromptReady,
} from './scenarioAgents.mjs';
import { currentHarnessProfile, dataDirForProfile } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

// The resume id is the evidence under test, so the scenario reads it where the
// daemon writes it rather than inferring it from the screen.
async function readPersistedResumeId(dataDir, sessionId) {
  const dbPath = path.join(dataDir, 'attn.db');
  const { stdout } = await execFileAsync('sqlite3', [
    `file:${dbPath}?mode=ro`,
    `select coalesce(resume_session_id, '') from sessions where id = '${sessionId}';`,
  ]);
  return stdout.trim();
}

// The rollout `codex resume <id>` would reopen. Located the way the daemon's
// driver locates it: the first line's session_meta names the id.
function findCodexRollout(resumeId) {
  const root = path.join(process.env.CODEX_HOME || path.join(os.homedir(), '.codex'), 'sessions');
  const stack = [root];
  while (stack.length > 0) {
    const dir = stack.pop();
    let entries = [];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        stack.push(full);
        continue;
      }
      if (!entry.name.endsWith('.jsonl')) continue;
      let head = '';
      try {
        head = fs.readFileSync(full, 'utf8').split('\n', 1)[0] || '';
      } catch {
        continue;
      }
      if (head.includes(`"id":"${resumeId}"`)) return full;
    }
  }
  return null;
}

async function waitFor(description, predicate, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await sleep(500);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

// A crash leaves no orderly shutdown behind: SIGKILL every worker this
// profile's registry knows about, then the daemon itself. The registry is what
// makes this precise — only PIDs attn recorded for this profile are signalled.
function killProfileRuntimeLikeACrash(dataDir, log) {
  const killed = { workers: [], daemon: null };
  const workersRoot = path.join(dataDir, 'workers');
  const instances = fs.existsSync(workersRoot) ? fs.readdirSync(workersRoot) : [];
  for (const instance of instances) {
    const registryDir = path.join(workersRoot, instance, 'registry');
    if (!fs.existsSync(registryDir)) continue;
    for (const entry of fs.readdirSync(registryDir)) {
      const raw = fs.readFileSync(path.join(registryDir, entry), 'utf8');
      const record = JSON.parse(raw);
      const pid = Number(record.pid ?? record.Pid ?? record.worker_pid);
      if (!Number.isInteger(pid) || pid <= 1) continue;
      try {
        process.kill(pid, 'SIGKILL');
        killed.workers.push({ session: entry, pid });
      } catch (error) {
        log(`worker ${entry} pid ${pid} already gone: ${error.message}`);
      }
    }
  }
  const pidFile = path.join(dataDir, 'attn.pid');
  if (fs.existsSync(pidFile)) {
    const pid = Number(fs.readFileSync(pidFile, 'utf8').trim());
    if (Number.isInteger(pid) && pid > 1) {
      try {
        process.kill(pid, 'SIGKILL');
        killed.daemon = pid;
      } catch (error) {
        log(`daemon pid ${pid} already gone: ${error.message}`);
      }
    }
  }
  return killed;
}

function daemonLogTail(dataDir, lines = 200) {
  const logPath = path.join(dataDir, 'daemon.log');
  if (!fs.existsSync(logPath)) return '';
  return fs.readFileSync(logPath, 'utf8').split('\n').slice(-lines).join('\n');
}

async function submitCodexPrompt(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
  // Codex reads a fast character stream as a paste and makes the next Enter a
  // newline; wait past that window so this is a real submit.
  await sleep(250);
  await client.request('type_pane_via_ui', { sessionId, paneId, text: '\n' });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-crash-recovery-resumability.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'CRASH-REC',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-crash-recovery-resumability',
    metadata: { agents: 'codex+claude+shell', focus: 'crash keeps what it can bring back' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const token = `CRASHREC${Date.now()}`;
  let codexSessionId = null;
  let codexPaneId = null;
  let codexResumeId = null;
  let claudeSessionId = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // Codex, with a conversation behind it: a rollout on disk and the native
    // resume id attn persisted from it. This is the session the crash used to
    // delete.
    codexSessionId = await runner.step('create_codex_session_with_a_conversation', async () => {
      const sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `crashrec-codex-${runner.runId}`,
        agent: 'codex',
        promptReadyFn: ensureCodexInitialPanePromptReady,
      });
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'codex pane', 20_000);
      codexPaneId = pane.paneId;
      // Codex reports its native rollout id at session start; until the daemon
      // has persisted it there is no resume target and the scenario would be
      // testing the wrong thing.
      codexResumeId = await waitFor(
        'codex to report its native resume id',
        async () => (await readPersistedResumeId(dataDir, sessionId)) || null,
        90_000,
      );
      await submitCodexPrompt(client, sessionId, pane.paneId, `Reply with exactly ${token} and nothing else.`);
      // The rollout is the conversation. Waiting for the token to land in it is
      // what makes "the crash did not lose the conversation" a real claim: the
      // pane would show the token from the local echo alone.
      const rollout = await waitFor(
        'the token to reach the codex rollout on disk',
        () => {
          const file = findCodexRollout(codexResumeId);
          if (!file) return null;
          return fs.readFileSync(file, 'utf8').includes(token) ? file : null;
        },
        120_000,
      );
      runner.writeJson('codex-conversation.json', { resumeId: codexResumeId, rollout });
      return sessionId;
    });

    // Claude, booted to its prompt and never asked anything: it writes its
    // transcript on the first turn, so there is nothing on disk to resume and
    // nothing to keep.
    claudeSessionId = await runner.step('create_claude_session_that_never_took_a_turn', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `crashrec-claude-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeInitialPanePromptReady,
      });
    });

    await runner.step('record_state_before_the_crash', async () => {
      const claudeResumeId = await readPersistedResumeId(dataDir, claudeSessionId);
      const claudeTranscript = path.join(
        process.env.ATTN_TOOL_HOME || os.homedir(),
        '.claude',
        'projects',
      );
      const claudeHasTranscript = fs.existsSync(claudeTranscript)
        && fs.readdirSync(claudeTranscript).some((dir) => fs.existsSync(path.join(claudeTranscript, dir, `${claudeResumeId}.jsonl`)));
      if (claudeHasTranscript) {
        throw new Error('the claude session already has a transcript; it cannot stand in for the unresumable case');
      }
      runner.writeJson('before-crash.json', {
        codex: { session: observer.getSession(codexSessionId), resumeId: codexResumeId },
        claude: { session: observer.getSession(claudeSessionId), resumeId: claudeResumeId, hasTranscript: false },
      });
    });

    const killed = await runner.step('crash_the_machine', async () => {
      const result = killProfileRuntimeLikeACrash(dataDir, (m) => runner.log(m));
      runner.writeJson('killed.json', result);
      if (result.workers.length === 0) {
        throw new Error('no pty workers were killed; the crash was not reproduced');
      }
      // Let the OS reap them before anything tries to adopt them.
      await sleep(1_000);
      return result;
    });
    runner.log(`crashed: ${killed.workers.length} workers, daemon pid ${killed.daemon}`);

    await runner.step('reboot_into_the_app', async () => {
      await relaunchAppAndConnect(client, observer);
      // Startup recovery runs before clients cross the barrier, but the
      // observer's first snapshot can beat the deferred pass; give it a beat.
      await sleep(2_000);
      runner.writeText('daemon-after-reboot.log', daemonLogTail(dataDir));
    });

    await runner.step('assert_the_resumable_codex_session_survived', async () => {
      const session = await waitFor(
        'the codex session to settle on a recovery verdict',
        async () => {
          const current = observer.getSession(codexSessionId);
          return current && current.state === 'recoverable' ? current : null;
        },
        30_000,
      ).catch(() => observer.getSession(codexSessionId));
      if (!session) {
        throw new Error('the codex session was deleted by startup recovery despite having a rollout to resume');
      }
      if (session.state !== 'recoverable') {
        throw new Error(`codex session state = ${session.state}, want recoverable`);
      }
      const recovery = daemonLogTail(dataDir, 400)
        .split('\n')
        .filter((line) => line.includes('worker session reconciliation summary'))
        .pop();
      runner.writeJson('after-crash-codex.json', { session, recovery });
      if (!recovery || !/marked_recoverable=[1-9]/.test(recovery)) {
        throw new Error(`reconciliation summary did not mark anything recoverable: ${recovery}`);
      }
    });

    await runner.step('assert_the_unresumable_claude_session_was_reaped', async () => {
      const session = observer.getSession(claudeSessionId);
      if (session) {
        throw new Error(`claude session survived as ${session.state}; it has no transcript to resume`);
      }
      // Reaped means the pane goes with the row: no Reload is offered for a
      // session that could not take one.
      const workspace = observer.getWorkspace(claudeSessionId);
      if (workspace) {
        throw new Error('the reaped claude session left its workspace pane behind');
      }
    });

    await runner.step('revive_the_codex_pane_and_read_the_old_conversation_back', async () => {
      await client.request('select_session', { sessionId: codexSessionId });
      const pane = await waitForFirstWorkspacePane(client, codexSessionId, 'revived codex pane', 30_000);
      await waitForPaneVisible(client, codexSessionId, pane.paneId, 30_000);
      await waitForPaneText(
        client,
        codexSessionId,
        pane.paneId,
        (text) => text.includes(token),
        'the resumed codex conversation still carries what was said before the crash',
        120_000,
      );
      const revived = await client.request('read_pane_text', { sessionId: codexSessionId, paneId: pane.paneId }, { timeoutMs: 20_000 });
      runner.writeText('revived-pane.txt', revived?.text || '');
      await captureSessionArtifacts(client, runner.runDir, 'revived-codex', codexSessionId);
      const session = observer.getSession(codexSessionId);
      if (!session || session.state === 'recoverable') {
        throw new Error(`codex session state after revive = ${session?.state}; want a live state`);
      }
    });

    const summary = runner.finishSuccess({
      codexSessionId,
      claudeSessionId,
      token,
      artifacts: { runDir: runner.runDir, trace: runner.tracePath },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (codexSessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', codexSessionId).catch(() => {});
    }
    runner.writeText('daemon-on-failure.log', daemonLogTail(dataDir, 400));
    const summary = runner.finishFailure(error, { codexSessionId, claudeSessionId, token });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [codexSessionId, claudeSessionId]) {
      if (sessionId) {
        await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
