#!/usr/bin/env node

/**
 * Real-app scenario: a conversation session dying and coming back.
 *
 * A `pi-host` session's whole history lives in pi's session file under attn's
 * data dir, so a host that died left behind everything a replacement needs.
 * This scenario proves that end to end, in the packaged app, against a real
 * agent:
 *
 *   1. hold a conversation, then `kill -9` the host mid-tool-run — the crash
 *      attn never gets to ask nicely about,
 *   2. the session parks as `recoverable`, with its transcript still readable
 *      and the way back in the pane,
 *   3. Reload brings the conversation back: the same transcript, and the agent
 *      answering a question only the reopened session file can answer,
 *   4. the dead host's process group is gone and the durable spawn record names
 *      the live replacement, not the corpse,
 *   5. a client that never saw the stream draws the whole conversation — the
 *      app is restarted and the pane refills from the host's snapshot alone,
 *   6. a session killed before its first assistant message (so no session file
 *      exists yet) relaunches into a working session rather than a dead one.
 *
 * Each kill targets a pid captured when that host appeared, never a pattern.
 *
 * Known residual, recorded rather than asserted: `kill -9` skips the
 * cooperative teardown pi needs to stop its tool subprocesses, and pi detaches
 * each one into its own process group, so the group kill cannot reach them
 * either. That is the slice-1 receipt, unchanged by revive; the scenario writes
 * what it found to `stranded-tool-children.json` and reaps it before exiting.
 *
 * Prereqs: a non-production profile install with the attn-pi plugin installed
 * (`attn plugin install-bundled attn-pi`) and pi credentials in ~/.pi.
 */
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  relaunchAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile } from './harnessProfile.mjs';

// A word the agent can only produce by having read the exchange that happened
// before the crash. It is what separates a revived conversation from a fresh
// one drawn under an old transcript.
const SECRET = 'marmalade';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Two conversation panes are mounted at once in the later steps, so every
// selector is scoped to the session it belongs to.
const pane = (sessionId) => `[data-testid="conversation-pane-${sessionId}"]`;
const inputOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-input"]`;
const sendOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-send"]`;
const reloadOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-reload"]`;

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

function conversationState(client, sessionId) {
  return client.request('conversation_get_state', { sessionId }, { timeoutMs: 20_000 }).catch(() => null);
}

// Every live process, as (pid, ppid, pgid, command). Read-only: the only kills
// this scenario performs target pids it captured when a host appeared.
function processTable() {
  const stdout = execFileSync('/bin/ps', ['-eo', 'pid=,ppid=,pgid=,command='], { encoding: 'utf8' });
  return stdout
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const match = /^(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/.exec(line);
      return match
        ? { pid: Number(match[1]), ppid: Number(match[2]), pgid: Number(match[3]), command: match[4] }
        : null;
    })
    .filter(Boolean);
}

const hostProcesses = () => processTable().filter((entry) => entry.command.includes('attn-pi-host'));

/** The host that appeared for this session, once one has. */
function waitForHost(known, description) {
  return pollFor(
    async () => hostProcesses().find((entry) => !known.includes(entry.pid)) ?? null,
    description,
    90_000,
  );
}

/**
 * Which of the recorded processes are still the ones we recorded: a pid the
 * kernel has since handed to something else is not our process, so pid AND
 * command have to match.
 */
function stillAlive(recorded) {
  const live = new Map(processTable().map((entry) => [entry.pid, entry.command]));
  return recorded.filter((entry) => live.get(entry.pid) === entry.command);
}

/** The durable spawn record the daemon keeps for a session's host (procreap). */
function hostRegistryEntry(dataDir, sessionId) {
  const file = path.join(dataDir, 'hosts', 'registry', `${sessionId}.json`);
  if (!fs.existsSync(file)) return null;
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    return null;
  }
}

async function sendPrompt(client, sessionId, text) {
  await client.request('dom_type', { selector: inputOf(sessionId), text });
  await client.request('dom_click', { selector: sendOf(sessionId) });
}

/** The pane state once the run answering with `word` has settled. */
function settledWith(client, sessionId, word, description, timeoutMs = 180_000) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      if (!current || current.sendLabel !== 'Send') return null;
      const reply = (current.messages || []).find(
        (message) => message.role === 'assistant' && !message.streaming
          && message.text.toLowerCase().includes(word),
      );
      return reply ? current : null;
    },
    description,
    timeoutMs,
  );
}

function composerOpen(client, sessionId, description, timeoutMs = 120_000) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      return current && current.inputDisabled === false ? current : null;
    },
    description,
    timeoutMs,
  );
}

