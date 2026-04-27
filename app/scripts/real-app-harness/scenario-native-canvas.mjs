#!/usr/bin/env node
/**
 * Behavioral scenario for the native canvas app driven through its UI
 * automation sidecar. Assumes the native binary is already running with
 * automation enabled (e.g. `ATTN_PROFILE=dev cargo run --bin attn-spike5`).
 *
 * Coverage:
 *   - wire plumbing: manifest discovery, ping, token rejection, unknown
 *     actions, get_state shape, list_sessions consistency, window geom
 *   - mutate spatial state: move_panel + assert post-state
 *   - mutate selection: select_workspace happy path + bogus id error
 *   - end-to-end PTY: spawn an `agent=shell` session via the daemon WS,
 *     wait for it to attach as a canvas panel, send `echo HELLO-<uuid>`,
 *     poll read_pane_text until the marker appears, unregister the
 *     session, confirm cleanup
 *
 * Tearing down: every resource the scenario creates is unregistered in
 * `finally` blocks, even on assertion failure.
 */
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import process from 'node:process';
import WebSocket from 'ws';
import {
  automationEnabledForNativeProfile,
  bundleIdentifierForNativeProfile,
  currentNativeProfile,
  manifestPathForNativeProfile,
} from './nativeHarnessProfile.mjs';

const PROFILE = currentNativeProfile() || 'default';
const MANIFEST_PATH = manifestPathForNativeProfile();
const BUNDLE_ID = bundleIdentifierForNativeProfile();
const DAEMON_WS_URL =
  process.env.ATTN_WS_URL ||
  (PROFILE === 'dev' ? 'ws://localhost:29849/ws' : 'ws://localhost:9849/ws');

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

/**
 * Tiny daemon WS client for spawn_session / unregister. Kept minimal —
 * uses raw JSON commands matching what wsctl (Go dev helper) sends.
 */
class DaemonWSClient {
  constructor(url) {
    this.url = url;
    this.ws = null;
    this.eventHandlers = new Set();
    this.opened = false;
  }

  async connect() {
    this.ws = new WebSocket(this.url);
    await new Promise((resolve, reject) => {
      this.ws.once('open', () => {
        this.opened = true;
        resolve();
      });
      this.ws.once('error', reject);
    });
    this.ws.on('message', (data) => {
      let event;
      try {
        event = JSON.parse(data.toString());
      } catch {
        return;
      }
      for (const handler of this.eventHandlers) handler(event);
    });
    // Identify as a canvas-style client so the daemon registers shell
    // sessions we spawn — same hello the native app sends.
    this.ws.send(JSON.stringify({
      cmd: 'client_hello',
      client_kind: 'native-canvas-harness',
      version: 'scenario-native-canvas',
      capabilities: ['shell_as_session'],
    }));
  }

  onEvent(handler) {
    this.eventHandlers.add(handler);
    return () => this.eventHandlers.delete(handler);
  }

  send(payload) {
    return new Promise((resolve, reject) => {
      this.ws.send(JSON.stringify(payload), (error) => {
        if (error) reject(error);
        else resolve();
      });
    });
  }

  async spawnShellSession({ workspaceId, cwd, cols = 80, rows = 24 }) {
    const id = crypto.randomUUID();
    const result = new Promise((resolveResult, rejectResult) => {
      const off = this.onEvent((event) => {
        if (event.event === 'spawn_result' && event.id === id) {
          off();
          if (event.success) resolveResult(id);
          else rejectResult(new Error(`spawn rejected: ${event.error || 'unknown'}`));
        }
      });
      setTimeout(() => {
        off();
        rejectResult(new Error(`spawn_session timed out for ${id}`));
      }, 5000);
    });
    await this.send({
      cmd: 'spawn_session',
      id,
      cwd,
      workspace_id: workspaceId,
      agent: 'shell',
      label: 'scenario-shell',
      cols,
      rows,
    });
    return result;
  }

  async unregisterSession(id) {
    await this.send({ cmd: 'unregister', id });
  }

  close() {
    if (this.opened) this.ws.close();
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

async function checkPtyRoundTrip(conn, daemon) {
  const state = await conn.request('get_state');
  const ws = pickFirst(state, 'workspaces');
  if (!ws) {
    info(`  skipped — no workspaces to host a shell`);
    return;
  }
  const cwd = ws.directory || process.env.HOME || '/tmp';
  const marker = `MARK-${crypto.randomUUID().slice(0, 8)}`;

  // Capture the event cursor BEFORE spawning so we can prove (or refute)
  // that every step of the chain — daemon broadcast, app receipt, panel
  // creation, attach result — happened during this scenario.
  const cursorBefore = (await tailEvents(conn)).next_cursor;

  const sessionId = await daemon.spawnShellSession({ workspaceId: ws.id, cwd });
  info(`  spawned shell session ${sessionId.slice(0, 8)}… (cursor=${cursorBefore})`);
  try {
    // Wait for the `panel_added` event with our session_id. Events let
    // us assert on the actual UI transition rather than poll-until-state-
    // converges; on failure we'll dump every event since spawn so we can
    // see exactly where the chain stopped (daemon broadcast?
    // Spike5App SessionsChanged? sync_terminal_panels?).
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

    // Capture another cursor so the type-and-echo step gets its own
    // diagnostic window if it fails.
    const cursorBeforeType = (await tailEvents(conn)).next_cursor;

    // Type the marker. Newline triggers the shell to execute echo, so
    // the marker shows up on the screen via shell echo.
    const sent = await conn.request('send_pty_input', {
      session_id: sessionId,
      text: `echo ${marker}\n`,
    });
    if (!sent.ok) fail(`send_pty_input: ${sent.error}`);

    // Poll read_pane_text until the marker is present somewhere.
    try {
      await pollUntil(
        async () => {
          const r = await conn.request('read_pane_text', { session_id: sessionId });
          if (!r.ok) return false;
          return r.result.text.includes(marker);
        },
        { timeoutMs: 5000, label: `marker ${marker} on screen` },
      );
    } catch (error) {
      await dumpEventsSince(conn, cursorBeforeType, 'send_pty_input');
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
    info(`  ok (typed and echoed ${marker})`);
  } finally {
    await daemon.unregisterSession(sessionId).catch(() => {});
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
  }
}

async function main() {
  info(`profile=${PROFILE} bundle=${BUNDLE_ID}`);
  info(`automationEnabledForProfile=${automationEnabledForNativeProfile()}`);
  info(`manifest=${MANIFEST_PATH}`);
  info(`daemon=${DAEMON_WS_URL}`);

  const manifest = readManifest();
  info(`manifest ok: port=${manifest.port} pid=${manifest.pid}`);

  const conn = new NativeAutomationConnection(manifest.port, manifest.token);
  await conn.connect();
  info(`connected to 127.0.0.1:${manifest.port}`);

  const daemon = new DaemonWSClient(DAEMON_WS_URL);
  await daemon.connect();
  info(`connected to daemon ws`);

  try {
    info(`\n[wire plumbing]`);
    await checkPlumbing(conn, manifest);

    info(`\n[move_panel]`);
    await checkMovePanel(conn);

    info(`\n[select_workspace]`);
    await checkSelectWorkspace(conn);

    info(`\n[pty round-trip]`);
    await checkPtyRoundTrip(conn, daemon);

    info(`\nPASS`);
  } finally {
    conn.close();
    daemon.close();
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
