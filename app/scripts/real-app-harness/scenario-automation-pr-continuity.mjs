#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { spawn, execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import WebSocket from 'ws';
import { parseCommonArgs, printCommonHelp, launchFreshAppAndConnect } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { readFrontendProtocolVersion } from './presentDaemon.mjs';
import {
  currentHarnessProfile,
  dataDirForProfile,
  resolveHarnessResources,
} from './harnessProfile.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, '../../..');
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function profileEnv(profile, extra = {}) {
  const env = { ...process.env, ATTN_PROFILE: profile, ...extra };
  for (const key of ['ATTN_SOCKET_PATH', 'ATTN_DB_PATH', 'ATTN_CONFIG_PATH', 'ATTN_PLUGIN_DIR']) {
    delete env[key];
  }
  return env;
}

function run(binary, args, env, options = {}) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: options.stdio || ['ignore', 'pipe', 'pipe'],
    timeout: options.timeout || 30_000,
  });
}

function runJSON(binary, args, env) {
  return JSON.parse(run(binary, args, env));
}

function createFixture(root) {
  const repo = path.join(root, 'fixture-repo');
  fs.mkdirSync(repo, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repo });
  execFileSync('git', ['remote', 'add', 'origin', 'https://mock.github.local/owner/repo.git'], { cwd: repo });
  fs.writeFileSync(path.join(repo, 'README.md'), 'Safe local automation continuity fixture.\n');
  execFileSync('git', ['add', 'README.md'], { cwd: repo });
  execFileSync('git', ['commit', '-q', '-m', 'fixture'], {
    cwd: repo,
    env: {
      ...process.env,
      GIT_AUTHOR_NAME: 'attn',
      GIT_AUTHOR_EMAIL: 'attn@local',
      GIT_COMMITTER_NAME: 'attn',
      GIT_COMMITTER_EMAIL: 'attn@local',
    },
  });
  return { repo, sha: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim() };
}

function recentCodexTranscripts() {
  const root = path.join(os.homedir(), '.codex', 'sessions');
  const files = [];
  const stack = [root];
  while (stack.length > 0) {
    const current = stack.pop();
    let entries = [];
    try { entries = fs.readdirSync(current, { withFileTypes: true }); } catch { continue; }
    for (const entry of entries) {
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) stack.push(full);
      else if (entry.isFile() && entry.name.endsWith('.jsonl')) files.push(full);
    }
  }
  return files.sort((a, b) => fs.statSync(b).mtimeMs - fs.statSync(a).mtimeMs);
}

function readRolloutID(file) {
  const first = fs.readFileSync(file, 'utf8').split('\n', 1)[0];
  try {
    const event = JSON.parse(first);
    return event?.type === 'session_meta' ? String(event.payload?.id || '').trim() : '';
  } catch { return ''; }
}

function seedCodexRollout(targetHome) {
  for (const source of recentCodexTranscripts()) {
    const id = readRolloutID(source);
    if (!id) continue;
    const target = path.join(targetHome, 'sessions', 'seed', path.basename(source));
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.copyFileSync(source, target);
    return { id, source, target };
  }
  throw new Error('no existing Codex rollout found under ~/.codex/sessions');
}

function createCodexProbe(root) {
  const log = path.join(root, 'codex-invocations.jsonl');
  const executable = path.join(root, 'codex-probe.mjs');
  fs.writeFileSync(executable, `#!/usr/bin/env node\nimport fs from 'node:fs';\nfs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({argv: process.argv.slice(2), at: new Date().toISOString()}) + '\\n');\nsetInterval(() => {}, 1000);\n`, { mode: 0o700 });
  return { executable, log };
}

async function startMock(sha) {
  const child = spawn(process.execPath, [path.join(REPO_ROOT, 'scripts/automation-mock-github.mjs')], {
    cwd: REPO_ROOT,
    env: { ...process.env, ATTN_AUTOMATION_MOCK_SHA: sha },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  const started = Date.now();
  while (!stdout.includes('\n') && Date.now() - started < 10_000) {
    if (child.exitCode !== null) throw new Error(`mock GitHub exited: ${stderr}`);
    await delay(25);
  }
  if (!stdout.includes('\n')) throw new Error(`mock GitHub did not start: ${stderr}`);
  return { child, ...JSON.parse(stdout.split('\n', 1)[0]) };
}

async function setRequested(mockURL, active) {
  const response = await fetch(`${mockURL}/__control/requested`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ active }),
  });
  if (!response.ok) throw new Error(`mock control returned ${response.status}`);
}

