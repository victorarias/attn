#!/usr/bin/env node

// Packaged-app regression: command-block geometry through the REAL lifecycle a
// workspace terminal experiences, across fish, bash, and zsh:
//   - complex make-like output (ANSI colors, long wrapping compiler lines,
//     carriage-return progress overwrites), tall seq output, small blocks
//   - an app relaunch (attach replay rebuilds the model from raw bytes; the
//     path that broke `make` output rendering in prod)
//   - pane width changes via split/close-split (buffer reflow; the path that
//     corrupted block geometry)
//
// Shell contract (correct-or-absent invariant):
//   - fish emits OSC 133 natively: blocks must exist, survive the relaunch
//     replay, and hit-test correctly at every step. A width change clears the
//     store; if the change interrupted attach replay, the re-requested replay
//     rebuilds the blocks at the new width — either way a click must select
//     the right block or nothing
//   - bash/zsh run WITHOUT shell integration (--norc / -f so a host config
//     cannot add markers): blocks must be ABSENT and clicks must select
//     nothing — never a wrong box
//   - text integrity (replay + width changes, including wrapped colored
//     lines) must hold for all three shells at every step

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  relaunchAppAndConnect,
  parseCommonArgs,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { delay } from './macosDriver.mjs';
import {
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const SHELLS = [
  { name: 'fish', exec: 'exec fish', blocksExpected: true },
  { name: 'bash', exec: 'exec bash --noprofile --norc', blocksExpected: false },
  { name: 'zsh', exec: 'exec zsh -f', blocksExpected: false },
];

function shellAvailable(shell) {
  try {
    execFileSync('/bin/sh', ['-c', `command -v ${shell}`], { encoding: 'utf8' });
    return true;
  } catch {
    return false;
  }
}

// Make-like output, portable across all three shells: colored
// percent-prefixed lines long enough to wrap in a full pane (and wrap several
// times in a split pane), then a carriage-return progress overwrite, then a
// done marker. Shipped as a script file in the session cwd (a long one-liner
// mangles when typed into a live shell); `sh make-sim.sh <token>` is ONE
// command, so under fish it is one block.
const MAKE_SIM_SCRIPT = `i=1
while [ $i -le 24 ]; do
  printf "\\033[32m[%3d%%]\\033[0m Building CXX object deep/nested/module_path/src/component_$i/impl/translation_unit_$i.cpp.o -O2 -Wall -Wextra -fno-omit-frame-pointer\\n" $((i*4))
  i=$((i+1))
done
printf "linking objects...\\rlinked OK         \\n"
echo "$1"
`;

function assertBlockInvariants(state, label) {
  if (!state.available) throw new Error(`${label}: block state unavailable`);
  const total = state.scrollback + state.rows;
  for (const block of state.blocks) {
    if (block.endRow !== undefined && block.endRow > total) {
      throw new Error(`${label}: block ${block.id} (${block.command}) endRow=${block.endRow} > total=${total} — stale geometry served`);
    }
  }
  console.log(`[verify] ${label}: ${state.blocks.length} blocks OK (total=${total}, cols=${state.cols}, selected=${state.selectedBlockId})`);
  return state;
}

function assertNoBlocks(state, label) {
  if (state.blocks.length !== 0 || state.selectedBlockId !== null) {
    throw new Error(`${label}: expected NO blocks without shell integration, got ${state.blocks.length} (selected=${state.selectedBlockId})`);
  }
}

// Text integrity through replay and reflow. Short sentinels must exist as
// whole rows; long sentinels and the long compiler lines wrap (and re-wrap on
// width change), so they must survive as contiguous tokens once row breaks
// are joined.
function assertTextIntegrity(text, sentinels, longTokens, label) {
  const lines = text.split('\n').map((line) => line.trim());
  for (const sentinel of sentinels) {
    if (!lines.includes(sentinel)) {
      throw new Error(`${label}: sentinel ${JSON.stringify(sentinel)} missing — replay/reflow corrupted history`);
    }
  }
  const joined = text.replace(/\n/g, '');
  for (const token of longTokens) {
    if (!joined.includes(token)) {
      throw new Error(`${label}: wrapped token ${JSON.stringify(token)} missing — reflow corrupted a long line`);
    }
  }
}

async function runCommandAndWait(client, sessionId, paneId, command, expectedLine) {
  await client.request('write_pane', { sessionId, paneId, text: command });
  await waitForPaneText(client, sessionId, paneId,
    (text) => text.split('\n').some((line) => line.trim() === expectedLine),
    `output of ${expectedLine}`, 30_000);
}

// read_pane_text returns the whole buffer; click_pane_cell takes VIEWPORT rows
// (scrolled to bottom, the viewport shows the last `rows` buffer lines).
async function clickOutputLine(client, sessionId, paneId, state, lineText) {
  const read = await client.request('read_pane_text', { sessionId, paneId });
  const lines = read.text.split('\n');
  const bufferRow = lines.findIndex((line) => line.trim() === lineText);
  if (bufferRow < 0) throw new Error(`line ${JSON.stringify(lineText)} not in pane text`);
  const viewportRow = bufferRow - Math.max(0, lines.length - state.rows);
  if (viewportRow < 0) throw new Error(`line ${JSON.stringify(lineText)} scrolled out of the viewport`);
  await client.request('click_pane_cell', { sessionId, paneId, cell: { row: viewportRow, col: 2 } });
  await delay(300);
  return client.request('get_pane_block_state', { sessionId, paneId });
}

async function clickAndExpectSelected(client, sessionId, paneId, lineText, commandPrefix, label) {
  const state = await client.request('get_pane_block_state', { sessionId, paneId });
  const selected = await clickOutputLine(client, sessionId, paneId, state, lineText);
  const block = selected.blocks.find((b) => (b.command || '').startsWith(commandPrefix));
  if (!block) throw new Error(`${label}: block for ${JSON.stringify(commandPrefix)} not tracked: ${JSON.stringify(selected.blocks.map((b) => b.command))}`);
  if (selected.selectedBlockId !== block.id) {
    throw new Error(`${label}: clicked ${JSON.stringify(lineText)} but selected ${selected.selectedBlockId} (want ${block.id}) — hit-test wrong`);
  }
  console.log(`[verify] ${label}: click selected the correct block (${block.id})`);
}

// After a width change the block store clears, then the replay re-request
// (triggered when the geometry change interrupted queued replay) may rebuild
// the blocks at the new width. Both outcomes honor correct-or-absent: if a
// block covers the clicked line it must be the RIGHT block; if none does, the
// click must select nothing.
async function clickAndExpectCorrectOrAbsent(client, sessionId, paneId, lineText, commandPrefix, label) {
  const state = await client.request('get_pane_block_state', { sessionId, paneId });
  const selected = await clickOutputLine(client, sessionId, paneId, state, lineText);
  if (selected.selectedBlockId === null) {
    console.log(`[verify] ${label}: click selected nothing (absent is correct)`);
    return;
  }
  const block = selected.blocks.find((b) => b.id === selected.selectedBlockId);
  if (!block || !(block.command || '').startsWith(commandPrefix)) {
    throw new Error(`${label}: clicked ${JSON.stringify(lineText)} selected block ${selected.selectedBlockId} (${block?.command ?? 'unknown'}) — want ${JSON.stringify(commandPrefix)} or nothing`);
  }
  console.log(`[verify] ${label}: click selected the correct rebuilt block (${block.id})`);
}

async function clickAndExpectNothing(client, sessionId, paneId, lineText, label) {
  const state = await client.request('get_pane_block_state', { sessionId, paneId });
  const selected = await clickOutputLine(client, sessionId, paneId, state, lineText);
  if (selected.selectedBlockId !== null) {
    throw new Error(`${label}: click selected block ${selected.selectedBlockId} but this shell has no integration — wrong box`);
  }
  console.log(`[verify] ${label}: click selected nothing (correct-or-absent)`);
}

async function selectAndWaitForPane(client, sessionId, paneId) {
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneAttached(client, sessionId, paneId, 20_000);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  for (const shell of SHELLS) {
    if (!shellAvailable(shell.name)) throw new Error(`${shell.name} required`);
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'BLOCK-RESIZE',
    tier: 'tier1-local-shell',
    prefix: 'block-resize',
    metadata: {
      shells: SHELLS.map((s) => s.name),
      focus: 'command-block geometry across relaunch replay and pane width changes',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const sessions = [];
  const summary = { shells: {} };

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir });

  const token = (shell) => `RESIZE_${shell}_${runner.runId}`;
  const makeDone = (shell) => `MAKE_DONE_${shell}_${runner.runId}`;
  // Short sentinels are checked as whole rows at every geometry. The runId
  // tokens and compiler paths wrap at full width and are checked as
  // contiguous tokens in the row-joined text — but ONLY at the geometry the
  // history was replayed at: the terminal resizes without reflow, so a
  // narrower pane truncates each row at its width by design (the worker keeps
  // the raw bytes; the next replay re-parses them at the new size).
  const sentinelsFor = () => ['smallblock', '142', 'linked OK'];
  const longTokensFor = (shell) => [
    token(shell.name),
    makeDone(shell.name),
    'deep/nested/module_path/src/component_7/impl/translation_unit_7.cpp.o',
    'deep/nested/module_path/src/component_24/impl/translation_unit_24.cpp.o',
  ];

  // Cleanup, registered as soon as each resource type exists so a signal
  // mid-scenario still tears them down. Runner cleanups run in REVERSE
  // registration order, so register observer/app first (they must close
  // LAST) and the session-panes sweep last (it must close FIRST) to
  // reproduce the effective order below: close panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    for (const { sessionId } of sessions) {
      const ws = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const p of ws?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: p.paneId }).catch(() => {});
      }
    }
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // Phase A: one session per shell, each with tall output, a make-like
    // colored/wrapping/CR-progress block, and a small marker block.
    await runner.step('phase_a_baseline_blocks', async () => {
      for (const shell of SHELLS) {
        const dir = path.join(runner.sessionDir, shell.name);
        fs.mkdirSync(dir, { recursive: true });
        fs.writeFileSync(path.join(dir, 'make-sim.sh'), MAKE_SIM_SCRIPT);
        const sessionId = await createSessionAndWaitForInitialPane({
          client, observer, cwd: dir, label: `blocks-${shell.name}-${runner.runId}`,
          agent: 'shell', waitForInitialPaneVisible: false, sessionWaitMs: 30_000,
        });
        await client.request('select_session', { sessionId });
        const workspace = await client.request('get_workspace', { sessionId });
        const pane = workspace?.panes?.[0];
        runner.assert(Boolean(pane), `No pane for ${shell.name}: ${JSON.stringify(workspace)}`);
        const paneId = pane.paneId;
        await waitForPaneVisible(client, sessionId, paneId, 20_000);
        await waitForPaneAttached(client, sessionId, paneId, 20_000);
        await waitForPaneShellReady(client, sessionId, paneId, { timeoutMs: 20_000, description: `${shell.name} shell ready` });
        await client.request('write_pane', { sessionId, paneId, text: shell.exec });
        await delay(1_500);

        await runCommandAndWait(client, sessionId, paneId, `seq 1 200; echo ${token(shell.name)}`, token(shell.name));
        await runCommandAndWait(client, sessionId, paneId, `sh make-sim.sh ${makeDone(shell.name)}`, makeDone(shell.name));
        await runCommandAndWait(client, sessionId, paneId, 'echo smallblock', 'smallblock');

        const baseline = assertBlockInvariants(
          await client.request('get_pane_block_state', { sessionId, paneId }),
          `${shell.name} baseline`,
        );
        if (shell.blocksExpected) {
          runner.assert(
            baseline.blocks.some((b) => (b.command || '').startsWith('seq 1 200')),
            `${shell.name} baseline: tall block not tracked`,
          );
          runner.assert(
            baseline.blocks.some((b) => (b.command || '').startsWith('sh make-sim.sh')),
            `${shell.name} baseline: make-like block not tracked`,
          );
        } else {
          assertNoBlocks(baseline, `${shell.name} baseline`);
        }
        sessions.push({ shell, sessionId, paneId });
        summary.shells[shell.name] = { baselineBlocks: baseline.blocks.length };
      }
    });

    // Phase B: one relaunch — attach replay rebuilds every model (and, for
    // fish, the block store) from raw bytes: the `make install` reinstall path.
    await runner.step('phase_b_relaunch_replay', async () => {
      await relaunchAppAndConnect(client, observer);
      for (const { shell, sessionId, paneId } of sessions) {
        await selectAndWaitForPane(client, sessionId, paneId);
        await waitForPaneText(client, sessionId, paneId, (text) => {
          const lines = text.split('\n').map((line) => line.trim());
          const joined = text.replace(/\n/g, '');
          return sentinelsFor(shell).every((sentinel) => lines.includes(sentinel))
            && longTokensFor(shell).every((needle) => joined.includes(needle));
        }, `${shell.name} replayed history after relaunch`, 30_000);
        const read = await client.request('read_pane_text', { sessionId, paneId });
        assertTextIntegrity(read.text, sentinelsFor(shell), longTokensFor(shell), `${shell.name} after-relaunch`);
        const state = assertBlockInvariants(
          await client.request('get_pane_block_state', { sessionId, paneId }),
          `${shell.name} after-relaunch`,
        );
        if (shell.blocksExpected) {
          // Blocks rebuilt from replayed markers: the small block and the
          // make-like block (clicked via its visible done marker) must both
          // hit-test correctly.
          await clickAndExpectSelected(client, sessionId, paneId, 'smallblock', 'echo smallblock', `${shell.name} after-relaunch small`);
          await clickAndExpectSelected(client, sessionId, paneId, makeDone(shell.name), 'sh make-sim.sh', `${shell.name} after-relaunch make`);
        } else {
          assertNoBlocks(state, `${shell.name} after-relaunch`);
          await clickAndExpectNothing(client, sessionId, paneId, 'smallblock', `${shell.name} after-relaunch`);
        }
        summary.shells[shell.name].afterRelaunchBlocks = state.blocks.length;
      }
      await client.request('capture_native_window_screenshot', { path: path.join(runner.runDir, '1-after-relaunch.png') }).catch(() => {});
    });

    // Phase C: width changes via split + close-split, per shell. A width
    // change invalidates stored block rows (the store clears); when it lands
    // while attach replay is still applying, the app re-requests the replay
    // and rebuilds the model — history must come back intact at the new
    // width, and fish blocks must be correct-or-absent at every step;
    // bash/zsh stay block-free.
    await runner.step('phase_c_width_changes', async () => {
      for (const { shell, sessionId, paneId } of sessions) {
        await selectAndWaitForPane(client, sessionId, paneId);
        await client.request('split_pane', { sessionId, targetPaneId: paneId, direction: 'vertical' });
        await delay(1_500);
        // The split focuses the new pane's session; re-select ours so bridge
        // clicks resolve against the workspace view the DOM renders.
        await client.request('select_session', { sessionId });
        await delay(300);
        // History may be restored by a debounced replay re-request after the
        // split's geometry change — wait for it rather than sampling once.
        // Short sentinels only: rows truncate at the narrower width (no-reflow
        // resize), so the wrapped long tokens are not expected here.
        await waitForPaneText(client, sessionId, paneId, (text) => {
          const lines = text.split('\n').map((line) => line.trim());
          return sentinelsFor(shell).every((sentinel) => lines.includes(sentinel));
        }, `${shell.name} history restored after split`, 20_000);
        const afterSplit = assertBlockInvariants(
          await client.request('get_pane_block_state', { sessionId, paneId }),
          `${shell.name} after-split`,
        );
        if (shell.blocksExpected) {
          await clickAndExpectCorrectOrAbsent(client, sessionId, paneId, 'smallblock', 'echo smallblock', `${shell.name} after-split`);
        } else {
          assertNoBlocks(afterSplit, `${shell.name} after-split`);
        }
        const splitRead = await client.request('read_pane_text', { sessionId, paneId });
        assertTextIntegrity(splitRead.text, sentinelsFor(shell), [], `${shell.name} after-split`);

        await runCommandAndWait(client, sessionId, paneId, 'echo postsplit', 'postsplit');
        if (shell.blocksExpected) {
          await clickAndExpectSelected(client, sessionId, paneId, 'postsplit', 'echo postsplit', `${shell.name} post-split`);
        } else {
          await clickAndExpectNothing(client, sessionId, paneId, 'postsplit', `${shell.name} post-split`);
        }

        const ws2 = await client.request('get_workspace', { sessionId });
        const newPane = (ws2?.panes || []).find((p) => p.paneId !== paneId);
        if (newPane) {
          await client.request('close_pane', { sessionId, paneId: newPane.paneId });
          await delay(1_500);
        }
        await client.request('select_session', { sessionId });
        await delay(300);
        assertBlockInvariants(
          await client.request('get_pane_block_state', { sessionId, paneId }),
          `${shell.name} after-close-split`,
        );
        await runCommandAndWait(client, sessionId, paneId, 'echo postclose', 'postclose');
        if (shell.blocksExpected) {
          await clickAndExpectSelected(client, sessionId, paneId, 'postclose', 'echo postclose', `${shell.name} post-close`);
        } else {
          await clickAndExpectNothing(client, sessionId, paneId, 'postclose', `${shell.name} post-close`);
        }
        // Widening back keeps every row, but columns truncated at the narrow
        // width stay truncated until the next replay re-parses the raw history
        // (no-reflow resize) — so the long tokens are not required here either.
        await waitForPaneText(client, sessionId, paneId, (text) => {
          const lines = text.split('\n').map((line) => line.trim());
          return [...sentinelsFor(shell), 'postsplit'].every((sentinel) => lines.includes(sentinel));
        }, `${shell.name} history intact after close-split`, 20_000);
        const finalRead = await client.request('read_pane_text', { sessionId, paneId });
        assertTextIntegrity(
          finalRead.text,
          [...sentinelsFor(shell), 'postsplit'],
          [],
          `${shell.name} final`,
        );
        console.log(`[verify] ${shell.name}: resize round-trip OK`);
      }
      await client.request('capture_native_window_screenshot', { path: path.join(runner.runDir, '2-final.png') }).catch(() => {});
    });

    const result = runner.finishSuccess({ shells: summary.shells });
    console.log('[verify] PASS — fish/bash/zsh: replay intact, blocks correct-or-absent across resizes');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    const result = runner.finishFailure(error, { shells: summary.shells });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    for (const { sessionId } of sessions) {
      const ws = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const p of ws?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: p.paneId }).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
