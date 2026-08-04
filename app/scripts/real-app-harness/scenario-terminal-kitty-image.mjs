#!/usr/bin/env node

/**
 * Packaged-app proof for worker-authoritative kitty images, which are ON by
 * default: with NOTHING injected into the environment, a program writes a kitty
 * graphics escape into a real session PTY, the worker (the system's only kitty
 * parser) describes the placement to the app, the app pulls the pixels and draws
 * them, the placement rides the scroll of the text it sits in, and the program's
 * delete empties the set.
 *
 * And the escape hatch: with ATTN_KITTY_STORAGE_LIMIT=0 in the daemon's
 * environment, the very same escape produces NOTHING — no stored image, no
 * placement, no wire traffic. Leg 5 proves that on a restarted daemon, so a hatch
 * broken by accident fails here rather than in someone's terminal.
 *
 * Why this scenario is not in the serial matrix (scenarioCatalog.mjs): it stops
 * and re-ensures the profile daemon twice to move ATTN_KITTY_STORAGE_LIMIT in
 * and out of the world the pty-workers inherit. That is a world change the
 * matrix's other scenarios should not have to reason about, the same reason
 * scenario-automation-scheduled-cleanup.mjs stays out. Run it directly:
 *
 *   ATTN_HARNESS_PROFILE=<name> node scripts/real-app-harness/scenario-terminal-kitty-image.mjs
 *
 * The escape reaches the PTY as RAW BYTES from a file (`cat`), never as a
 * write_pane JS string: an ESC in a JS string is mangled by the shell quoting
 * on the way through, and a kitty escape that loses its ESC is just text.
 */

import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, resolveHarnessResources } from './harnessProfile.mjs';
import { ensureFreshWorld } from './freshWorld.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';

// The escape hatch: a storage limit of zero disables the kitty protocol
// entirely. Unset means the default limit, which is why the image legs inject
// nothing at all.
const STORAGE_OFF = '0';

// The image: 64x48 RGB, a coarse checkerboard with a white frame. Small enough
// to send in a couple of escapes, loud enough to find by eye in the screenshot.
const IMAGE_WIDTH = 64;
const IMAGE_HEIGHT = 48;
// Kitty's own convention chunks the base64 payload at 4096 bytes per escape
// (m=1 until the last). Matching it means this scenario exercises the worker's
// multi-escape path rather than a single-escape shape no real emitter sends.
const PAYLOAD_CHUNK = 4096;
const IMAGE_ID = 8801;
// A negative z-index, which kitty defines as "draw this under the text". It is
// the one placement field that is a SIGNED int32 all the way from ghostty's
// storage through the protocol to the client's store, so asserting it comes
// back as -1 is what proves nothing on that path widened or truncated it.
const IMAGE_Z = -1;

const BLOB_TIMEOUT_MS = 20_000;
const PLACEMENT_TIMEOUT_MS = 20_000;

// A capture smaller than this is not a picture of the app. Receipt, measured
// 2026-08-03 on this scenario's own artifacts: a screen-region grab of a parked
// window returned a 40x1200px sliver of chrome at 14,201 bytes, while every real
// full-window capture of the same app ran 155,065-493,022 bytes (the low end
// being a near-empty pane). 60KB sits ~4x above every broken observation and
// ~2.5x below the smallest healthy one, so only a capture that failed to see the
// window touches it.
const MIN_CAPTURE_BYTES = 60_000;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function profileEnv(profile, extra = {}) {
  const env = { ...process.env, ATTN_PROFILE: profile, ...extra };
  // profile-env's job in shell form: a routing override inherited from the
  // caller's shell would silently aim these daemon commands somewhere else.
  for (const key of ['ATTN_SOCKET_PATH', 'ATTN_DB_PATH', 'ATTN_CONFIG_PATH', 'ATTN_PLUGIN_DIR']) {
    delete env[key];
  }
  return env;
}

function run(binary, args, env) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
    timeout: 60_000,
  });
}

function checkerboardRGB(width, height) {
  const pixels = Buffer.alloc(width * height * 3);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const offset = (y * width + x) * 3;
      const frame = x < 2 || y < 2 || x >= width - 2 || y >= height - 2;
      const light = (Math.floor(x / 8) + Math.floor(y / 8)) % 2 === 0;
      const [r, g, b] = frame ? [255, 255, 255] : (light ? [255, 0, 255] : [0, 255, 0]);
      pixels[offset] = r;
      pixels[offset + 1] = g;
      pixels[offset + 2] = b;
    }
  }
  return pixels;
}

