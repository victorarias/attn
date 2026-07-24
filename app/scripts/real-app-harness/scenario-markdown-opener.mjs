#!/usr/bin/env node

// The global markdown opener (⌘P) in the packaged app, end to end:
//
//   native ⌘P -> palette opens on recents (empty at first)
//   -> typing filters the session's markdown via git-backed fs_index
//   -> picking docks a markdown tile bound to the session
//   -> a gitignored file stays invisible to fuzzy mode
//   -> re-summoning with an empty query now lists the opened files as recents,
//      most recent first, and picking one reuses its tile
//   -> a file an agent wrote, reported by the tool-use hook, appears in recents
//      without ever having been opened
//   -> typing an absolute path switches to path mode: directories descend, and
//      the gitignored file fuzzy mode could not see opens from there.
//
// Everything here is out of reach of the browser e2e: the native keystroke, the
// real daemon's file-activity table, and git enumeration over a real repository.

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver, delay } from './macosDriver.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { profileForAppPath, socketPathForProfile } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const OPENER_INPUT = '.markdown-opener-input';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

// The opener's rendered rows, straight from the DOM — assertions read what the
// palette is showing rather than inferring it from a screenshot.
async function openerState(client) {
  return client.request('markdown_opener_get_state', {});
}

async function waitForOpener(client, predicate, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await openerState(client);
    if (predicate(last)) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last opener state:\n${JSON.stringify(last, null, 2)}`);
}

async function waitForWorkspaceUi(client, workspaceId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_workspace_ui_state', { workspaceId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) return last;
    await delay(200);
  }
  throw new Error(`Timed out waiting for ${description}. Last workspace UI state:\n${JSON.stringify(last, null, 2)}`);
}

function markdownTileIds(state) {
  return (state?.tileIds || []).filter((id) => id.startsWith('tile-markdown'));
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) return;
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await delay(200);
  }
}

// A real git repository: fuzzy mode enumerates through `git ls-files`, so the
// scenario must exercise that path (and its .gitignore behavior), not the
// WalkDir fallback.
function seedRepo(cwd, alpha, beta, ignored) {
  fs.mkdirSync(path.join(cwd, 'docs'), { recursive: true });
  fs.mkdirSync(path.join(cwd, 'build'), { recursive: true });
  fs.writeFileSync(path.join(cwd, '.gitignore'), 'build/\n', 'utf8');
  fs.writeFileSync(path.join(cwd, 'docs', alpha), '# Alpha plan\n', 'utf8');
  fs.writeFileSync(path.join(cwd, 'docs', beta), '# Beta notes\n', 'utf8');
  fs.writeFileSync(path.join(cwd, 'build', ignored), '# Generated\n', 'utf8');
  const git = (...args) => execFileSync('git', args, { cwd, stdio: 'pipe' });
  git('init');
  git('config', 'user.email', 'harness@example.com');
  git('config', 'user.name', 'Harness');
  git('add', '.gitignore', path.join('docs', alpha));
  git('commit', '-m', 'seed');
  // The beta file stays untracked-but-not-ignored: it must still be findable.
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-markdown-opener.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'MARKDOWN-OPENER',
    tier: 'tier1-local-shell',
    prefix: 'markdown-opener',
    metadata: {
      agent: 'shell',
      focus: 'native Cmd+P opener: fuzzy over git-enumerated markdown, recents, agent edits, then path mode',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // Files are named per run so a re-run against the same profile (whose
    // recents table persists) still proves THIS run's opens landed in recents.
    const alpha = `alpha-plan-${runner.runId}.md`;
    const beta = `beta-notes-${runner.runId}.md`;
    // Gitignored, and named per run for the same reason: a previous run opened
    // its own copy through path mode, so that one legitimately lives in recents.
    const ignored = `ignored-generated-${runner.runId}.md`;
    // Written by the tool-use hook rather than opened. Also gitignored, so the
    // fuzzy index cannot be what surfaces them.
    const claudeEdited = `claude-wrote-${runner.runId}.md`;
    const codexEdited = `codex-wrote-${runner.runId}.md`;

    const { workspaceId, cwd } = await runner.step('create_shell_session', async () => {
      const sessionCwd = path.join(runner.sessionDir, 'opener-ws');
      fs.mkdirSync(sessionCwd, { recursive: true });
      seedRepo(sessionCwd, alpha, beta, ignored);
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: sessionCwd,
        label: `markdown-opener-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      runner.registerCleanup('close_session_panes', () => (sessionId ? closeWorkspacePanes(client, sessionId) : null));
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane');
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell prompt ready',
      });
      const workspace = await client.request('get_workspace', { sessionId });
      if (!workspace.workspaceId) {
        throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
      }
      return { workspaceId: workspace.workspaceId, cwd: sessionCwd };
    });

    // The real ⌘P: the registry binding through the macOS/WebView key path,
    // which browser e2e never exercises.
    const summon = async (description) => {
      await driver.activateApp();
      await driver.pressKey('p', { command: true });
      try {
        await waitForOpener(client, (state) => state.open, description);
      } catch (error) {
        const frontmost = await driver.frontmostBundleId().catch(() => '(unknown)');
        throw new Error(
          `${error.message}\n\nThe native Cmd+P did not reach the app. This scenario needs native `
          + `keyboard input: grant Accessibility permission to the process running it and keep attn `
          + `frontmost. Frontmost app was "${frontmost}" (expected "${driver.bundleId}").`,
        );
      }
    };

    // Pick a specific row by its label. Rows carry stable per-index ids, and a
    // previous run's recents can outrank this run's file, so never assume the
    // wanted row is the highlighted one.
    const pickRow = async (state, endsWith) => {
      const index = state.rows.findIndex((row) => row.path.endsWith(endsWith));
      if (index < 0) {
        throw new Error(`No opener row for ${endsWith}: ${JSON.stringify(state.rows)}`);
      }
      await client.request('dom_click', { selector: `#markdown-opener-opt-${index}` });
    };

    const typeQuery = async (text) => {
      await client.request('dom_type', { selector: OPENER_INPUT, text });
      await delay(400);
    };

    await runner.step('opener_summons', async () => {
      await summon('native Cmd+P opens the markdown opener');
      // The empty query lists recents, never the whole tree: whatever is showing,
      // none of it is this run's freshly created files.
      const state = await openerState(client);
      runner.assert(
        !state.rows.some((row) => row.path.includes(runner.runId)),
        `Empty query must not list files that have never been opened: ${JSON.stringify(state.rows)}`,
      );
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'opener-empty.png'), { client }).catch(() => {});
    });

    await runner.step('gitignored_file_is_invisible', async () => {
      // build/ is gitignored, so git enumeration never reports it.
      await typeQuery('ignoredgenerated');
      const state = await openerState(client);
      // Earlier runs opened their own copy through path mode, so their
      // (absolute-path) rows may match this query; only this run's file proves
      // anything about the index.
      runner.assert(
        !state.rows.some((row) => row.path.endsWith(ignored)),
        `A gitignored markdown file must not appear in fuzzy mode: ${JSON.stringify(state.rows)}`,
      );
    });

    await runner.step('fuzzy_opens_untracked_file', async () => {
      // The untracked-but-not-ignored file must still be findable: git
      // enumeration asks for --others --exclude-standard, not just the index.
      await typeQuery('betanotes');
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(beta)),
        'fuzzy query matches the untracked markdown file',
      );
      // Rows from the fuzzy index are labeled relative to the session root.
      // (Earlier runs' recents can also match — they carry absolute paths
      // because they live outside this run's root — so assert on this run's
      // file rather than on row 0.)
      const betaRow = state.rows.find((row) => row.path.endsWith(beta));
      runner.assert(
        betaRow.path === `docs/${beta}`,
        `Fuzzy rows must be labeled relative to the session root: ${JSON.stringify(state.rows)}`,
      );
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'opener-fuzzy.png'), { client }).catch(() => {});
      await pickRow(state, beta);
      await waitForOpener(client, (current) => !current.open, 'picking a file closes the opener');
      const ui = await waitForWorkspaceUi(
        client,
        workspaceId,
        (state) => markdownTileIds(state).length === 1,
        'picking a file docks its markdown tile',
      );
      runner.log(`[RealAppHarness] docked ${markdownTileIds(ui)[0]} for ${beta}`);
    });

    await runner.step('fuzzy_opens_tracked_file', async () => {
      await summon('re-summon for the tracked file');
      await typeQuery('alphaplan');
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(alpha)),
        'fuzzy query matches the tracked markdown file',
      );
      await pickRow(state, alpha);
      await waitForWorkspaceUi(
        client,
        workspaceId,
        (state) => markdownTileIds(state).length === 2,
        'the second pick docks a second markdown tile',
      );
    });

    const beforeRecents = await client.request('get_workspace_ui_state', { workspaceId });
    await runner.step('recents_list_opened_files', async () => {
      await summon('re-summon to inspect recents');
      // Both opens are now recents — recorded daemon-side by the open itself,
      // with no client bookkeeping — and the most recent one ranks first.
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(alpha)),
        'recents appear on an empty query',
      );
      // Both files this run opened are listed, and the later open ranks ahead
      // of the earlier one. Rows from previous runs against the same profile
      // legitimately outrank both (higher use count), so compare positions
      // within this run's files rather than against row 0.
      const alphaAt = state.rows.findIndex((row) => row.path.endsWith(alpha));
      const betaAt = state.rows.findIndex((row) => row.path.endsWith(beta));
      runner.assert(
        alphaAt >= 0 && betaAt >= 0 && alphaAt < betaAt,
        `Recents must list both opened files, the later open first: ${JSON.stringify(state.rows)}`,
      );
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'opener-recents.png'), { client }).catch(() => {});

      // Picking a recent reuses its tile rather than docking a duplicate.
      await pickRow(state, alpha);
      await waitForOpener(client, (current) => !current.open, 'picking a recent closes the opener');
      await delay(1_500);
      const after = await client.request('get_workspace_ui_state', { workspaceId });
      const before = markdownTileIds(beforeRecents);
      const now = markdownTileIds(after);
      runner.assert(
        now.length === 2 && before.every((id) => now.includes(id)),
        `Picking a recent must reuse its tile. Before: ${JSON.stringify(before)}, after: ${JSON.stringify(now)}`,
      );
    });

    await runner.step('agent_edits_surface_without_being_opened', async () => {
      // The tool-use hook is what an agent runs after every tool call. Driving
      // the packaged CLI with real captured payloads (Claude 2.x and codex-cli
      // 0.145.0) exercises the whole chain the agent would: hook parsing, the
      // socket command, the daemon's filter, and the file-activity table.
      // The daemon refuses (and forgets) a file that is not on disk, so the
      // agent's writes have to be real writes.
      fs.writeFileSync(path.join(cwd, 'build', claudeEdited), '# Claude wrote this\n', 'utf8');
      fs.writeFileSync(path.join(cwd, 'build', codexEdited), '# Codex wrote this\n', 'utf8');
      const hookBin = path.join(options.appPath, 'Contents', 'MacOS', 'attn');
      // The hook routes by env, exactly as an agent's generated hook command
      // does. Pin it at the profile under test so a run can never reach another
      // world's daemon.
      const socketPath = socketPathForProfile(profileForAppPath(options.appPath));
      const runHook = (payload) => {
        execFileSync(hookBin, ['_hook-tool-use', sessionId], {
          input: JSON.stringify({ ...payload, cwd }),
          env: { ...process.env, ATTN_SOCKET_PATH: socketPath },
          stdio: ['pipe', 'pipe', 'pipe'],
        });
      };
      runHook({
        tool_name: 'Write',
        tool_input: { file_path: path.join(cwd, 'build', claudeEdited), content: '# Claude wrote this\n' },
      });
      runHook({
        tool_name: 'apply_patch',
        tool_input: {
          command: `*** Begin Patch\n*** Add File: ${path.join(cwd, 'build', codexEdited)}\n+# Codex wrote this\n*** End Patch`,
        },
      });
      // A source file an agent touched must not reach a markdown opener.
      runHook({ tool_name: 'Edit', tool_input: { file_path: path.join(cwd, 'main.go') } });

      await summon('re-summon after the agent edits');
      // Both files are gitignored, so fuzzy mode cannot see them and the empty
      // query lists recents only: appearing here can only be the edit signal.
      const state = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith(claudeEdited))
          && current.rows.some((row) => row.path.endsWith(codexEdited)),
        'files an agent wrote appear in recents without ever being opened',
      );
      runner.assert(
        !state.rows.some((row) => row.path.endsWith('main.go')),
        `A non-markdown edit must not enter the opener: ${JSON.stringify(state.rows)}`,
      );
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'opener-agent-edits.png'), { client }).catch(() => {});

      await pickRow(state, claudeEdited);
      await waitForOpener(client, (current) => !current.open, 'picking an agent-written file closes the opener');
      await waitForWorkspaceUi(
        client,
        workspaceId,
        (ui) => markdownTileIds(ui).length === 3,
        'the agent-written file opens as a third markdown tile',
      );
    });

    await runner.step('path_mode_reaches_gitignored_file', async () => {
      await summon('re-summon for path mode');
      // A query that starts with a slash switches modes: one directory at a
      // time, straight from the filesystem, so a file .gitignore hides from the
      // fuzzy index is still reachable.
      await typeQuery(`${cwd}/`);
      const listing = await waitForOpener(
        client,
        (current) => current.rows.some((row) => row.path.endsWith('/build')),
        'path mode lists the session directory',
      );
      runner.assert(
        listing.rows.some((row) => row.title === 'docs/'),
        `Path mode must mark directories with a trailing slash: ${JSON.stringify(listing.rows)}`,
      );

      // Picking a directory descends instead of opening anything.
      await pickRow(listing, '/build');
      const descended = await waitForOpener(
        client,
        (current) => current.open && current.rows.some((row) => row.path.endsWith(ignored)),
        'picking a directory descends into it',
      );
      runner.assert(
        descended.query.endsWith('/build/'),
        `Descending must rewrite the query to the directory: ${JSON.stringify(descended.query)}`,
      );
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'opener-path-mode.png'), { client }).catch(() => {});

      await pickRow(descended, ignored);
      await waitForOpener(client, (current) => !current.open, 'picking a file in path mode closes the opener');
      await waitForWorkspaceUi(
        client,
        workspaceId,
        (state) => markdownTileIds(state).length === 4,
        'the gitignored file opens as a fourth markdown tile',
      );
    });

    const result = runner.finishSuccess({ sessionId, workspaceId, cwd });
    console.log('[verify] PASS — markdown opener: fuzzy (git-enumerated, gitignore-respecting), recents, and path mode all worked.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    await captureFrontWindowScreenshot(path.join(runner.runDir, 'failure.png'), { client }).catch(() => {});
    const result = runner.finishFailure(error, { sessionId });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
