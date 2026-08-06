#!/usr/bin/env node

/**
 * Real-app scenario: a conversation session, end to end.
 *
 * A `pi-host` session has no PTY. Its agent runs in a headless host process the
 * daemon spawns as a process-group leader, and everything the user sees comes
 * from that host's envelope stream. This scenario proves the whole chain in the
 * packaged app:
 *
 *   1. create a `pi-host` session and wait for its composer to open
 *      (`session_ready` reached the app),
 *   2. type a prompt into the real composer and click Send,
 *   3. assert the reply streams into the pane and the run settles (the send
 *      button goes back from Steer to Send),
 *   4. send a SECOND prompt after settle and assert its own reply,
 *   5. start a long-running tool subprocess, close the session while it is
 *      live, and assert the host's group AND that subprocess are gone. pi
 *      detaches each tool child into its own process group, so the group kill
 *      alone would never reach it: what does is the cooperative SIGTERM the
 *      daemon sends first, which pi's dispose turns into tool teardown. The
 *      receipted bug is that a hard kill skips exactly that.
 *
 * Prereqs: a non-production profile install with the attn-pi plugin installed
 * (`attn plugin install-bundled attn-pi`) and pi credentials in ~/.pi.
 */
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const INPUT = '[data-testid="conversation-input"]';
const SEND = '[data-testid="conversation-send"]';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

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

// Every live process, as (pid, pgid, command). Read-only: the scenario never
// kills anything by pattern — it asks the app to close the session and then
// checks what the daemon left behind.
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

function hostProcesses() {
  return processTable().filter((entry) => entry.command.includes('attn-pi-host'));
}

async function sendPrompt(client, sessionId, text) {
  await client.request('dom_type', { selector: INPUT, text });
  await client.request('dom_click', { selector: SEND });
}

async function waitForReply(client, sessionId, expected, description) {
  // The run has to close, not just produce text: a reply asserted on its own
  // would pass on a host that streamed and then wedged. The composer stays open
  // for the whole run — Enter is a steer while the agent works — so the signal
  // that the run ended is the send button going back to a plain send.
  return pollFor(
    async () => {
      const state = await conversationState(client, sessionId);
      if (!state || state.sendLabel !== 'Send') return null;
      const reply = state.messages.find(
        (message) => message.role === 'assistant' && message.text.toLowerCase().includes(expected),
      );
      return reply && !reply.streaming ? state : null;
    },
    `${description}: an assistant reply containing "${expected}" with the run settled`,
    120_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-pi-host-conversation.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the pi-host scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-CONVERSATION',
    tier: 'tier2-local-real-agent',
    prefix: 'pi-host-conversation',
    metadata: { agent: 'pi-host', focus: 'conversation round trip, second prompt, no orphans on close' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;
  let hostPid = null;
  let hostGroup = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'pi-host-repo');
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

    await runner.step('create_conversation_session', async () => {
      const before = hostProcesses().map((entry) => entry.pid);
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `pi-host-${runner.runId.slice(-6)}`,
        agent: 'pi-host',
      });
      sessionId = created.sessionId;
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      runner.registerCleanup('close_session', () => client.request('close_session', { sessionId }).catch(() => {}));

      const state = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.inputDisabled === false ? current : null;
        },
        'the conversation composer to open (session_ready)',
        90_000,
      );
      const host = hostProcesses().find((entry) => !before.includes(entry.pid));
      if (!host) throw new Error('no attn-pi-host process appeared for the session');
      hostPid = host.pid;
      hostGroup = host.pgid;
      note('host is up and the composer is open', { hostPid: host.pid, hostPgid: hostGroup, placeholder: state.placeholder });
    });

    await runner.step('first_prompt_round_trip', async () => {
      await sendPrompt(client, sessionId, 'Reply with exactly one word: alpha');
      const state = await waitForReply(client, sessionId, 'alpha', 'first prompt');
      note('first reply streamed and settled', { messages: state.messages.length });
    });

    await runner.step('second_prompt_after_settle', async () => {
      await sendPrompt(client, sessionId, 'Reply with exactly one word: bravo');
      const state = await waitForReply(client, sessionId, 'bravo', 'second prompt');
      const roles = state.messages.map((message) => message.role);
      if (state.messages.filter((message) => message.role === 'assistant').length < 2) {
        throw new Error(`expected two assistant replies, got roles ${JSON.stringify(roles)}`);
      }
      fs.writeFileSync(
        path.join(runner.runDir, 'conversation.json'),
        `${JSON.stringify(state, null, 2)}\n`,
        'utf8',
      );
      note('second reply streamed and settled', { messages: state.messages.length });
    });

    await runner.step('close_session_leaves_no_orphans', async () => {
      // Close the session with a tool subprocess live. That is the receipted
      // bug: hard-killing the host strands what pi started, and a group whose
      // only member is the host would prove nothing about it. pi's bash tool
      // blocks for the whole sleep, so the run is still open at close.
      await sendPrompt(client, sessionId, 'Run the bash command `sleep 45`, then say done.');
      const toolChildren = await pollFor(
        async () => {
          // pi puts each tool subprocess in its OWN process group, so the
          // host's group does not contain them. Parentage is what finds them,
          // and it is what makes this assertion about the bug rather than
          // about the group kill.
          const children = processTable().filter((entry) => entry.ppid === hostPid);
          return children.length > 0 ? children : null;
        },
        `a tool subprocess to appear under host pid ${hostPid}`,
        60_000,
      );

      const groupBefore = processTable().filter((entry) => entry.pgid === hostGroup);
      await client.request('close_session', { sessionId });
      // A survivor is matched on pid AND command: a pid the kernel handed to
      // something else in the meantime is not the process we started.
      const stillRunning = () => {
        const live = new Map(processTable().map((entry) => [entry.pid, entry.command]));
        return [
          ...processTable().filter((entry) => entry.pgid === hostGroup),
          ...toolChildren.filter((child) => live.get(child.pid) === child.command),
        ];
      };
      const survivors = await pollFor(
        async () => (stillRunning().length === 0 ? [] : null),
        `the host group ${hostGroup} and its tool subprocesses to exit`,
        30_000,
      ).catch(() => stillRunning());
      fs.writeFileSync(
        path.join(runner.runDir, 'host-process-group.json'),
        `${JSON.stringify({ pgid: hostGroup, hostPid, before: groupBefore, toolChildren, survivors }, null, 2)}\n`,
        'utf8',
      );
      if (survivors.length > 0) {
        throw new Error(`closing the session left ${JSON.stringify(survivors)} behind`);
      }
      note('host and its tool subprocesses are gone after close', {
        pgid: hostGroup,
        hostPid,
        toolChildren: toolChildren.length,
      });
    });

    await runner.finishSuccess({ sessionId, hostPid, hostPgid: hostGroup });
  } catch (error) {
    await runner.finishFailure(error, { sessionId, hostPid, hostPgid: hostGroup });
    throw error;
  } finally {
    // The app and the observer socket outlive the assertions, and an open
    // socket holds node's event loop open — without this the scenario prints
    // its verdict and then never exits, leaving the app running for whoever
    // runs next.
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