// Transmit-and-display (a=T) of raw 24-bit RGB (f=24) sent inline (t=d), split
// across escapes the way kitty's convention does. Mirrors
// kittyPlaceRGBChunked in internal/pty/kittycorpus_test.go, which is the shape
// the worker's corpus goldens are pinned against.
//
// q=2 silences the terminal's own reply. A terminal that took the image answers
// `\x1b_Gi=<id>;OK\x1b\\` on the PTY, and this emitter is `cat` at a shell
// prompt with nobody reading it — so the reply lands in the shell's line editor
// and eats the next command. That is faithful terminal behavior, not a defect
// (kitty does the same); real emitters either set q or read the reply
// themselves. Silencing it keeps the assertions about placements.
function kittyTransmitAndDisplay(id, width, height, z) {
  const encoded = checkerboardRGB(width, height).toString('base64');
  let out = '';
  let offset = 0;
  let first = true;
  while (offset < encoded.length) {
    const part = encoded.slice(offset, offset + PAYLOAD_CHUNK);
    offset += part.length;
    const more = offset < encoded.length ? 1 : 0;
    out += first
      ? `\x1b_Ga=T,i=${id},f=24,t=d,s=${width},v=${height},z=${z},q=2,m=${more};${part}\x1b\\`
      : `\x1b_Gm=${more};${part}\x1b\\`;
    first = false;
  }
  return out;
}

function findPlacement(state, imageId) {
  return (state?.placements || []).find((entry) => entry.imageId === imageId) || null;
}

/**
 * Evidence-grade screenshot: the app photographs its OWN window.
 *
 * The obvious route, captureFrontWindowScreenshot, resolves the window's bounds
 * and then grabs that RECTANGLE OF THE SCREEN. The harness parks the app window
 * mostly off-screen, so the rect clips at the screen edge and yields a sliver of
 * window chrome that looks identical in every leg — which is exactly how this
 * scenario once filed three byte-identical PNGs of nothing as proof. The bridge
 * action instead captures by CGWindowID (`screencapture -l <id> -o`), reading
 * the window's own surface whatever sits on top of it, and it re-resolves that
 * id on every call, which matters because a relaunch mints a new one.
 *
 * Two tripwires so a capture that saw nothing fails the leg instead of being
 * filed: the app must report it captured the window rather than falling back to
 * a region grab, and the file must be large enough to be a window.
 */
async function captureWindowEvidence(client, runner, name) {
  const outputPath = path.join(runner.runDir, name);
  const result = await client.request('capture_native_window_screenshot', { path: outputPath });
  runner.assert(
    result?.source === 'native_window',
    `capture ${name} reported source ${JSON.stringify(result?.source ?? null)}, want "native_window" — a screen-region fallback photographs whatever occupies the window's rect, not the window (${result?.windowCaptureError ?? 'no window-capture error reported'})`,
    result,
  );
  const bytes = fs.statSync(outputPath).size;
  runner.assert(
    bytes >= MIN_CAPTURE_BYTES,
    `capture ${name} is ${bytes} bytes, under the ${MIN_CAPTURE_BYTES}-byte floor every real window capture clears — the window was not in the frame`,
    { path: outputPath, bytes, windowId: result?.windowId ?? null, bounds: result?.bounds ?? null },
  );
  return {
    name,
    path: outputPath,
    bytes,
    sha256: createHash('sha256').update(fs.readFileSync(outputPath)).digest('hex'),
    windowId: result?.windowId ?? null,
    bounds: result?.bounds ?? null,
  };
}

