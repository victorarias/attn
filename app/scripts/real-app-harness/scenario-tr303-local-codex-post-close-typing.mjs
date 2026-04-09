#!/usr/bin/env node

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneTextChange,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { ensureCodexMainPromptReady } from './scenarioAgents.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
  };

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  return {
    options,
    help: Boolean(options.help),
  };
}

function summarizeTraceEvents(events) {
  return events.map((event) => ({
    at: event.at,
    event: event.event,
    paneId: event.paneId ?? null,
    runtimeId: event.runtimeId ?? null,
    details: event.details ?? null,
  }));
}

function interestingPostTypingEvents(events) {
  return events.filter((event) => {
    const name = typeof event?.event === 'string' ? event.event : '';
    return (
      name === 'terminal.mounted'
      || name === 'pty.redraw.requested'
      || name.startsWith('pty.attach')
      || name.startsWith('pty.geometry')
      || name === 'pty.output.live'
    );
  });
}

function withTimeout(promise, timeoutMs) {
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      setTimeout(() => reject(new Error(`Timed out after ${timeoutMs}ms`)), timeoutMs);
    }),
  ]);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr303-local-codex-post-close-typing.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-303',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr303-local-codex-post-close-typing',
    metadata: {
      agent: 'codex',
      focus: 'typing after split-close should not remount or redraw',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let splitPaneId = null;
  const token = `TR303TYPE${Date.now()}`;

  try {
    await runner.step('launch_app', async () => {
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      await observer.connect();
    });

    sessionId = await runner.step('create_local_session', async () => {
      const result = await client.request('create_session', {
        cwd: runner.sessionDir,
        label: `tr303-local-codex-${runner.runId}`,
        agent: 'codex',
      });
      await observer.waitForSession({ id: result.sessionId, timeoutMs: 30_000 });
      return result.sessionId;
    });

    await runner.step('prepare_main_prompt', async () => {
      await client.request('select_session', { sessionId });
      await ensureCodexMainPromptReady(client, sessionId, 45_000);
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    splitPaneId = await runner.step('split_and_close', async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: 'main',
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(client, sessionId, existingPaneIds, 'new shell pane after split', 30_000);
      await client.request('close_pane', { sessionId, paneId: newPane.paneId });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => (workspace.panes || []).length === 1,
        'workspace collapse after split close',
        20_000,
      );
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
      await captureSessionArtifacts(client, runner.runDir, '02-after-close', sessionId);
      return newPane.paneId;
    });

    await runner.step('type_and_assert_no_redraw', async () => {
      await client.request('select_session', { sessionId });
      await client.request('click_pane', { sessionId, paneId: 'main' });
      await waitForPaneInputFocus(client, sessionId, 'main', 15_000);

      const beforeTrace = await client.request('dump_terminal_runtime_trace', {}, { timeoutMs: 10_000 });
      const beforeCount = Array.isArray(beforeTrace?.events) ? beforeTrace.events.length : 0;
      const beforeTextPayload = await client.request('read_pane_text', { sessionId, paneId: 'main' }, { timeoutMs: 20_000 });
      const beforeText = beforeTextPayload?.text || '';

      await client.request('type_pane_via_ui', { sessionId, paneId: 'main', text: token });
      await waitForPaneTextChange(
        client,
        sessionId,
        'main',
        beforeText,
        'local codex pane text change after typing post-close token',
        15_000,
      );

      const afterTrace = await client.request('dump_terminal_runtime_trace', {}, { timeoutMs: 10_000 });
      const delta = Array.isArray(afterTrace?.events) ? afterTrace.events.slice(beforeCount) : [];
      const interesting = interestingPostTypingEvents(delta);
      const forbidden = interesting.filter((event) => {
        const name = event.event || '';
        return (
          name === 'terminal.mounted'
          || name === 'pty.redraw.requested'
          || name.startsWith('pty.attach')
          || name.startsWith('pty.geometry')
        );
      });
      const liveOutputs = interesting.filter((event) => event.event === 'pty.output.live');

      runner.writeJson('03-post-close-typing-trace-delta.json', {
        token,
        splitPaneId,
        interestingEvents: summarizeTraceEvents(interesting),
        forbiddenEvents: summarizeTraceEvents(forbidden),
      });

      runner.assert(liveOutputs.length > 0, 'typing after split-close produces live PTY output', {
        count: liveOutputs.length,
      });
      runner.assert(forbidden.length === 0, 'typing after split-close does not trigger remount, attach, redraw, or geometry events', {
        events: summarizeTraceEvents(forbidden),
      });

      await captureSessionArtifacts(client, runner.runDir, '03-post-close-typing', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      splitPaneId,
      token,
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, {
      sessionId,
      splitPaneId,
      token,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    try {
      await withTimeout(cleanupSessionViaAppClose(client, observer, sessionId), 15_000);
    } catch {}
    try {
      await withTimeout(client.quitApp(), 5_000);
    } catch {}
    try {
      await withTimeout(observer.disconnect(), 5_000);
    } catch {}
  }
}

main()
  .then(() => {
    process.exit(process.exitCode ?? 0);
  })
  .catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
