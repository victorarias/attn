#!/usr/bin/env node
/**
 * Behavioral scenario for the native canvas app driven through its UI
 * automation sidecar. Assumes the native binary is already running with
 * automation enabled (e.g. `ATTN_PROFILE=dev cargo run --bin attn-native`).
 *
 * Coverage:
 *   - wire plumbing: manifest discovery, ping, token rejection, unknown
 *     actions, get_state shape, list_sessions consistency, window geom
 *   - mutate spatial state: move_panel + assert post-state
 *   - mutate selection: select_workspace happy path + bogus id error
 *   - workspace lifecycle: create_workspace + destroy_workspace, asserting
 *     both get_state convergence and the observation events that prove
 *     the canvas sync path executed (not just the daemon ack)
 *   - end-to-end PTY: spawn an `agent=shell` session through native
 *     automation, wait for it to attach as a canvas panel, exercise BOTH input
 *     paths back-to-back — `send_pty_input` (direct daemon route) and
 *     `type_into_panel` (through TerminalView::on_key_down so a
 *     regression in focus or key encoding trips the second case but
 *     not the first), including the selected-vs-input-focused canvas
 *     gate — poll read_pane_text for the marker, unregister the
 *     session, confirm cleanup
 *
 * Tearing down: every resource the scenario creates is unregistered in
 * `finally` blocks, even on assertion failure.
 */
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import process from 'node:process';
import {
  automationEnabledForNativeProfile,
  bundleIdentifierForNativeProfile,
  currentNativeProfile,
  manifestPathForNativeProfile,
} from './nativeHarnessProfile.mjs';

const PROFILE = currentNativeProfile() || 'default';
const MANIFEST_PATH = manifestPathForNativeProfile();
const BUNDLE_ID = bundleIdentifierForNativeProfile();

function fail(message) {
  console.error(`FAIL: ${message}`);
  process.exit(1);
}

function info(message) {
  console.log(message);
}

function readManifest() {
  let body;
  try {
    body = fs.readFileSync(MANIFEST_PATH, 'utf8');
  } catch (error) {
    fail(
      `manifest not found at ${MANIFEST_PATH} — is the native app running with automation? ` +
        `Try: ATTN_PROFILE=${PROFILE === 'default' ? 'dev' : PROFILE} cargo run --bin attn-spike5\n` +
        `(${error.message})`,
    );
  }
  let parsed;
  try {
    parsed = JSON.parse(body);
  } catch (error) {
    fail(`manifest is not valid JSON: ${error.message}`);
  }
  if (!parsed.enabled) fail(`manifest reports enabled=false`);
  if (typeof parsed.port !== 'number') fail(`manifest.port is not a number: ${parsed.port}`);
  if (typeof parsed.token !== 'string' || parsed.token.length < 32) {
    fail(`manifest.token looks wrong (length=${parsed.token?.length})`);
  }
  return parsed;
}

class NativeAutomationConnection {
  constructor(port, token) {
    this.port = port;
    this.token = token;
    this.socket = null;
    this.buffer = '';
    this.pending = []; // [{ resolve, reject }] in FIFO order
    this.nextId = 1;
  }

  async connect() {
    await new Promise((resolve, reject) => {
      this.socket = net.createConnection({ host: '127.0.0.1', port: this.port }, resolve);
      this.socket.once('error', reject);
    });
    this.socket.setEncoding('utf8');
    this.socket.on('data', (chunk) => this.onData(chunk));
    this.socket.on('error', (error) => {
      const reject = this.pending.shift()?.reject;
      if (reject) reject(error);
    });
    this.socket.on('close', () => {
      while (this.pending.length > 0) {
        this.pending.shift().reject(new Error('socket closed'));
      }
    });
  }

  onData(chunk) {
    this.buffer += chunk;
    let nl;
    while ((nl = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, nl).trim();
      this.buffer = this.buffer.slice(nl + 1);
      if (!line) continue;
      const next = this.pending.shift();
      if (!next) continue;
      try {
        next.resolve(JSON.parse(line));
      } catch (error) {
        next.reject(new Error(`response is not valid JSON: ${error.message}`));
      }
    }
  }

