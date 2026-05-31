import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  waitForFirstWorkspacePane,
  waitForPaneInputFocus,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';

function compact(text) {
  return text.replace(/\s+/g, '');
}

// Claude Code stores per-project state in ~/.claude.json under
// `projects.<absolute-path>`. Pre-marking a folder as trusted before launch
// skips the "Do you trust this folder?" gating, which is brittle to detect
// (wording drifts across Claude releases) and slow to dismiss via type-and-
// enter. The harness session dir is fresh per run, so writing this entry
// can't shadow a real user-curated trust decision.
export function preTrustClaudeFolder(folderPath) {
  // Claude indexes projects by the realpath of cwd, so on macOS where the
  // harness session dirs live under /var/folders/... (a symlink to
  // /private/var/folders/...) we have to resolve symlinks before keying
  // the config; otherwise our entry sits next to Claude's canonical entry
  // and never gets read.
  const resolvedFolder = path.resolve(folderPath);
  const absoluteFolder = (() => {
    try {
      return fs.realpathSync(resolvedFolder);
    } catch {
      return resolvedFolder;
    }
  })();
  const configPath = path.join(os.homedir(), '.claude.json');

  let config = {};
  // Carry forward the existing file mode so we don't silently downgrade a
  // `0600` config to whatever the umask gives us (default `0644`). Claude's
  // config can hold account/session metadata, so widening read permission
  // would be a real footgun. If the file is new, default to `0600`.
  let fileMode = 0o600;
  try {
    const raw = fs.readFileSync(configPath, 'utf8');
    if (raw.trim()) {
      config = JSON.parse(raw);
    }
    fileMode = fs.statSync(configPath).mode & 0o777;
  } catch (error) {
    if (error.code !== 'ENOENT') {
      throw error;
    }
  }

  if (!config.projects || typeof config.projects !== 'object') {
    config.projects = {};
  }
  const existing = config.projects[absoluteFolder] && typeof config.projects[absoluteFolder] === 'object'
    ? config.projects[absoluteFolder]
    : {};
  config.projects[absoluteFolder] = {
    ...existing,
    hasTrustDialogAccepted: true,
    hasCompletedProjectOnboarding: true,
  };

  // Write atomically: writing through a temp file keeps the 90+KB user
  // config from being truncated if the harness is killed mid-write.
  const tmpPath = `${configPath}.harness-${process.pid}-${Date.now()}`;
  fs.writeFileSync(tmpPath, JSON.stringify(config, null, 2), { encoding: 'utf8', mode: fileMode });
  fs.renameSync(tmpPath, configPath);
  return absoluteFolder;
}

function hasTrustPrompt(text) {
  return (
    text.includes('Do you trust this folder?')
    || text.includes('Do you trust the contents of this directory?')
    || text.includes('Working with untrusted contents')
    || text.includes('Security guide')
  );
}

function hasClaudePrompt(text) {
  if (hasTrustPrompt(text)) {
    return false;
  }
  return /(^|\n)\s*❯(?:\s|$)/u.test(text);
}

function hasCodexPrompt(text) {
  if (hasTrustPrompt(text)) {
    return false;
  }
  if (hasCodexUpdatePrompt(text)) {
    return false;
  }
  return (
    text.includes('OpenAI Codex')
    || text.includes('/model to change')
    || text.includes('100% left')
  );
}

// Codex sometimes interrupts startup with an "Update available!" chooser
// (1 = update now, 2 = skip, 3 = skip until next version). If we don't
// dismiss it, the usual `hasCodexPrompt` signals never arrive and the
// scenario times out. We send "3" so the remote stops asking until the
// next release.
function hasCodexUpdatePrompt(text) {
  return (
    text.includes('Update available!')
    && text.includes('Skip until next version')
  );
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function ensureClaudeInitialPanePromptReady(client, sessionId, timeoutMs = 40_000) {
  const startedAt = Date.now();
  let trustHandled = false;

  while (Date.now() - startedAt < timeoutMs) {
    await client.request('select_session', { sessionId });
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for Claude session ${sessionId}`, 20_000);
    await waitForPaneVisible(client, sessionId, initialPane.paneId, 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (hasTrustPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: initialPane.paneId, text: '1' });
      await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });
      trustHandled = true;
      await delay(500);
      continue;
    }

    if (hasClaudePrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      return { trustHandled, paneId: initialPane.paneId, text };
    }
  }

  throw new Error(`Timed out waiting for Claude prompt readiness in session ${sessionId}`);
}

export async function ensureCodexInitialPanePromptReady(client, sessionId, timeoutMs = 40_000) {
  const startedAt = Date.now();
  let trustHandled = false;
  let updatePromptHandled = false;

  while (Date.now() - startedAt < timeoutMs) {
    await client.request('select_session', { sessionId });
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for Codex session ${sessionId}`, 20_000);
    await waitForPaneVisible(client, sessionId, initialPane.paneId, 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (hasTrustPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: initialPane.paneId, text: '1' });
      await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });
      trustHandled = true;
      await delay(500);
      continue;
    }

    if (hasCodexUpdatePrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: initialPane.paneId, text: '3' });
      await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });
      updatePromptHandled = true;
      await delay(500);
      continue;
    }

    if (hasCodexPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      return { trustHandled, updatePromptHandled, paneId: initialPane.paneId, text };
    }

    await delay(300);
  }

  throw new Error(`Timed out waiting for Codex prompt readiness in session ${sessionId}`);
}

export async function promptClaudeForStructuredBlock(client, sessionId, token, lineCount = 8) {
  const lines = Array.from({ length: lineCount }, (_, index) =>
    `${token} line ${index + 1} render width coverage verification payload ${index + 1} for split stability`
  );
  const prompt = [
    'Reply with exactly the following lines and nothing else.',
    'Preserve uppercase letters, digits, and spacing exactly.',
    'Do not add a preamble.',
    'Do not use a code block.',
    ...lines,
  ].join('\n');

  const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for Claude prompt ${sessionId}`, 20_000);
  await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
  await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
  await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: `${prompt}\r`, submit: false });

  return { prompt, expectedLines: lines, paneId: initialPane.paneId };
}