async function wsRequest(wsURL, message, event, timeoutMs = 30_000) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsURL);
    const timer = setTimeout(() => { ws.close(); reject(new Error(`timed out waiting for ${event}`)); }, timeoutMs);
    let ready = false;
    ws.once('open', () => ws.send(JSON.stringify({
      cmd: 'client_hello',
      client_kind: 'harness-automation-continuity',
      version: `protocol-${readFrontendProtocolVersion()}`,
      capabilities: ['workspace_sessions'],
    })));
    ws.on('message', (raw) => {
      const value = JSON.parse(raw.toString());
      if (!ready && value.event === 'initial_state') {
        ready = true;
        ws.send(JSON.stringify(message));
        return;
      }
      if (event ? value.event !== event : value.ok !== true) return;
      clearTimeout(timer);
      ws.close();
      if (value.success === false) reject(new Error(value.error || `${event} failed`));
      else resolve(value);
    });
    ws.once('error', (error) => { clearTimeout(timer); reject(error); });
  });
}

async function socketRequest(socketPath, message, timeoutMs = 10_000) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection(socketPath);
    let raw = '';
    const timer = setTimeout(() => { socket.destroy(); reject(new Error(`timed out waiting for socket response to ${message.cmd}`)); }, timeoutMs);
    socket.setEncoding('utf8');
    socket.once('connect', () => socket.write(`${JSON.stringify(message)}\n`));
    socket.on('data', (chunk) => {
      raw += chunk;
      if (!raw.includes('\n')) return;
      clearTimeout(timer);
      socket.end();
      const value = JSON.parse(raw.split('\n', 1)[0]);
      if (!value.ok) reject(new Error(value.error || `${message.cmd} failed`));
      else resolve(value);
    });
    socket.once('error', (error) => { clearTimeout(timer); reject(error); });
  });
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(100);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function invocations(log) {
  if (!fs.existsSync(log)) return [];
  return fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automation-pr-continuity.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation continuity scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = path.join(resources.appPath, 'Contents', 'MacOS', 'attn');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-PR-CONTINUITY',
    tier: 'tier2-packaged-local-provider',
    prefix: 'automation-pr-continuity',
    metadata: { profile, provider: 'local mock GitHub', transcript: 'copied existing Codex rollout' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const fixture = createFixture(runner.sessionDir);
  const seedHome = path.join(runner.sessionDir, 'codex-home');
  const seed = seedCodexRollout(seedHome);
  const probe = createCodexProbe(runner.sessionDir);
  const definitionID = `slice4-${Date.now().toString(36)}`;
  const definitionFile = path.join(runner.sessionDir, 'definition.yml');
  let mock = null;
  let daemonEnv = null;
  let sessionID = '';
  let ticketID = '';
  let worktree = '';
  try {
    mock = await runner.step('start_local_mock_github', () => startMock(fixture.sha));
    daemonEnv = profileEnv(profile, {
      ATTN_MOCK_GH_URL: mock.url,
      ATTN_MOCK_GH_HOST: mock.host,
      ATTN_MOCK_GH_TOKEN: 'test-token',
      CODEX_HOME: seedHome,
    });
    await runner.step('restart_isolated_daemon_with_mock', async () => {
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await poll(() => {
        try { runJSON(binary, ['automation', 'list'], daemonEnv); return { ready: true }; } catch { return null; }
      }, 'profile daemon');
    });
    await runner.step('launch_packaged_app', () => launchFreshAppAndConnect(client, observer));
    fs.writeFileSync(definitionFile, `api_version: attn.dev/automations/v1alpha1\nid: ${definitionID}\nname: Slice 4 packaged continuity proof\nenabled: true\ntrigger:\n  type: github_review_requested\n  repositories:\n    mode: all_accessible\n    include: [mock.github.local/owner/repo]\nprompt: |\n  Review only the local fixture and report in this ticket. Never write to GitHub.\nlaunch:\n  driver: codex\n  executable: ${JSON.stringify(probe.executable)}\n  model: gpt-5.6-terra\n  effort: high\nlocation:\n  type: repository_worktree\n  repository_sources:\n    default: {type: managed_cache}\n    overrides:\n      mock.github.local/owner/repo:\n        type: local_clone\n        path: ${JSON.stringify(fixture.repo)}\npolicy:\n  continuity: per_subject\n  catch_up: latest\n  overlap: coalesce\n`);
    await runner.step('apply_definition', async () => runJSON(binary, ['automation', 'apply', '--file', definitionFile], daemonEnv));
    await runner.step('deliver_initial_review', async () => {
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      const runRow = await poll(() => {
        const rows = runJSON(binary, ['automation', 'runs', definitionID], daemonEnv) || [];
        return rows.find((row) => row.State === 'delivered') || null;
      }, 'initial delivered automation run', 45_000);
      sessionID = runRow.SessionID;
      ticketID = runRow.TicketID;
      worktree = observer.getSession(sessionID)?.directory || path.join(dataDirForProfile(profile), 'automation', 'worktrees', sessionID, 'repo');
      await poll(() => invocations(probe.log).length >= 1 ? invocations(probe.log)[0] : null, 'first Codex launch');
      runner.assert(fs.existsSync(worktree), 'initial exact-SHA worktree exists', { worktree });
      runner.assert(execFileSync('git', ['rev-parse', 'HEAD'], { cwd: worktree, encoding: 'utf8' }).trim() === fixture.sha, 'initial worktree is pinned to provider SHA');
    });
    await runner.step('seed_resume_and_stop_origin', async () => {
      await socketRequest(resources.socket, { cmd: 'set_session_resume_id', id: sessionID, resume_session_id: seed.id });
      fs.writeFileSync(path.join(worktree, 'review-notes.txt'), 'preserve me across review cycles\n');
      run(binary, ['ticket', 'status', 'completed', '--ticket', ticketID, '--session', sessionID, '--comment', 'first review complete'], daemonEnv);
      const db = path.join(dataDirForProfile(profile), 'attn.db');
      execFileSync('sqlite3', [db, `UPDATE tickets SET archived_at=datetime('now') WHERE id='${ticketID.replaceAll("'", "''")}';`]);
      await client.request('close_session', { sessionId: sessionID });
      await observer.waitFor(() => !observer.getSession(sessionID) ? true : null, 'origin reviewer to unregister');
    });
    await runner.step('withdraw_and_rerequest', async () => {
      await setRequested(mock.url, false);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      await setRequested(mock.url, true);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
    });
    const continuation = await runner.step('assert_same_reviewer_resumed', async () => {
      const row = await poll(() => {
        const rows = runJSON(binary, ['automation', 'runs', definitionID], daemonEnv) || [];
        return rows.length >= 2 && rows[0].State === 'delivered' ? rows[0] : null;
      }, 'delivered continuation', 45_000);
      const calls = await poll(() => invocations(probe.log).length >= 2 ? invocations(probe.log) : null, 'Codex resume launch');
      const resumed = calls[1].argv;
      runner.assert(row.SessionID === sessionID && row.TicketID === ticketID, 'continuation reuses the same session and ticket', row);
      runner.assert(resumed.includes('resume') && resumed.includes(seed.id), 'Codex receives the copied rollout id', { argv: resumed, rollout: seed.id });
      runner.assert(resumed.some((arg) => arg.includes('gpt-5.6-terra')) && resumed.some((arg) => arg.includes('high')), 'resume keeps pinned model and effort', { argv: resumed });
      runner.assert(fs.readFileSync(path.join(worktree, 'review-notes.txt'), 'utf8').includes('preserve me'), 'dirty reviewer work survives continuation');
      const tickets = runJSON(binary, ['ticket', 'list', '--all', '--json'], daemonEnv);
      const ticket = tickets.find((item) => item.id === ticketID);
      runner.assert(ticket?.status === 'working' && !ticket.archived_at, 'successful continuation reopens and unarchives the ticket', ticket);
      return row;
    });
    await runner.step('assert_missing_worktree_fails_visibly', async () => {
      await client.request('close_session', { sessionId: sessionID });
      await observer.waitFor(() => !observer.getSession(sessionID) ? true : null, 'continued reviewer to unregister');
      await setRequested(mock.url, false);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      fs.rmSync(worktree, { recursive: true, force: true });
      await setRequested(mock.url, true);
      await wsRequest(options.wsUrl, { cmd: 'refresh_prs' }, 'refresh_prs_result');
      const failed = await poll(() => {
        const rows = runJSON(binary, ['automation', 'runs', definitionID], daemonEnv) || [];
        return rows.length >= 3 && rows[0].State === 'failed' ? rows[0] : null;
      }, 'visible missing-worktree failure', 30_000);
      runner.assert(String(failed.LastError).includes('worktree') && String(failed.LastError).includes('missing'), 'missing delivered worktree fails without recreation', failed);
    });
    runner.finishSuccess({ profile, definitionID, sessionID, ticketID, worktree, seed, continuation });
  } catch (error) {
    runner.finishFailure(error, { profile, definitionID, sessionID, ticketID, worktree, seed });
    throw error;
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    if (daemonEnv) { try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {} }
    if (mock?.child) mock.child.kill('SIGTERM');
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