  async request(action, payload = null) {
    const id = `scenario-${this.nextId++}`;
    const body = JSON.stringify({ id, token: this.token, action, payload });
    return new Promise((resolve, reject) => {
      this.pending.push({ resolve, reject });
      this.socket.write(`${body}\n`, 'utf8', (error) => {
        if (error) reject(error);
      });
    });
  }

  close() {
    if (this.socket) this.socket.end();
  }
}

async function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function pollUntil(predicate, { timeoutMs = 5000, intervalMs = 100, label = 'condition' }) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await predicate();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await delay(intervalMs);
  }
  // Throw rather than process.exit so callers can dump diagnostics
  // (event tail, last get_state) before the scenario fails.
  throw new Error(
    `timed out waiting for ${label}${lastError ? ` (last error: ${lastError.message})` : ''}`,
  );
}

/**
 * Pull the event ring buffer from the native app since `cursor`. Returns
 * `{ events, next_cursor }`. The cursor is monotonic across the process,
 * so callers can capture it before an action and replay everything that
 * happened during it.
 */
async function tailEvents(conn, cursor = 0) {
  const r = await conn.request('tail_events', { since_id: cursor });
  if (!r.ok) throw new Error(`tail_events failed: ${r.error}`);
  return r.result;
}

function formatEvent(e) {
  return `  +${String(e.id).padStart(5)} ${e.category.padEnd(34)} ${JSON.stringify(e.payload)}`;
}

async function dumpEventsSince(conn, cursor, label) {
  try {
    const tail = await tailEvents(conn, cursor);
    console.error(`\n--- events since ${label} (cursor=${cursor}) ---`);
    for (const ev of tail.events) console.error(formatEvent(ev));
    console.error(`--- end (${tail.events.length} events, next_cursor=${tail.next_cursor}) ---\n`);
  } catch (error) {
    console.error(`(failed to fetch events for diagnosis: ${error.message})`);
  }
}

function pickFirst(state, key) {
  const arr = state.result?.[key];
  if (!Array.isArray(arr) || arr.length === 0) return null;
  return arr[0];
}

async function checkPlumbing(conn, manifest) {
  const pong = await conn.request('ping');
  if (!pong.ok || pong.result?.pong !== true) fail(`ping failed: ${JSON.stringify(pong)}`);
  if (pong.result.pid !== manifest.pid) {
    fail(`pid mismatch: manifest=${manifest.pid} ping=${pong.result.pid}`);
  }
  info(`  ping ok (pid=${pong.result.pid})`);

  const badConn = new NativeAutomationConnection(manifest.port, 'wrongtoken');
  await badConn.connect();
  const rejected = await badConn.request('ping');
  if (rejected.ok || rejected.error !== 'invalid token') {
    fail(`expected token rejection, got: ${JSON.stringify(rejected)}`);
  }
  badConn.close();
  info(`  token enforcement ok`);

  const unknown = await conn.request('definitely-not-an-action');
  if (unknown.ok || !unknown.error?.includes('unknown action')) {
    fail(`unknown-action handling: ${JSON.stringify(unknown)}`);
  }
  info(`  unknown-action error ok`);

  const geom = await conn.request('get_window_geometry');
  if (!geom.ok) fail(`get_window_geometry: ${geom.error}`);
  const b = geom.result.globalBounds;
  if (!(b.width > 0 && b.height > 0)) fail(`window geom looks wrong: ${JSON.stringify(b)}`);
  info(`  window geometry ok (${b.width}x${b.height} @ scale=${geom.result.scaleFactor})`);

  const state = await conn.request('get_state');
  if (!state.ok) fail(`get_state: ${state.error}`);
  for (const field of ['workspaces', 'sessions', 'canvas', 'daemon', 'selected_workspace_id']) {
    if (!(field in state.result)) fail(`get_state missing field: ${field}`);
  }
  const list = await conn.request('list_sessions');
  if (!list.ok) fail(`list_sessions: ${list.error}`);
  if (list.result.length !== state.result.sessions.length) {
    fail(`list_sessions/get_state.sessions length mismatch`);
  }
  info(
    `  state shape ok (workspaces=${state.result.workspaces.length} sessions=${state.result.sessions.length})`,
  );

  // tail_events sanity: shape is right and cursor advances. Other tests
  // depend on this primitive, so a separate plumbing-only check makes
  // failures easier to diagnose.
  const tail0 = await tailEvents(conn);
  if (!Array.isArray(tail0.events) || typeof tail0.next_cursor !== 'number') {
    fail(`tail_events shape wrong: ${JSON.stringify(tail0)}`);
  }
  // Re-tail past the cursor: should be empty unless something just fired.
  const tail1 = await tailEvents(conn, tail0.next_cursor);
  if (tail1.next_cursor < tail0.next_cursor) {
    fail(`tail_events cursor regressed: ${tail1.next_cursor} < ${tail0.next_cursor}`);
  }
  info(`  tail_events ok (${tail0.events.length} historic events, cursor=${tail0.next_cursor})`);
}