async function poll(fn, description, timeoutMs) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await sleep(200);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-kitty-image.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('kitty image scenario requires a named non-production profile (it restarts the profile daemon)');
  }
  const resources = resolveHarnessResources(profile);
  const binary = path.join(resources.appPath, 'Contents', 'MacOS', 'attn');

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-KITTY-IMAGE',
    tier: 'tier1-local-shell',
    prefix: 'terminal-kitty-image',
    metadata: {
      profile,
      darkLimit: STORAGE_OFF,
      focus: 'worker-described kitty placement renders, scrolls, deletes by default — and stays dark with the escape hatch',
    },
  });

  // Raw bytes on disk, so the pane only ever runs `cat`. Anything that puts an
  // ESC through shell quoting is how these escapes get silently corrupted.
  const imageFile = path.join(runner.sessionDir, 'kitty-image.bin');
  const deleteFile = path.join(runner.sessionDir, 'kitty-delete.bin');
  fs.writeFileSync(imageFile, `\n${kittyTransmitAndDisplay(IMAGE_ID, IMAGE_WIDTH, IMAGE_HEIGHT, IMAGE_Z)}\n`, 'binary');
  fs.writeFileSync(deleteFile, '\x1b_Ga=d,q=2\x1b\\\n', 'binary');

  const defaultEnv = profileEnv(profile);
  const offEnv = profileEnv(profile, { ATTN_KITTY_STORAGE_LIMIT: STORAGE_OFF });

  // Nothing to inject for the image legs: images are on by default, so the app
  // launches with the environment it would have in the field. The app spawns the
  // daemon only when none is running, and the worker inherits the DAEMON's
  // environment — which is why the dark leg ensures a daemon with the hatch set
  // BEFORE relaunching the app, so the app never spawns one without it.
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let darkSessionId = null;
  let placed = null;
  let scrolled = null;
  let cleared = null;
  let darkState = null;
  const captures = {};

  runner.log('run context', {
    runDir: runner.runDir, sessionDir: runner.sessionDir, wsUrl: options.wsUrl, profile,
  });

  const closeSessionPanes = async (id) => {
    if (!id) return;
    const workspace = await client.request('get_workspace', { sessionId: id }).catch(() => null);
    for (const pane of workspace?.panes || []) {
      await client.request('close_pane', { sessionId: id, paneId: pane.paneId }).catch(() => {});
    }
  };

  // Cleanups run in reverse registration order: leave the profile daemon as the
  // world expects to find it (nothing injected, images on by default) LAST,
  // after the app is gone.
  runner.registerCleanup('restore_default_daemon', () => {
    try { run(binary, ['daemon', 'stop'], defaultEnv); } catch {}
    try { run(binary, ['daemon', 'ensure'], defaultEnv); } catch {}
  });
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    await closeSessionPanes(sessionId);
    await closeSessionPanes(darkSessionId);
  });

  // One shell session with an attached, ready pane. Both legs need the same
  // thing; the only difference is which daemon environment spawned its worker.
  const openShellPane = async (label) => {
    const id = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: runner.sessionDir,
      label,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId: id });
    const workspace = await client.request('get_workspace', { sessionId: id });
    const pane = workspace?.panes?.[0];
    runner.assert(Boolean(pane), `No pane in workspace: ${JSON.stringify(workspace)}`);
    await waitForPaneVisible(client, id, pane.paneId, 20_000);
    await waitForPaneAttached(client, id, pane.paneId, 20_000);
    await waitForPaneShellReady(client, id, pane.paneId, {
      timeoutMs: 20_000,
      description: 'shell pane ready',
    });
    return { sessionId: id, paneId: pane.paneId };
  };

  let pane = null;
  let darkPane = null;

  try {
    // Nothing injected: whatever this daemon does with kitty escapes is the
    // default every user gets.
    await runner.step('start_daemon_default', async () => {
      await ensureFreshWorld({ profile, appPath: resources.appPath });
      try { run(binary, ['daemon', 'stop'], defaultEnv); } catch {}
      run(binary, ['daemon', 'ensure'], defaultEnv);
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_session', async () => {
      pane = await openShellPane(`kitty-image-${runner.runId}`);
      sessionId = pane.sessionId;
    });

    await runner.step('emit_kitty_image', async () => {
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `cat ${imageFile}` });
      // The pane text proves the command ran; the placement below proves the
      // bytes were parsed. Waiting on the echo first keeps a failure honest
      // about which of the two broke.
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.includes('kitty-image.bin'),
        'image emitter command echoed',
        20_000,
      );

      placed = await poll(async () => {
        const state = await client.request('get_pane_placement_state', { sessionId, paneId: pane.paneId });
        const placement = findPlacement(state, IMAGE_ID);
        // 'present' is the whole round trip: the worker described the
        // placement, the app noticed it had no pixels for that
        // (image, generation), pulled them, and holds them now.
        return placement && placement.blob === 'present' ? { state, placement } : null;
      }, `kitty placement ${IMAGE_ID} described and its pixels pulled`, BLOB_TIMEOUT_MS);

      runner.assert(placed.state.available === true, 'pane reported no live terminal handle', placed.state);
      runner.assert(
        placed.placement.pixelWidth === IMAGE_WIDTH && placed.placement.pixelHeight === IMAGE_HEIGHT,
        `placement pixel size = ${placed.placement.pixelWidth}x${placed.placement.pixelHeight}, want ${IMAGE_WIDTH}x${IMAGE_HEIGHT}`,
        placed.placement,
      );
      runner.assert(placed.placement.visible === true, 'placement is not inside the grid rectangle', placed.placement);
      runner.assert(
        placed.placement.z === IMAGE_Z,
        `placement z = ${placed.placement.z}, want ${IMAGE_Z} — a negative z-index survives ghostty's storage, the wire, and the client's store as a signed int32, and the renderer draws it under the text`,
        placed.placement,
      );
      runner.assert(
        placed.state.placements.length === 1,
        `placement set carries ${placed.state.placements.length} entries, want exactly the one image`,
        placed.state,
      );
      runner.writeJson('placement-placed.json', placed.state);
      captures.drawn = await captureWindowEvidence(client, runner, 'image-drawn.png');
    });

    await runner.step('placement_rides_the_scroll', async () => {
      // Two bursts, each more than a screenful, so the image's row leaves the
      // viewport and then keeps going. The invariant needs no line counting:
      // the image keeps its absolute buffer row, and its screen row falls by
      // exactly the number of rows that moved into scrollback.
      const lines = placed.state.rows + 5;
      const settledAfterBurst = async (previousScrollback, description) => poll(async () => {
        const state = await client.request('get_pane_placement_state', { sessionId, paneId: pane.paneId });
        const placement = findPlacement(state, IMAGE_ID);
        if (!placement || state.scrollback <= previousScrollback) return null;
        // Mid-burst, the pane's scrollback and the placement set read beside it
        // belong to different instants of the same scroll; two identical reads
        // mean the stream has stopped moving under the arithmetic below.
        await sleep(500);
        const settled = await client.request('get_pane_placement_state', { sessionId, paneId: pane.paneId });
        const settledPlacement = findPlacement(settled, IMAGE_ID);
        if (!settledPlacement || settled.scrollback !== state.scrollback) return null;
        return { state: settled, placement: settledPlacement };
      }, description, PLACEMENT_TIMEOUT_MS);

      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `seq 1 ${lines}` });
      const first = await settledAfterBurst(placed.state.scrollback, 'the first scroll to land and settle with the image still described');

      const firstRows = first.state.scrollback - placed.state.scrollback;
      runner.assert(
        first.placement.bufferRow === placed.placement.bufferRow,
        `buffer row moved from ${placed.placement.bufferRow} to ${first.placement.bufferRow}; scrolling moves an image between history and screen, never off its row`,
        { before: placed.placement, after: first.placement, scrolledRows: firstRows },
      );
      runner.assert(
        first.placement.visible === false,
        'the image scrolled past the top of the viewport but still reports as inside the grid',
        first.placement,
      );

      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `seq 1 ${lines}` });
      scrolled = await settledAfterBurst(first.state.scrollback, 'the second scroll to land and settle with the image still described');

      const scrolledRows = scrolled.state.scrollback - first.state.scrollback;
      runner.assert(
        scrolled.placement.bufferRow === first.placement.bufferRow,
        `buffer row moved from ${first.placement.bufferRow} to ${scrolled.placement.bufferRow} on a later scroll; the error must not accumulate`,
        { before: first.placement, after: scrolled.placement },
      );
      runner.assert(
        scrolled.placement.screenRow === first.placement.screenRow - scrolledRows,
        `screen row is ${scrolled.placement.screenRow}, want ${first.placement.screenRow - scrolledRows} after ${scrolledRows} more rows scrolled`,
        { before: first.placement, after: scrolled.placement, scrolledRows },
      );
      runner.writeJson('placement-scrolled.json', {
        placed: placed.state, firstBurst: first.state, secondBurst: scrolled.state,
      });
    });

    await runner.step('program_delete_empties_the_set', async () => {
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `cat ${deleteFile}` });
      cleared = await poll(async () => {
        const state = await client.request('get_pane_placement_state', { sessionId, paneId: pane.paneId });
        return state.available && findPlacement(state, IMAGE_ID) === null ? state : null;
      }, 'the placement set to empty after the program deleted the image', PLACEMENT_TIMEOUT_MS);
      runner.assert(
        cleared.placements.length === 0,
        `placement set still carries ${cleared.placements.length} entries after a=d`,
        cleared,
      );
      runner.writeJson('placement-cleared.json', cleared);
      captures.deleted = await captureWindowEvidence(client, runner, 'image-deleted.png');
      // Liveness, not rendering: the pane has scrolled and run two more commands
      // since image-drawn.png, so these two frames cannot legitimately match.
      // Identical bytes mean the camera is pointed at something that does not
      // change — a parked window's chrome, a stale file — and every screenshot
      // this run files is worthless. What the image itself did is the placement
      // state's job, above; this only certifies the pictures are of the app.
      runner.assert(
        captures.deleted.sha256 !== captures.drawn.sha256,
        `image-deleted.png is byte-identical to image-drawn.png (sha256 ${captures.drawn.sha256}), though the pane scrolled and ran two commands between them — the captures are not showing live window content`,
        captures,
      );
    });

    await runner.step('dark_with_the_escape_hatch', async () => {
      // The way out. Same app, same escape, same everything — only the daemon's
      // environment differs, and a session spawned from it must store no image
      // at all, so there is never anything to describe.
      await closeSessionPanes(sessionId);
      sessionId = null;
      await client.quitApp();
      run(binary, ['daemon', 'stop'], offEnv);
      run(binary, ['daemon', 'ensure'], offEnv);
      await launchFreshAppAndConnect(client, observer);

      darkPane = await openShellPane(`kitty-dark-${runner.runId}`);
      darkSessionId = darkPane.sessionId;
      await client.request('write_pane', {
        sessionId: darkSessionId, paneId: darkPane.paneId, text: `cat ${imageFile}`,
      });
      await waitForPaneText(
        client,
        darkSessionId,
        darkPane.paneId,
        (text) => text.includes('kitty-image.bin'),
        'image emitter command echoed on the dark daemon',
        20_000,
      );
      // Give the whole pipeline the same budget the default leg needed to
      // produce a placement, so "nothing arrived" means nothing arrives, not
      // that we looked too early.
      await sleep(BLOB_TIMEOUT_MS);
      darkState = await client.request('get_pane_placement_state', {
        sessionId: darkSessionId, paneId: darkPane.paneId,
      });
      runner.assert(darkState.available === true, 'dark pane reported no live terminal handle', darkState);
      runner.assert(
        darkState.placements.length === 0,
        `a daemon with ATTN_KITTY_STORAGE_LIMIT=0 still produced ${darkState.placements.length} placement(s)`,
        darkState,
      );
      runner.writeJson('placement-dark.json', darkState);
      // Captured after a relaunch, so its windowId is a different number from
      // the two above — recorded in captures.json, which is how a reader can see
      // for themselves that each capture resolved the window it ran against.
      captures.dark = await captureWindowEvidence(client, runner, 'dark-no-image.png');
      runner.writeJson('captures.json', captures);
    });

    const result = runner.finishSuccess({
      sessionId: darkSessionId,
      darkLimit: STORAGE_OFF,
      placed: placed.placement,
      scrolled: scrolled.placement,
      clearedCount: cleared.placements.length,
      darkCount: darkState.placements.length,
      grid: { cols: placed.state.cols, rows: placed.state.rows },
      captures,
    });
    console.log('[verify] PASS — kitty image described, drawn, scrolled, deleted by default; nothing with the escape hatch.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    for (const id of [sessionId, darkSessionId].filter(Boolean)) {
      await captureSessionArtifacts(client, runner.runDir, `kitty-image-failure-${id}`, id).catch(() => {});
    }
    // Diagnostic, not evidence: no tripwires here, because a failing run wants
    // whatever picture is obtainable, including a fallback region grab.
    await client.request('capture_native_window_screenshot', {
      path: path.join(runner.runDir, 'failure.png'),
    }).catch(() => {});
    const result = runner.finishFailure(error, { sessionId, darkSessionId });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    await closeSessionPanes(sessionId);
    await closeSessionPanes(darkSessionId);
    await client.quitApp().catch(() => {});
    await observer.close();
    try { run(binary, ['daemon', 'stop'], defaultEnv); } catch {}
    try { run(binary, ['daemon', 'ensure'], defaultEnv); } catch {}
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
