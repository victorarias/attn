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

// --- Focus-free readiness (PTY-driven) -----------------------------------
//
// The ensure*InitialPanePromptReady helpers above gate on the pane acquiring
// DOM *input focus* (click_pane + waitForPaneInputFocus). That focus state
// reflects the ghostty terminal's real focus/blur events, which WebKit only
// delivers when the app is the macOS *key window*. The real-app harness keeps
// the app parked in the background (driver.activateBackground / window_park,
// "without changing frontmost"), so a backgrounded scenario can never satisfy
// that gate — the pane renders fine and reads fine, but never reports focus.
//
// These variants reach the same "agent is at its interactive prompt" state
// WITHOUT requiring focus. Every interaction (dismissing the trust gate, the
// Codex update chooser) is a `write_pane`, which goes straight to the worker
// PTY's stdin via sendRuntimeInput — exactly the bytes a human's keystrokes
// become, independent of which window is key. Use these for scenarios that
// also DRIVE the agent via write_pane (PTY-direct) rather than synthetic
// keystrokes; keep the focus-gated variants for scenarios that test
// keystroke/UI input, where focus genuinely matters.

// Answer a TUI menu (trust gate, update chooser) by writing the choice + CR
// straight to the PTY. A TUI reading stdin sees this identically to a real
// keypress, so no DOM focus is needed.
async function answerPaneMenuViaPty(client, sessionId, paneId, choice) {
  await client.request('write_pane', { sessionId, paneId, text: `${choice}\r`, submit: false });
}

async function ensureAgentPromptReadyViaPty(client, sessionId, { label, isReady, gates, timeoutMs }) {
  const startedAt = Date.now();
  const handled = [];

  while (Date.now() - startedAt < timeoutMs) {
    // select_session only changes which workspace is shown; it does not steal
    // OS focus, so it is safe for a backgrounded app. It keeps read_pane_text
    // resolving against the right workspace view.
    await client.request('select_session', { sessionId });
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for ${label} session ${sessionId}`, 20_000);
    await waitForPaneVisible(client, sessionId, initialPane.paneId, 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (isReady(text)) {
      return { paneId: initialPane.paneId, text, handled };
    }

    const gate = gates.find((entry) => entry.match(text));
    if (gate) {
      await answerPaneMenuViaPty(client, sessionId, initialPane.paneId, gate.choice);
      handled.push(gate.name);
      await delay(500);
      continue;
    }

    await delay(300);
  }

  throw new Error(`Timed out waiting for ${label} prompt readiness in session ${sessionId} (focus-free / PTY)`);
}

// Focus-free Claude readiness. Pre-trust the folder (preTrustClaudeFolder)
// before launch and the trust gate normally never appears; this still handles
// it defensively via PTY write in case it does.
export async function ensureClaudePromptReadyViaPty(client, sessionId, timeoutMs = 60_000) {
  return ensureAgentPromptReadyViaPty(client, sessionId, {
    label: 'Claude',
    isReady: hasClaudePrompt,
    gates: [{ name: 'trust', match: hasTrustPrompt, choice: '1' }],
    timeoutMs,
  });
}

// Focus-free Codex readiness. Codex has no pre-trust path, so the trust gate
// (choice 1) and the "Update available!" chooser (choice 3 = skip until next
// version) are both dismissed here via PTY write.
export async function ensureCodexPromptReadyViaPty(client, sessionId, timeoutMs = 60_000) {
  return ensureAgentPromptReadyViaPty(client, sessionId, {
    label: 'Codex',
    isReady: hasCodexPrompt,
    gates: [
      { name: 'trust', match: hasTrustPrompt, choice: '1' },
      { name: 'update', match: hasCodexUpdatePrompt, choice: '3' },
    ],
    timeoutMs,
  });
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
  // Claude Code treats a rapid multi-line write_pane as a paste, so a trailing \r
  // in the same call inserts a newline instead of submitting. Write the prompt,
  // wait a beat, then submit with a lone \r — the same doorbell pattern Codex
  // needs mid-turn.
  await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: prompt, submit: false });
  await delay(500);
  await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });

  // Wait for the reply to actually render: the input box echoes the prompt (up
  // to lineCount occurrences of token pre-submit), so only count it submitted
  // once the token count exceeds that — the echoed user message contributes at
  // least one more occurrence and the reply's exact lines contribute lineCount.
  const replyTimeoutMs = 45_000;
  const startedAt = Date.now();
  let lastText = '';
  while (Date.now() - startedAt < replyTimeoutMs) {
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    lastText = pane?.text || '';
    const occurrences = lastText.split(token).length - 1;
    if (occurrences >= lineCount + 1) {
      return { prompt, expectedLines: lines, paneId: initialPane.paneId };
    }
    await delay(1_000);
  }

  throw new Error(`Timed out waiting for Claude structured block reply for ${token} in session ${sessionId}`);
}