async function checkMovePanel(conn) {
  const state = await conn.request('get_state');
  if (!state.ok) fail(`get_state: ${state.error}`);
  const ws = pickFirst(state, 'workspaces');
  if (!ws) {
    info(`  skipped — no workspaces in environment`);
    return;
  }
  const panel = ws.panels?.[0];
  if (!panel) {
    info(`  skipped — workspace has no panels`);
    return;
  }

  const targetX = panel.world_x + 137; // arbitrary, distinguishable
  const targetY = panel.world_y + 31;
  const move = await conn.request('move_panel', {
    workspace_id: ws.id,
    panel_id: panel.id,
    world_x: targetX,
    world_y: targetY,
  });
  if (!move.ok) fail(`move_panel: ${move.error}`);
  if (Math.abs(move.result.panel.world_x - targetX) > 0.001) {
    fail(`move_panel returned wrong world_x: ${move.result.panel.world_x} vs ${targetX}`);
  }

  // Cross-verify via get_state — proves the mutation actually landed in
  // the entity, not just the action's response shape.
  const after = await conn.request('get_state');
  const wsAfter = after.result.workspaces.find((w) => w.id === ws.id);
  const panelAfter = wsAfter.panels.find((p) => p.id === panel.id);
  if (Math.abs(panelAfter.world_x - targetX) > 0.001) {
    fail(`get_state didn't reflect move: world_x=${panelAfter.world_x} expected ${targetX}`);
  }
  info(`  ok (panel ${panel.id}: x ${panel.world_x.toFixed(1)} → ${targetX.toFixed(1)})`);

  // Move it back so re-runs are idempotent. Best-effort.
  await conn.request('move_panel', {
    workspace_id: ws.id,
    panel_id: panel.id,
    world_x: panel.world_x,
    world_y: panel.world_y,
  });
}

async function checkSelectWorkspace(conn) {
  const state = await conn.request('get_state');
  const workspaces = state.result.workspaces || [];
  if (workspaces.length === 0) {
    info(`  skipped — no workspaces`);
    return;
  }

  // Bogus id should error.
  const bogus = await conn.request('select_workspace', { id: 'definitely-not-a-real-id' });
  if (bogus.ok) fail(`select_workspace accepted bogus id: ${JSON.stringify(bogus)}`);
  if (!bogus.error?.includes('unknown workspace')) {
    fail(`select_workspace bogus id error wrong: ${bogus.error}`);
  }

  if (workspaces.length === 1) {
    // Re-select current — proves the wire works even without a real
    // flip target. The full switch behavior is exercised any time
    // there's >1 workspace in the environment.
    const current = state.result.selected_workspace_id || workspaces[0].id;
    const same = await conn.request('select_workspace', { id: current });
    if (!same.ok) fail(`select_workspace (same id): ${same.error}`);
    info(`  ok (single-ws env: re-select + bogus id rejected)`);
    return;
  }

  // Real switch: pick a non-current workspace, switch, assert.
  const current = state.result.selected_workspace_id || workspaces[0].id;
  const target = workspaces.find((w) => w.id !== current);
  const switched = await conn.request('select_workspace', { id: target.id });
  if (!switched.ok) fail(`select_workspace: ${switched.error}`);
  if (switched.result.selected_workspace_id !== target.id) {
    fail(`select_workspace returned wrong id: ${switched.result.selected_workspace_id}`);
  }
  const after = await conn.request('get_state');
  if (after.result.selected_workspace_id !== target.id) {
    fail(`selection didn't flip in get_state: ${after.result.selected_workspace_id}`);
  }
  info(`  ok (flipped ${current} → ${target.id})`);

  // Restore.
  await conn.request('select_workspace', { id: current });
}