function parkedRecoverable(client, sessionId, description, timeoutMs = 90_000) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      return current && current.recoverable ? current : null;
    },
    description,
    timeoutMs,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-pi-host-revive.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the pi-host revive scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const dataDir = dataDirForProfile(profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-REVIVE',
    tier: 'tier2-local-real-agent',
    prefix: 'pi-host-revive',
    metadata: {
      agent: 'pi-host',
      focus: 'crash to recoverable, reload with history, dead host reaped, snapshot on a cold client, zero-file relaunch',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;
  let earlySessionId = null;
  let deadHost = null;
  let revivedHost = null;
  let toolChildren = [];

  // The hard kill is the point of this scenario, so reaping what the hard kill
  // strands is also this scenario's job.
  const reapStrandedToolChildren = () => {
    const stranded = stillAlive(toolChildren);
    for (const child of stranded) {
      try {
        process.kill(child.pid, 'SIGKILL');
      } catch {
        // Gone between the check and the kill: nothing to reap.
      }
    }
    return stranded;
  };

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('reap_stranded_tool_children', reapStrandedToolChildren);

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'pi-host-revive-repo');
      fs.mkdirSync(dir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: dir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: dir,
        env: {
          ...process.env,
          GIT_AUTHOR_NAME: 'attn',
          GIT_AUTHOR_EMAIL: 'attn@local',
          GIT_COMMITTER_NAME: 'attn',
          GIT_COMMITTER_EMAIL: 'attn@local',
        },
      });
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('hold_a_conversation', async () => {
      const before = hostProcesses().map((entry) => entry.pid);
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `pi-host-revive-${runner.runId.slice(-6)}`,
        agent: 'pi-host',
      });
      sessionId = created.sessionId;
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      await composerOpen(client, sessionId, 'the conversation composer to open (session_ready)');
      deadHost = await waitForHost(before, 'the host process for the session');

      await sendPrompt(client, sessionId, `Remember this word: ${SECRET}. Reply with exactly one word: alpha`);
      const settled = await settledWith(client, sessionId, 'alpha', 'the first run to settle');
      note('the conversation has something worth surviving', {
        hostPid: deadHost.pid,
        hostPgid: deadHost.pgid,
        messages: settled.messages.length,
      });
    });

    await runner.step('kill_9_mid_tool_run_parks_the_session', async () => {
      await sendPrompt(client, sessionId, 'Run the bash command `sleep 45`, then say done.');
      // Kill while a tool is genuinely running, not merely while a run is open:
      // the live child is what makes this the worst case rather than a tidy one.
      toolChildren = await pollFor(
        async () => {
          const children = processTable().filter((entry) => entry.ppid === deadHost.pid);
          return children.length > 0 ? children : null;
        },
        `a tool subprocess under host pid ${deadHost.pid}`,
        90_000,
      );
      // The pid captured when this session's host appeared. Never a match on a
      // name, a path, or a worktree string.
      process.kill(deadHost.pid, 'SIGKILL');

      const state = await parkedRecoverable(client, sessionId, 'the pane to report the session as recoverable');
      if (!state.reloadAvailable) {
        throw new Error('a conversation that died offered no way back');
      }
      if (!state.inputDisabled) {
        throw new Error('the composer stayed open over a host that is gone');
      }
      // The transcript is not what died. It has to stay readable while the
      // session waits to be reloaded.
      if (!(state.messages || []).some((message) => message.text.includes(SECRET))) {
        throw new Error(`the transcript was lost with the host: ${JSON.stringify(state.messages)}`);
      }
      note('a hard-killed host parked the session as recoverable with its transcript intact', {
        killedPid: deadHost.pid,
        toolChildren: toolChildren.length,
      });
    });

    await runner.step('reload_resumes_with_history', async () => {
      const before = hostProcesses().map((entry) => entry.pid);
      await client.request('dom_click', { selector: reloadOf(sessionId) });
      revivedHost = await waitForHost(before, 'a replacement host after the reload');
      const reopened = await composerOpen(client, sessionId, 'the revived conversation to accept prompts');
      if (!(reopened.messages || []).some((message) => message.text.includes(SECRET))) {
        throw new Error(`the revived conversation lost its history: ${JSON.stringify(reopened.messages)}`);
      }

      // The load-bearing assertion: the AGENT has the history, not just the
      // pane. Only a reopened session file can answer this.
      await sendPrompt(client, sessionId, 'What word did I ask you to remember? Reply with only that word.');
      const settled = await settledWith(client, sessionId, SECRET, 'the revived agent to answer from what came before the crash');
      runner.writeJson('revived-conversation.json', settled);
      note('the revived agent answered from the conversation that survived the crash', {
        deadPid: deadHost.pid,
        revivedPid: revivedHost.pid,
        messages: settled.messages.length,
      });
    });

    await runner.step('the_dead_host_is_gone_and_the_record_names_the_live_one', async () => {
      const groupSurvivors = processTable().filter((entry) => entry.pgid === deadHost.pgid);
      // The durable spawn record is the only way a stranded host could ever be
      // found again. After a revive it must name the live replacement: a record
      // still pointing at the corpse would make cleanup chase a dead pid and
      // leave the live host running.
      const record = hostRegistryEntry(dataDir, sessionId);
      runner.writeJson('host-records.json', { deadHost, revivedHost, groupSurvivors, record });
      if (groupSurvivors.length > 0) {
        throw new Error(`the killed host's group ${deadHost.pgid} still has ${JSON.stringify(groupSurvivors)}`);
      }
      if (!record) {
        throw new Error('the revived host has no durable spawn record; nothing could ever reap it');
      }
      if (record.pid !== revivedHost.pid) {
        throw new Error(`the host registry names pid ${record.pid}, but the live host is ${revivedHost.pid}`);
      }

      // Recorded, not asserted: SIGKILL skips the cooperative teardown that
      // stops pi's tool subprocesses, and pi detaches each into its own process
      // group, so the group kill cannot reach them either. This is the slice-1
      // receipt; revive neither improves nor worsens it, and the numbers are
      // written down so the next slice can check whether that is still true.
      runner.writeJson('stranded-tool-children.json', {
        note: 'a hard kill strands pi tool subprocesses by design (slice-1 receipt); revive does not change that',
        started: toolChildren,
        stranded: stillAlive(toolChildren),
      });
      note('the dead host is gone and the registry names the live one', {
        pgid: deadHost.pgid,
        registryPid: record.pid,
        strandedToolChildren: stillAlive(toolChildren).length,
      });
    });

    await runner.step('a_cold_client_draws_the_whole_conversation', async () => {
      // Restart the app: the new window never saw this host's stream, so
      // everything it draws came from the snapshot it asked for on mount. It is
      // what a second client sees, assertable without driving two of them.
      await relaunchAppAndConnect(client, observer);
      await client.request('select_session', { sessionId });

      const cold = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && (current.messages || []).length > 0 ? current : null;
        },
        'the restarted app to draw the conversation from a snapshot',
        90_000,
      );
      if (!(cold.messages || []).some((message) => message.text.includes(SECRET))) {
        throw new Error(`a client that was not watching drew an incomplete conversation: ${JSON.stringify(cold.messages)}`);
      }
      if (cold.inputDisabled) {
        throw new Error('the snapshot drew a conversation the user cannot talk to');
      }
      runner.writeJson('cold-client.json', cold);
      note('a client with no history of the stream drew the whole conversation', {
        messages: cold.messages.length,
        tools: (cold.tools || []).length,
      });
    });

    await runner.step('killed_before_its_first_word_relaunches_clean', async () => {
      // No session file exists until pi's first assistant message, so a host
      // killed this early leaves nothing to reopen. The relaunch has to make a
      // fresh session rather than fail on a file that is not there.
      const before = hostProcesses().map((entry) => entry.pid);
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `pi-host-early-${runner.runId.slice(-6)}`,
        agent: 'pi-host',
      });
      earlySessionId = created.sessionId;
      await observer.waitForSession({ id: earlySessionId, timeoutMs: 30_000 });
      await client.request('select_session', { sessionId: earlySessionId });
      await composerOpen(client, earlySessionId, 'the early session composer to open');
      const host = await waitForHost(before, 'the host for the early session');

      // Where the host points pi's SessionManager (ATTN_PI_HOST_SESSION_DIR).
      // Empty here is the condition this step exists for.
      const sessionsDir = path.join(dataDir, 'hosts', 'state', earlySessionId);
      const filesAtKill = fs.existsSync(sessionsDir) ? fs.readdirSync(sessionsDir) : [];
      process.kill(host.pid, 'SIGKILL');
      await parkedRecoverable(client, earlySessionId, 'the early session to park as recoverable');

      const relaunched = hostProcesses().map((entry) => entry.pid);
      await client.request('dom_click', { selector: reloadOf(earlySessionId) });
      await waitForHost(relaunched, 'a replacement host for the early session');
      await composerOpen(client, earlySessionId, 'the relaunched early session to accept prompts');
      await sendPrompt(client, earlySessionId, 'Reply with exactly one word: bravo');
      const settled = await settledWith(client, earlySessionId, 'bravo', 'the relaunched early session to answer');
      runner.writeJson('early-relaunch.json', { sessionsDir, filesAtKill, state: settled });
      note('a session killed before its first assistant message relaunched into a working one', {
        sessionFilesAtKill: filesAtKill.length,
        messages: settled.messages.length,
      });
    });

    await runner.finishSuccess({ sessionId, earlySessionId, deadHost, revivedHost });
  } catch (error) {
    await runner.finishFailure(error, { sessionId, earlySessionId, deadHost, revivedHost });
    throw error;
  } finally {
    for (const id of [sessionId, earlySessionId].filter(Boolean)) {
      await client.request('close_session', { sessionId: id }).catch(() => {});
    }
    reapStrandedToolChildren();
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
