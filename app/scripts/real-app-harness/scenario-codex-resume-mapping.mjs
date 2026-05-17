#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForMain,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { ensureCodexMainPromptReady } from './scenarioAgents.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
    codexExecutable: process.env.ATTN_REAL_CODEX_EXECUTABLE || '',
  };

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--codex-executable') options.codexExecutable = args[++index] || '';
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  return {
    options,
    help: Boolean(options.help),
  };
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function submitCodexPromptViaUi(client, sessionId, text) {
  await client.request('type_pane_via_ui', {
    sessionId,
    paneId: 'main',
    text,
  });
  // Codex treats fast character streams as paste bursts and intentionally
  // makes a following Enter insert a newline. Wait past that suppression
  // window so this is a real submit gesture.
  await delay(250);
  await client.request('type_pane_via_ui', {
    sessionId,
    paneId: 'main',
    text: '\n',
  });
}

async function resolveCodexExecutable(configured) {
  const trimmed = String(configured || '').trim();
  if (trimmed) {
    return trimmed;
  }
  const { stdout } = await execFileAsync('/bin/sh', ['-lc', 'command -v codex']);
  const resolved = stdout.trim();
  if (!resolved) {
    throw new Error('codex executable not found in PATH');
  }
  return resolved;
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

function prepareIsolatedCodexExecutable(realExecutable) {
  const codexHome = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-real-codex-home-'));
  const sourceHome = path.join(os.homedir(), '.codex');
  const authFile = path.join(sourceHome, 'auth.json');
  const isolatedAuthFile = path.join(codexHome, 'auth.json');
  if (fs.existsSync(authFile)) {
    fs.copyFileSync(authFile, isolatedAuthFile);
    fs.chmodSync(isolatedAuthFile, 0o600);
  }

  const wrapperPath = path.join(codexHome, 'codex-real-wrapper.sh');
  fs.writeFileSync(
    wrapperPath,
    [
      '#!/bin/sh',
      `export CODEX_HOME=${shellQuote(codexHome)}`,
      `exec ${shellQuote(realExecutable)} -c features.apps=false -c features.enable_mcp_apps=false "$@"`,
      '',
    ].join('\n'),
    { mode: 0o700 }
  );
  return { codexHome, executable: wrapperPath, realExecutable };
}

function dataDirForProfile(profile) {
  const normalized = String(profile || '').trim().toLowerCase();
  if (!normalized || normalized === 'default') {
    return path.join(os.homedir(), '.attn');
  }
  return path.join(os.homedir(), `.attn-${normalized}`);
}

function dbPathForHarnessProfile() {
  return process.env.ATTN_DB_PATH || path.join(dataDirForProfile(currentHarnessProfile()), 'attn.db');
}

function sqlString(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

async function queryStoredResumeId(dbPath, sessionId) {
  const sql = `SELECT resume_session_id FROM sessions WHERE id = ${sqlString(sessionId)} LIMIT 1;`;
  try {
    const { stdout } = await execFileAsync('sqlite3', [dbPath, sql], { timeout: 5_000 });
    return stdout.trim();
  } catch {
    return '';
  }
}

async function waitForStoredResumeId(dbPath, sessionId, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastValue = '';
  while (Date.now() - startedAt < timeoutMs) {
    lastValue = await queryStoredResumeId(dbPath, sessionId);
    if (lastValue) {
      return lastValue;
    }
    await delay(250);
  }
  throw new Error(`Timed out waiting for stored Codex resume id in ${dbPath}; last value=${JSON.stringify(lastValue)}`);
}

async function waitForSessionNotWorking(observer, sessionId, timeoutMs = 20_000) {
  return observer.waitFor(() => {
    const session = observer.getSession(sessionId);
    if (!session) {
      return null;
    }
    return session.state !== 'working' ? session : null;
  }, `session ${sessionId} to leave working state`, timeoutMs);
}

function codexSessionsRoot(codexHome) {
  return path.join(codexHome, 'sessions');
}

function walkJsonlFiles(rootDir) {
  const files = [];
  const stack = [rootDir];
  while (stack.length > 0) {
    const current = stack.pop();
    let entries = [];
    try {
      entries = fs.readdirSync(current, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      const fullPath = path.join(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(fullPath);
      } else if (entry.isFile() && entry.name.endsWith('.jsonl')) {
        files.push(fullPath);
      }
    }
  }
  return files;
}

function readCodexSessionMeta(filePath) {
  let fd = null;
  try {
    fd = fs.openSync(filePath, 'r');
    const buffer = Buffer.alloc(64 * 1024);
    const bytes = fs.readSync(fd, buffer, 0, buffer.length, 0);
    const text = buffer.subarray(0, bytes).toString('utf8');
    for (const line of text.split('\n')) {
      if (!line.trim()) {
        continue;
      }
      const event = JSON.parse(line);
      if (event?.type === 'session_meta') {
        return event.payload || null;
      }
    }
  } catch {
    return null;
  } finally {
    if (fd !== null) {
      fs.closeSync(fd);
    }
  }
  return null;
}

function normalizeExistingPath(value) {
  const resolved = path.resolve(String(value || ''));
  try {
    return fs.realpathSync.native(resolved);
  } catch {
    return resolved;
  }
}

function findCodexTranscript({ codexHome, sessionId, cwd }) {
  const expectedCwd = normalizeExistingPath(cwd);
  for (const filePath of walkJsonlFiles(codexSessionsRoot(codexHome))) {
    const meta = readCodexSessionMeta(filePath);
    if (meta?.id === sessionId && normalizeExistingPath(meta.cwd) === expectedCwd) {
      return { filePath, meta };
    }
  }
  return null;
}

async function waitForCodexTranscript({ codexHome, sessionId, cwd, timeoutMs = 30_000 }) {
  const startedAt = Date.now();
  let found = null;
  while (Date.now() - startedAt < timeoutMs) {
    found = findCodexTranscript({ codexHome, sessionId, cwd });
    if (found) {
      return found;
    }
    await delay(500);
  }
  throw new Error(`Timed out waiting for Codex transcript id=${sessionId} cwd=${cwd}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-codex-resume-mapping.mjs');
    console.log(`Additional options:
  --codex-executable <path>   Real Codex executable to run (default: command -v codex)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-CODEX-RESUME',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-codex-resume-mapping',
    metadata: {
      agent: 'codex',
      focus: 'Real Codex native session id is preserved across app reload-session flow',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const dbPath = dbPathForHarnessProfile();
  let sessionId = null;
  let codexExecutable = null;
  let realCodexExecutable = null;
  let isolatedCodexHome = null;
  let nativeSessionId = null;
  let transcript = null;

  try {
    realCodexExecutable = await runner.step('resolve_real_codex_executable', async () => {
      return resolveCodexExecutable(options.codexExecutable);
    });
    const isolatedCodex = await runner.step('prepare_isolated_real_codex_home', async () => {
      return prepareIsolatedCodexExecutable(realCodexExecutable);
    });
    codexExecutable = isolatedCodex.executable;
    isolatedCodexHome = isolatedCodex.codexHome;

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('point_codex_setting_at_real_executable', async () => {
      await client.request('set_setting', {
        key: 'codex_executable',
        value: codexExecutable,
      });
    });

    sessionId = await runner.step('create_real_codex_session', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `codex-resume-${runner.runId}`,
        agent: 'codex',
        waitForMainVisible: false,
      });
    });

    nativeSessionId = await runner.step('assert_real_codex_hook_records_native_id', async () => {
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      const readiness = await ensureCodexMainPromptReady(client, sessionId, 60_000);
      runner.writeJson('codex-readiness.json', readiness);
      await submitCodexPromptViaUi(client, sessionId, 'Reply with exactly: ok');
      await delay(1000);
      runner.writeJson('codex-after-submit.json', await client.request('read_pane_text', {
        sessionId,
        paneId: 'main',
      }));
      const resumeId = await waitForStoredResumeId(dbPath, sessionId, 45_000);
      runner.assert(resumeId !== sessionId, 'stored resume id is Codex native id, not attn wrapper id', {
        attnSessionId: sessionId,
        resumeId,
      });
      return resumeId;
    });

    transcript = await runner.step('assert_resume_id_matches_real_codex_transcript', async () => {
      const found = await waitForCodexTranscript({
        codexHome: isolatedCodexHome,
        sessionId: nativeSessionId,
        cwd: runner.sessionDir,
        timeoutMs: 45_000,
      });
      runner.writeJson('codex-transcript.json', found);
      const stoppedSession = await waitForSessionNotWorking(observer, sessionId, 20_000);
      runner.assert(stoppedSession.state !== 'working', 'real Codex session is not green after the turn stops', {
        state: stoppedSession.state,
      });
      await captureSessionArtifacts(client, runner.runDir, '01-first-launch', sessionId);
      return found;
    });

    await runner.step('reload_session_from_ui_automation', async () => {
      await client.request('reload_session', { sessionId }, { timeoutMs: 45_000 });
      await observer.waitForSession({ id: sessionId, timeoutMs: 20_000 });
    });

    await runner.step('assert_reload_preserves_real_codex_resume_id', async () => {
      const resumeIdAfterReload = await waitForStoredResumeId(dbPath, sessionId, 30_000);
      runner.assert(resumeIdAfterReload === nativeSessionId, 'reload keeps the same real Codex native resume id', {
        nativeSessionId,
        resumeIdAfterReload,
      });
      const reloadedSession = await waitForSessionNotWorking(observer, sessionId, 20_000);
      runner.assert(reloadedSession.state !== 'working', 'reloaded stopped Codex session is not green', {
        state: reloadedSession.state,
      });
      const transcriptAfterReload = await waitForCodexTranscript({
        codexHome: isolatedCodexHome,
        sessionId: nativeSessionId,
        cwd: runner.sessionDir,
        timeoutMs: 30_000,
      });
      runner.assert(transcriptAfterReload.filePath === transcript.filePath, 'reload still points at the original real Codex transcript', {
        before: transcript.filePath,
        after: transcriptAfterReload.filePath,
      });
      await captureSessionArtifacts(client, runner.runDir, '02-after-reload', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      codexExecutable: realCodexExecutable,
      dbPath,
      nativeSessionId,
      transcriptPath: transcript?.filePath || null,
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, {
      sessionId,
      codexExecutable: realCodexExecutable,
      dbPath,
      nativeSessionId,
      transcriptPath: transcript?.filePath || null,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    try {
      await client.request('set_setting', {
        key: 'codex_executable',
        value: '',
      }, { timeoutMs: 10_000 });
    } catch {}
    try {
      await cleanupSessionViaAppClose(client, observer, sessionId);
    } catch {}
    try {
      if (isolatedCodexHome) {
        fs.rmSync(isolatedCodexHome, { recursive: true, force: true });
      }
    } catch {}
    try {
      await client.quitApp();
    } catch {}
    try {
      await observer.close();
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