async function checkPtyRoundTrip(conn) {
  const state = await conn.request('get_state');
  let ws = pickFirst(state, 'workspaces');
  let createdWorkspaceId = null;
  if (!ws) {
    const created = await conn.request('create_workspace', {
      directory: process.cwd(),
      title: `PTY Round Trip ${new Date().toISOString().slice(11, 19)}`,
    });
    if (!created.ok) fail(`create_workspace for pty round-trip: ${created.error}`);
    createdWorkspaceId = created.result.id;
    await pollUntil(
      async () => {
        const next = await conn.request('get_state');
        return next.result.workspaces.find((candidate) => candidate.id === createdWorkspaceId);
      },
      { timeoutMs: 5000, intervalMs: 100, label: 'pty round-trip workspace in get_state' },
    );
    const nextState = await conn.request('get_state');
    ws = nextState.result.workspaces.find((candidate) => candidate.id === createdWorkspaceId);
    info(`  created temporary workspace ${createdWorkspaceId.slice(0, 8)}… for PTY round-trip`);
  }
  // Capture the event cursor BEFORE spawning so we can prove (or refute)
  // that every step of the chain — daemon broadcast, app receipt, panel
  // creation, attach result — happened during this scenario.
  const cursorBefore = (await tailEvents(conn)).next_cursor;

  const spawned = await conn.request('spawn_session', {
    workspace_id: ws.id,
    agent: 'shell',
  });
  if (!spawned.ok) fail(`spawn_session for pty round-trip: ${spawned.error}`);
  const sessionId = spawned.result.session_id;
  info(`  spawned shell session ${sessionId.slice(0, 8)}… (cursor=${cursorBefore})`);
  try {
    // Wait for the `panel_added` event with our session_id. Events let
    // us assert on the actual UI transition rather than poll-until-state-
    // converges; on failure we'll dump every event since spawn so we can
    // see exactly where the chain stopped (daemon broadcast?
    // NativeApp SessionsChanged? sync_terminal_panels?).
    try {
      await pollUntil(
        async () => {
          const tail = await tailEvents(conn, cursorBefore);
          return tail.events.find(
            (e) => e.category === 'panel_added' && e.payload.session_id === sessionId,
          );
        },
        {
          timeoutMs: 15000,
          intervalMs: 200,
          label: `panel_added event for ${sessionId.slice(0, 8)}`,
        },
      );
    } catch (error) {
      await dumpEventsSince(conn, cursorBefore, 'spawn');
      throw error;
    }
    info(`  panel attached`);

    // Exercise both input paths back-to-back. send_pty_input bypasses
    // GPUI entirely (constructs the wire message in the action handler);
    // type_into_panel routes through TerminalView::on_key_down so a
    // regression in focus, key encoding, or the keystroke→send_input
    // chain trips this case but not the first.
    await sendAndExpectMarker(conn, sessionId, 'send_pty_input', {
      session_id: sessionId,
    });
    await focusPanel(conn, sessionId, false);
    await expectCanvasPanelFocus(conn, sessionId, { inputFocus: false });
    await sendAndExpectNoMarker(conn, sessionId, 'type_into_panel', {
      session_id: sessionId,
      focus: false,
    });
    await sendAndExpectMarker(conn, sessionId, 'type_into_panel', {
      session_id: sessionId,
    });
    await expectCanvasPanelFocus(conn, sessionId, { inputFocus: true });
  } finally {
    await conn.request('unregister_session', { session_id: sessionId }).catch(() => {});
    try {
      await pollUntil(
        async () => {
          const s = await conn.request('get_state');
          const w = s.result.workspaces.find((x) => x.id === ws.id);
          return !w?.panels?.some((p) => p.session_id === sessionId);
        },
        { timeoutMs: 3000, intervalMs: 200, label: 'panel removal after unregister' },
      );
      info(`  cleanup ok (panel + session removed)`);
    } catch (cleanupError) {
      console.error(`WARNING: ${cleanupError.message}`);
    }
    if (createdWorkspaceId) {
      const destroyed = await conn.request('destroy_workspace', { id: createdWorkspaceId });
      if (!destroyed.ok) {
        console.error(`WARNING: destroy_workspace ${createdWorkspaceId}: ${destroyed.error}`);
      }
      try {
        await pollUntil(
          async () => {
            const s = await conn.request('get_state');
            return !s.result.workspaces.some((workspace) => workspace.id === createdWorkspaceId);
          },
          { timeoutMs: 3000, intervalMs: 200, label: 'temporary pty workspace removal' },
        );
        info(`  temporary workspace cleanup ok`);
      } catch (cleanupError) {
        console.error(`WARNING: ${cleanupError.message}`);
      }
    }
  }
}

async function focusPanel(conn, sessionId, inputFocus) {
  const focused = await conn.request('focus_panel', {
    session_id: sessionId,
    input_focus: inputFocus,
  });
  if (!focused.ok) fail(`focus_panel: ${focused.error}`);
  info(`  focus_panel ok (input_focus=${inputFocus})`);
}

async function expectCanvasPanelFocus(conn, sessionId, { inputFocus }) {
  const state = await conn.request('get_state');
  if (!state.ok) fail(`get_state: ${state.error}`);
  const panel = state.result.workspaces
    .flatMap((workspace) => workspace.panels || [])
    .find((candidate) => candidate.session_id === sessionId);
  if (!panel) fail(`panel for session ${sessionId} not found in get_state`);
  const canvas = state.result.canvas;
  if (canvas.selected_panel_id !== panel.id) {
    fail(`selected_panel_id=${canvas.selected_panel_id}, expected ${panel.id}`);
  }
  const expectedInputPanel = inputFocus ? panel.id : null;
  if (canvas.input_focused_panel_id !== expectedInputPanel) {
    fail(
      `input_focused_panel_id=${canvas.input_focused_panel_id}, expected ${expectedInputPanel}`,
    );
  }
}

/**
 * Send `echo <marker>\n` via the named action and wait for the marker to
 * appear on screen. Both `send_pty_input` and `type_into_panel` accept the
 * same `{ session_id, text }` shape, so the only thing that varies between
 * paths is the action name.
 */
async function sendAndExpectMarker(conn, sessionId, action, baseArgs) {
  const marker = `MARK-${action}-${crypto.randomUUID().slice(0, 8)}`;
  const cursorBefore = (await tailEvents(conn)).next_cursor;

  const sent = await conn.request(action, {
    ...baseArgs,
    text: `echo ${marker}\n`,
  });
  if (!sent.ok) fail(`${action}: ${sent.error}`);

  try {
    await pollUntil(
      async () => {
        const r = await conn.request('read_pane_text', { session_id: sessionId });
        if (!r.ok) return false;
        return r.result.text.includes(marker);
      },
      { timeoutMs: 5000, label: `marker ${marker} on screen via ${action}` },
    );
  } catch (error) {
    await dumpEventsSince(conn, cursorBefore, action);
    // Also dump the current screen text — the most likely failure mode
    // is "input arrived but the shell hasn't echoed it back yet".
    try {
      const pane = await conn.request('read_pane_text', { session_id: sessionId });
      if (pane.ok) {
        console.error(`current screen rows:`);
        for (const row of pane.result.rows.slice(-10)) {
          console.error(`  ${JSON.stringify(row)}`);
        }
      }
    } catch (_) {}
    throw error;
  }
  info(`  ok (${action}: typed and echoed ${marker})`);
}

/**
 * Dispatch a marker through a path that should be blocked by canvas-level
 * focus. We wait briefly and assert the marker never appears in the
 * terminal buffer.
 */
async function sendAndExpectNoMarker(conn, sessionId, action, baseArgs) {
  const marker = `MARK-BLOCKED-${action}-${crypto.randomUUID().slice(0, 8)}`;
  const sent = await conn.request(action, {
    ...baseArgs,
    text: `echo ${marker}\n`,
  });
  if (!sent.ok) fail(`${action}: ${sent.error}`);

  await delay(1000);
  const r = await conn.request('read_pane_text', { session_id: sessionId });
  if (!r.ok) fail(`read_pane_text after blocked ${action}: ${r.error}`);
  if (r.result.text.includes(marker)) {
    fail(`${action} leaked through canvas-level focus gate: ${marker}`);
  }
  info(`  ${action} blocked without input focus (${marker})`);
}

/**
 * Round-trip workspace registration through `create_workspace` /
 * `destroy_workspace` against the live daemon. Asserts both that
 * `get_state` reflects the new workspace (so the canvas observed the
 * daemon broadcast) and that `tail_events` fired
 * `workspace_registered_observed` / `workspace_unregistered_observed`
 * (so the path through `NativeApp::DaemonEvent::WorkspaceRegistered`
 * actually executed — not just the daemon ack).
 */
async function checkWorkspaceLifecycle(conn) {
  const cursorBefore = (await tailEvents(conn)).next_cursor;

  let fallbackId = null;
  let wsId = null;
  let cleanupNeeded = false;
  let fallbackCleanupNeeded = false;

  try {
    const fallbackDirectory = `/tmp/attn-scenario-fallback-${crypto.randomUUID().slice(0, 8)}`;
    const fallbackTitle = `000 Scenario Fallback ${new Date().toISOString().slice(11, 19)}`;
    const fallback = await conn.request('create_workspace', {
      directory: fallbackDirectory,
      title: fallbackTitle,
    });
    if (!fallback.ok) fail(`create_workspace fallback: ${fallback.error}`);
    fallbackId = fallback.result.id;
    fallbackCleanupNeeded = true;

    const directory = `/tmp/attn-scenario-ws-${crypto.randomUUID().slice(0, 8)}`;
    const title = `Scenario WS ${new Date().toISOString().slice(11, 19)}`;
    const created = await conn.request('create_workspace', { directory, title });
    if (!created.ok) fail(`create_workspace: ${created.error}`);
    wsId = created.result.id;
    cleanupNeeded = true;
    for (const [label, id] of [['fallback', fallbackId], ['target', wsId]]) {
      if (typeof id !== 'string' || id.length < 16) {
        fail(
          `create_workspace returned suspicious ${label} id: ${JSON.stringify({
            fallback,
            created,
          })}`,
        );
      }
    }
    info(`  created fallback ${fallbackId.slice(0, 8)}… and target ${wsId.slice(0, 8)}…`);

    // Wait for the workspace to land in get_state — proves the canvas
    // observed the daemon's workspace_registered broadcast and updated
    // its workspace map.
    try {
      await pollUntil(
        async () => {
          const s = await conn.request('get_state');
          return (
            s.ok &&
            s.result.workspaces?.some((w) => w.id === fallbackId) &&
            s.result.workspaces?.some((w) => w.id === wsId) &&
            s.result.selected_workspace_id === wsId
          );
        },
        {
          timeoutMs: 5000,
          intervalMs: 100,
          label: `workspace ${wsId.slice(0, 8)} in get_state and selected`,
        },
      );
    } catch (error) {
      await dumpEventsSince(conn, cursorBefore, 'create_workspace');
      throw error;
    }

    // Confirm the corresponding observation event fired. Catches the
    // failure mode where get_state is right because of a fresh poll but
    // the event-driven sync path silently broke.
    const tail = await tailEvents(conn, cursorBefore);
    const observed = tail.events.find(
      (e) => e.category === 'workspace_registered_observed' && e.payload.workspace_id === wsId,
    );
    const fallbackObserved = tail.events.find(
      (e) =>
        e.category === 'workspace_registered_observed' && e.payload.workspace_id === fallbackId,
    );
    if (!observed || !fallbackObserved) {
      await dumpEventsSince(conn, cursorBefore, 'create_workspace');
      fail(`missing workspace_registered_observed event for lifecycle workspaces`);
    }
    info(`  observed workspace_registered_observed for ${wsId.slice(0, 8)}…`);

    // Tear down through the same wire surface and assert the inverse.
    const cursorBeforeDestroy = (await tailEvents(conn)).next_cursor;
    const destroyed = await conn.request('destroy_workspace', { id: wsId });
    if (!destroyed.ok) fail(`destroy_workspace: ${destroyed.error}`);
    cleanupNeeded = false;

    try {
      await pollUntil(
        async () => {
          const s = await conn.request('get_state');
          return (
            s.ok &&
            !s.result.workspaces?.some((w) => w.id === wsId) &&
            s.result.selected_workspace_id &&
            s.result.selected_workspace_id !== wsId
          );
        },
        {
          timeoutMs: 5000,
          intervalMs: 100,
          label: `workspace ${wsId.slice(0, 8)} gone and selection restored`,
        },
      );
    } catch (error) {
      await dumpEventsSince(conn, cursorBeforeDestroy, 'destroy_workspace');
      throw error;
    }

    const tailAfter = await tailEvents(conn, cursorBeforeDestroy);
    const unregistered = tailAfter.events.find(
      (e) =>
        e.category === 'workspace_unregistered_observed' && e.payload.workspace_id === wsId,
    );
    if (!unregistered) {
      await dumpEventsSince(conn, cursorBeforeDestroy, 'destroy_workspace');
      fail(`no workspace_unregistered_observed event for ${wsId}`);
    }
    const fallbackDestroyed = await conn.request('destroy_workspace', { id: fallbackId });
    if (!fallbackDestroyed.ok) fail(`destroy_workspace fallback: ${fallbackDestroyed.error}`);
    fallbackCleanupNeeded = false;

    info(`  ok (created ${wsId.slice(0, 8)}, restored selection after destroy, cleaned up)`);
  } finally {
    if (cleanupNeeded && wsId) {
      // destroy_workspace is idempotent on the daemon, so a best-effort
      // cleanup on assertion failure won't double-fault.
      await conn.request('destroy_workspace', { id: wsId }).catch(() => {});
    }
    if (fallbackCleanupNeeded && fallbackId) {
      await conn.request('destroy_workspace', { id: fallbackId }).catch(() => {});
    }
  }
}

/**
 * Round-trip a session through the new `spawn_session` and
 * `unregister_session` automation actions — the same wire path the
 * canvas's "+ Session" toolbar and the per-panel close button drive.
 * This isolates lifecycle plumbing from `checkPtyRoundTrip`, which
 * exercises terminal input, focus routing, and visible output.
 */
async function checkSessionLifecycle(conn) {
  // Always provision a fresh host workspace rather than trusting any
  // previously-selected one — earlier tests destroy workspaces without
  // awaiting the daemon's broadcast, which can leave a stale
  // selected_workspace_id pointing at a workspace that's already gone.
  // Use an existing directory (HOME or /tmp): unlike workspace metadata
  // (which the daemon never opens), the cwd flows through to the
  // session's PTY spawn, and a non-existent path makes the worker stall.
  const directory = process.env.HOME || '/tmp';
  const created = await conn.request('create_workspace', {
    directory,
    title: `Scenario Session Host ${new Date().toISOString().slice(11, 19)}`,
  });
  if (!created.ok) fail(`create_workspace (session host): ${created.error}`);
  const hostWorkspaceId = created.result.id;
  await pollUntil(
    async () => {
      const s = await conn.request('get_state');
      return s.ok && s.result.workspaces?.some((w) => w.id === hostWorkspaceId);
    },
    { timeoutMs: 5000, label: `host workspace ${hostWorkspaceId.slice(0, 8)} in get_state` },
  );

  const cursorBefore = (await tailEvents(conn)).next_cursor;
  const spawned = await conn.request('spawn_session', {
    workspace_id: hostWorkspaceId,
    agent: 'shell',
  });
  if (!spawned.ok) fail(`spawn_session: ${spawned.error}`);
  const sessionId = spawned.result.session_id;
  if (typeof sessionId !== 'string' || sessionId.length < 16) {
    fail(`spawn_session returned suspicious id: ${JSON.stringify(spawned)}`);
  }
  info(`  spawned session ${sessionId.slice(0, 8)}… via action`);

  let panelCleanupNeeded = true;
  try {
    // Wait for the panel + the spawn-success ack. Both prove different
    // things: panel_added → sessions_changed sync ran; spawn_result
    // success → the daemon accepted the wire message.
    try {
      await pollUntil(
        async () => {
          const tail = await tailEvents(conn, cursorBefore);
          const sawPanel = tail.events.some(
            (e) => e.category === 'panel_added' && e.payload.session_id === sessionId,
          );
          const sawAck = tail.events.some(
            (e) =>
              e.category === 'session_spawn_succeeded' && e.payload.session_id === sessionId,
          );
          return sawPanel && sawAck;
        },
        { timeoutMs: 15000, intervalMs: 200, label: `panel + spawn ack for ${sessionId.slice(0, 8)}` },
      );
    } catch (error) {
      await dumpEventsSince(conn, cursorBefore, 'spawn_session');
      throw error;
    }
    info(`  panel + spawn ack observed`);

    const cursorBeforeKill = (await tailEvents(conn)).next_cursor;
    const killed = await conn.request('unregister_session', { session_id: sessionId });
    if (!killed.ok) fail(`unregister_session: ${killed.error}`);

    try {
      await pollUntil(
        async () => {
          const s = await conn.request('get_state');
          if (!s.ok) return false;
          const ws = s.result.workspaces.find((w) => w.id === hostWorkspaceId);
          return !ws?.panels?.some((p) => p.session_id === sessionId);
        },
        {
          timeoutMs: 5000,
          intervalMs: 200,
          label: `panel ${sessionId.slice(0, 8)} pruned after unregister_session`,
        },
      );
    } catch (error) {
      await dumpEventsSince(conn, cursorBeforeKill, 'unregister_session');
      throw error;
    }
    panelCleanupNeeded = false;
    info(`  cleanup ok (panel + session removed via action)`);
  } finally {
    if (panelCleanupNeeded) {
      // Best-effort fallback so a failed assertion doesn't leak the
      // session into subsequent scenarios.
      await conn.request('unregister_session', { session_id: sessionId }).catch(() => {});
    }
    // Block on the workspace actually disappearing — the action
    // returns once the cmd is queued, but the broadcast that drops
    // it from the canvas's workspaces_by_id may still be in flight.
    // If we returned now, the next scenario's `pickFirst('workspaces')`
    // could grab this stale workspace and race the daemon into spawning
    // a session in a workspace that's about to vanish.
    await conn.request('destroy_workspace', { id: hostWorkspaceId }).catch(() => {});
    await pollUntil(
      async () => {
        const s = await conn.request('get_state');
        return s.ok && !s.result.workspaces?.some((w) => w.id === hostWorkspaceId);
      },
      { timeoutMs: 5000, intervalMs: 100, label: `host workspace ${hostWorkspaceId.slice(0, 8)} drained` },
    ).catch(() => {});
  }
}

async function main() {
  info(`profile=${PROFILE} bundle=${BUNDLE_ID}`);
  info(`automationEnabledForProfile=${automationEnabledForNativeProfile()}`);
  info(`manifest=${MANIFEST_PATH}`);

  const manifest = readManifest();
  info(`manifest ok: port=${manifest.port} pid=${manifest.pid}`);

  const conn = new NativeAutomationConnection(manifest.port, manifest.token);
  await conn.connect();
  info(`connected to 127.0.0.1:${manifest.port}`);

  try {
    info(`\n[wire plumbing]`);
    await checkPlumbing(conn, manifest);

    info(`\n[move_panel]`);
    await checkMovePanel(conn);

    info(`\n[select_workspace]`);
    await checkSelectWorkspace(conn);

    info(`\n[workspace lifecycle]`);
    await checkWorkspaceLifecycle(conn);

    info(`\n[session lifecycle via action]`);
    await checkSessionLifecycle(conn);

    info(`\n[pty round-trip]`);
    await checkPtyRoundTrip(conn);

    info(`\nPASS`);
  } finally {
    conn.close();
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
