#!/usr/bin/env node

// Terminal annotations end to end in the packaged app: a real claude turn on a
// real WebGL grid, an alt-drag that resolves through the markdown/grid
// alignment to an anchor, and the two guarantees the unit tests can only claim
// about mocks —
//
//   1. the annotation is the daemon's, not the pane's, so it survives the app
//      being quit and relaunched, and still resolves onto live rows there;
//   2. a second turn does not take the first turn's annotation away.
//
// Every assertion is wording-independent. The agent is asked for plain prose,
// but what it actually says is read off the grid and carried through the run,
// so agent discretion changes the quote rather than failing the scenario.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import WebSocket from 'ws';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  relaunchAppAndConnect,
} from './common.mjs';
import { dataDirForProfile } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
} from './scenarioAssertions.mjs';
import { ensureClaudePromptReadyViaPty, preTrustClaudeFolder } from './scenarioAgents.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

// Generous on purpose: how long a live agent takes to stop says nothing about
// whether an annotation survived it. A tight bound here turns agent discretion
// into a failure that reads like a product regression.
const SETTLE_TIMEOUT_MS = 240_000;
const TURN_START_TIMEOUT_MS = 90_000;
// The window is fetched when the turn settles; this covers that round trip and
// the repaint, not the agent.
const ANCHOR_TIMEOUT_MS = 30_000;
const SETTLED_STATES = new Set(['idle', 'waiting_input', 'pending_approval']);

// The label clicked in the popup. Its aria-label is the button's accessible
// name in QUICK_LABELS; its emoji is what the filed annotation carries.
const LABEL = { name: 'Show the receipt', emoji: '🧾' };
// The note drafted beside the marks, and what proves it left the composer.
const NOTE = 'Prefer the smallest change that covers this.';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs) {
  const startedAt = Date.now();
  let lastError = null;
  while (Date.now() - startedAt < timeoutMs) {
    try {
      const value = await fn();
      if (value !== null && value !== undefined && value !== false) return value;
    } catch (error) {
      lastError = error;
    }
    await sleep(500);
  }
  throw new Error(`Timed out waiting for ${description} after ${timeoutMs}ms${lastError ? `: ${lastError.message}` : ''}`);
}

// Claude treats a fast multi-line write as a paste, so the submit has to be a
// lone carriage return a beat later.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await sleep(600);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

function prosePrompt(topic) {
  return [
    `In two or three plain sentences, and using no tools and no files, explain ${topic}.`,
    'Answer in prose only — no lists, no code, no headings.',
  ].join(' ');
}

// A submitted prompt does not immediately leave the settled state it was typed
// in, so waiting only for "settled" returns before the turn has even opened and
// every later read is of the previous turn. Wait for the turn to open, then for
// it to close.
async function waitForTurn(observer, sessionId, description) {
  await pollFor(
    () => (observer.getSession(sessionId)?.state === 'working' ? true : null),
    `${description} to start`,
    TURN_START_TIMEOUT_MS,
  );
  return pollFor(
    () => {
      const state = observer.getSession(sessionId)?.state;
      return SETTLED_STATES.has(state) ? state : null;
    },
    `${description} to settle`,
    SETTLE_TIMEOUT_MS,
  );
}

// Whether `inner` lies entirely within `outer`, with a pixel of slack for the
// subpixel rounding a real window produces.
function contains(outer, inner) {
  return inner.left >= outer.left - 1
    && inner.top >= outer.top - 1
    && inner.left + inner.width <= outer.left + outer.width + 1
    && inner.top + inner.height <= outer.top + outer.height + 1;
}

function overlaps(a, b) {
  return a.left < b.left + b.width && b.left < a.left + a.width
    && a.top < b.top + b.height && b.top < a.top + a.height;
}

// Alt-drag over the agent's prose until it resolves to an anchor. Returns the
// row it landed on, which every later assertion is about.
async function pollForDrag(client, runner, sessionId, paneId, description) {
  let lastRow = null;
  let lastSpan = null;
  const attempt = async () => {
    const target = proseRow(await readLines(client, sessionId, paneId));
    if (!target) return null;
    const span = wordSpan(target.text);
    if (!span) return null;
    lastRow = target;
    lastSpan = span;
    await client.request('drag_pane_selection', {
      sessionId,
      paneId,
      start: { col: span.startCol, row: target.row },
      end: { col: span.endCol, row: target.row },
      altKey: true,
    });
    const state = await client.request('get_annotation_state', {});
    return state.popupOpen ? { row: target.row, rowText: target.text, span } : null;
  };
  try {
    return await pollFor(attempt, `an alt-drag on ${description} to resolve to an anchor`, ANCHOR_TIMEOUT_MS);
  } catch (error) {
    runner.assert(
      false,
      `Alt-drag over ${description}'s prose never resolved to an anchor, so the grid and the `
      + `transcript did not line up. Last try: row ${lastRow?.row} cols ${lastSpan?.startCol}-`
      + `${lastSpan?.endCol} of ${JSON.stringify(lastRow?.text)}. ${error.message}`,
    );
    throw error;
  }
}

// The daemon's own copy, read over a fresh socket rather than through the UI.
// The point of the whole change is that these two agree without the pane
// holding anything, so asking the daemon directly is what makes that testable.
function daemonAnnotations(wsUrl, sessionId, timeoutMs = 8_000) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    const requestId = `harness-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
    const timer = setTimeout(() => {
      try { ws.close(); } catch { /* already closing */ }
      reject(new Error(`session_annotations_get timed out for ${sessionId}`));
    }, timeoutMs);
    ws.once('open', () => {
      ws.send(JSON.stringify({
        cmd: 'client_hello',
        client_kind: 'harness-observer',
        version: 'real-app-harness',
        capabilities: ['workspace_sessions'],
      }));
      ws.send(JSON.stringify({ cmd: 'session_annotations_get', session_id: sessionId, request_id: requestId }));
    });
    ws.on('message', (raw) => {
      const data = JSON.parse(raw.toString());
      // A malformed hello, an unknown command, or a rejected capability comes
      // back as a plain error event; without this the mistake reads as a
      // timeout and looks like the daemon lost the annotations.
      if (data.event === 'error') {
        clearTimeout(timer);
        ws.close();
        reject(new Error(`daemon rejected session_annotations_get: ${data.error || JSON.stringify(data)}`));
        return;
      }
      if (data.event !== 'session_annotations_get_result' || data.request_id !== requestId) return;
      clearTimeout(timer);
      ws.close();
      if (!data.success) {
        reject(new Error(data.error || `session_annotations_get failed for ${sessionId}`));
        return;
      }
      resolve({ annotations: data.annotations || [], note: data.note ?? '', generation: data.generation ?? 0 });
    });
    ws.once('error', (error) => {
      clearTimeout(timer);
      reject(error);
    });
  });
}

// The agent's prose on the grid. Claude renders an assistant message as a "⏺ "
// line plus two-space-indented continuations; the widest continuation is the
// safest row to drag across because it is the least likely to be a wrapped
// fragment of something else. Returns null until such a row exists.
export function proseRow(lines) {
  let best = null;
  // Claude draws on the alternate screen, which has no scrollback, so an answer
  // taller than the grid pushes its own "⏺ " marker off the top and leaves only
  // continuations behind. Those rows are still the agent talking and still what
  // a user would drag across, so the indented run before the first marker line
  // counts as in-block; requiring a visible marker made the scenario depend on
  // the answer being short enough to fit.
  let inBlock = true;
  for (let row = 0; row < lines.length; row += 1) {
    const line = lines[row];
    if (line.startsWith('⏺ ')) {
      // Tool calls render as "⏺ Bash(...)"; only prose is annotatable.
      inBlock = !/^⏺ \w+\(/.test(line);
      continue;
    }
    if (!inBlock) continue;
    if (!line.startsWith('  ') || line.trim() === '') {
      inBlock = false;
      continue;
    }
    // A row worth dragging holds several whole words and no box drawing.
    const words = line.trim().split(/\s+/).filter((word) => /^[A-Za-z][A-Za-z'-]*[.,;:]?$/.test(word));
    if (words.length < 6) continue;
    if (!best || line.trim().length > best.text.trim().length) best = { row, text: line, words };
  }
  return best;
}

// The widest row carrying real text, whatever it is. Used before the agent has
// said anything, when the point is not which row is dragged over but that a
// drag over any of them is refused out loud — nothing on the grid at that
// moment belongs to a transcript message.
export function anyTextRow(lines) {
  let best = null;
  for (let row = 0; row < lines.length; row += 1) {
    const line = lines[row];
    const words = line.trim().split(/\s+/).filter((word) => /^[A-Za-z][A-Za-z'-]*[.,;:]?$/.test(word));
    if (words.length < 5) continue;
    if (!best || line.trim().length > best.text.trim().length) best = { row, text: line, words };
  }
  return best;
}

// A whole-word column span inside a row, away from both edges so the drag
// cannot pick up the indent or a wrapped word.
export function wordSpan(rowText) {
  const trimmedStart = rowText.length - rowText.trimStart().length;
  const words = [...rowText.matchAll(/[A-Za-z][A-Za-z'-]*/g)]
    .filter((match) => match.index >= trimmedStart);
  if (words.length < 5) return null;
  const first = words[1];
  const last = words[Math.min(words.length - 2, 4)];
  return { startCol: first.index, endCol: last.index + last[0].length };
}

async function readLines(client, sessionId, paneId) {
  const read = await client.request('read_pane_text', { sessionId, paneId });
  return (read.text ?? '').split('\n');
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-annotations.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-ANNOTATIONS',
    tier: 'tier3-local-agent',
    prefix: 'terminal-annotations',
    metadata: {
      focus: 'alt-drag anchors to the transcript; a past turn stays annotated and the annotations outlive the app',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  // ⌘Enter is delivered by the page's keydown listener, and whether it reaches
  // that listener at all — rather than being eaten by AppKit or handed to the
  // PTY — is the part only a real keystroke into the packaged app can show.
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;
  let paneId = null;
  // What the agent said, and where it sits on the grid. Carried through the
  // rest of the run so every later assertion is about this exact text, and
  // named in the failure report when a step goes wrong.
  let anchoredRowText = null;
  let quote = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, wsUrl: options.wsUrl });

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const pane of workspace?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    }
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_agent_session', async () => {
      const cwd = path.join(runner.sessionDir, 'annotated');
      fs.mkdirSync(cwd, { recursive: true });
      preTrustClaudeFolder(cwd);
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `annotate-${runner.runId}`,
        agent: 'claude',
        sessionWaitMs: 60_000,
        promptReadyFn: ensureClaudePromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      await client.request('select_session', { sessionId });
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'agent pane', 20_000);
      paneId = pane.paneId;
    });

    // Before the agent has said anything there is nothing to annotate, and the
    // whole failure mode this covers is that the gesture used to do exactly
    // nothing about it — no popup, no message, no log line, indistinguishable
    // from the feature being broken. Runs first because "no window yet" is only
    // deterministic before the first turn.
    await runner.step('a_refused_drag_says_why', async () => {
      // Settle first. A session still coming up is refused for a different and
      // equally correct reason ("annotations open once the agent stops
      // talking"), and pinning whichever one the timing produced would make
      // this step a race rather than a check.
      await pollFor(
        () => (SETTLED_STATES.has(observer.getSession(sessionId)?.state) ? true : null),
        'the new session to settle at its prompt',
        60_000,
      );

      const refused = await pollFor(
        async () => {
          const target = anyTextRow(await readLines(client, sessionId, paneId));
          if (!target) return null;
          const span = wordSpan(target.text);
          if (!span) return null;
          await client.request('drag_pane_selection', {
            sessionId,
            paneId,
            start: { col: span.startCol, row: target.row },
            end: { col: span.endCol, row: target.row },
            altKey: true,
          });
          // Read straight away: the notice retires itself after a few seconds,
          // and polling around this request would race its own dismissal.
          const state = await client.request('get_annotation_state', {});
          return state.notice ? { state, target } : null;
        },
        'an alt-drag before the first turn to be refused out loud',
        20_000,
      );

      runner.assert(
        !refused.state.popupOpen,
        `A drag before the agent said anything opened an annotation editor: ${JSON.stringify(refused.state)}`,
      );
      // A session that has never taken a turn has no transcript on disk yet, so
      // the identity ladder resolves nothing — the same outcome as a session
      // whose transcript was cleaned up, which is what this is standing in for.
      runner.assert(
        /No transcript could be read/.test(refused.state.notice),
        `The refusal did not name its reason: ${JSON.stringify(refused.state.notice)}`,
      );
      const { noticeRect: box, viewport } = refused.state;
      runner.assert(
        box && box.left >= 0 && box.top >= 0
        && box.left + box.width <= viewport.width && box.top + box.height <= viewport.height,
        `The refusal was drawn off-screen, where it explains nothing: `
        + `${JSON.stringify(box)} in ${JSON.stringify(viewport)}`,
      );
      runner.log('refused', { row: refused.target.row, notice: refused.state.notice });

      // The other half of the fix: the daemon used to return this outcome and
      // log nothing, so a user reporting "I cannot annotate" left no trace to
      // diagnose from. The line names the session and where it was running.
      const logPath = path.join(dataDirForProfile(), 'daemon.log');
      const logged = fs.existsSync(logPath)
        ? fs.readFileSync(logPath, 'utf8').split('\n').filter((line) => line.includes(sessionId) && line.includes('no transcript resolved'))
        : [];
      runner.assert(
        logged.length > 0,
        `The daemon refused the message window without logging it, so the cause is invisible outside the app (${logPath})`,
      );

      // It must retire on its own — it explains a finished gesture, and one
      // that lingers becomes a thing in the way of the next drag.
      await sleep(6_000);
      const after = await client.request('get_annotation_state', {});
      runner.assert(
        after.notice === null,
        `The refusal was still on screen 6s later: ${JSON.stringify(after.notice)}`,
      );
    });

    await runner.step('first_turn_and_annotate', async () => {
      await submitPrompt(client, sessionId, paneId, prosePrompt('what a retry wrapper around a network call should do about idempotency'));
      await waitForTurn(observer, sessionId, 'the first turn');

      // Prose reaches the grid while the agent is still writing, but the
      // annotatable window is only read once the turn settles — mid-turn the
      // message is incomplete and its offsets would move. So the drag is the
      // poll: retry until one resolves, rather than guessing how long after
      // the settle the window lands.
      const anchored = await pollForDrag(client, runner, sessionId, paneId, 'the first turn');
      anchoredRowText = anchored.rowText;

      await client.request('dom_click', { selector: `[aria-label="${LABEL.name}"]` });
      const filed = await pollFor(
        async () => {
          const state = await client.request('get_annotation_state', {});
          return state.annotations?.length === 1 ? state : null;
        },
        'the label to file exactly one annotation',
        10_000,
      );
      quote = filed.annotations[0].quote;
      runner.assert(quote.trim().length > 0, `Filed annotation has an empty quote: ${JSON.stringify(filed.annotations[0])}`);
      runner.assert(
        filed.annotations[0].emoji === LABEL.emoji,
        `Annotation emoji = ${JSON.stringify(filed.annotations[0].emoji)}, want ${LABEL.emoji}`,
      );
      // The anchor is offsets into the transcript, so the quote is the
      // agent's markdown — which is what the row renders, modulo the wrap.
      const quotedWords = quote.trim().split(/\s+/);
      const rowWords = new Set(anchoredRowText.trim().split(/\s+/));
      const missing = quotedWords.filter((word) => !rowWords.has(word));
      runner.assert(
        missing.length === 0,
        `Quote is not the text that was dragged over. Quote ${JSON.stringify(quote)}, `
        + `row ${JSON.stringify(anchoredRowText)}, words not on the row: ${JSON.stringify(missing)}`,
      );
      runner.log('annotated', { quote, row: anchored.row, span: anchored.span });
    });

    await runner.step('daemon_holds_it', async () => {
      const stored = await pollFor(
        async () => {
          const result = await daemonAnnotations(options.wsUrl, sessionId);
          return result.annotations.length === 1 ? result : null;
        },
        'the daemon to hold the annotation',
        10_000,
      );
      const [annotation] = stored.annotations;
      runner.assert(annotation.quote === quote, `Daemon quote ${JSON.stringify(annotation.quote)} != UI quote ${JSON.stringify(quote)}`);
      runner.assert(
        typeof annotation.message_key === 'string' && annotation.message_key.length > 0,
        `Stored annotation has no message key: ${JSON.stringify(annotation)}`,
      );
      runner.assert(stored.generation >= 1, `Stored generation = ${stored.generation}, want at least 1`);
    });

    await runner.step('survives_app_relaunch', async () => {
      await relaunchAppAndConnect(client, observer);
      await client.request('select_session', { sessionId });
      const state = await pollFor(
        async () => {
          const current = await client.request('get_annotation_state', {});
          return current.annotations?.length === 1 ? current : null;
        },
        'the relaunched app to hydrate the annotation from the daemon',
        30_000,
      );
      runner.assert(
        state.annotations[0].quote === quote,
        `Hydrated a different annotation than was stored: ${JSON.stringify(state.annotations)}`,
      );
      const workspace = await client.request('get_workspace', { sessionId });
      paneId = workspace?.panes?.[0]?.paneId ?? paneId;
    });

    // The annotation is only worth keeping if it can still be found on the
    // message. Scroll the first turn back into view and alt-click the wash: it
    // reopens only when the projection resolved those rows.
    await runner.step('still_projects_onto_the_grid', async () => {
      const found = await pollFor(
        async () => {
          const lines = await readLines(client, sessionId, paneId);
          const row = lines.findIndex((line) => line === anchoredRowText);
          if (row >= 0) return { row, lines };
          await client.request('wheel_pane', { sessionId, paneId, deltaY: -300 });
          return null;
        },
        `the annotated row ${JSON.stringify(anchoredRowText)} to be scrolled back into view`,
        30_000,
      );
      const span = wordSpan(anchoredRowText);
      const col = Math.floor((span.startCol + span.endCol) / 2);
      // A zero-width alt-drag is the alt-click the terminal reports as an
      // activation of whatever wash is under the pointer.
      await client.request('drag_pane_selection', {
        sessionId,
        paneId,
        start: { col, row: found.row },
        end: { col, row: found.row },
        altKey: true,
      });
      const state = await client.request('get_annotation_state', {});
      runner.assert(
        state.popupOpen,
        `Alt-clicking the wash on the first turn did not reopen it, so the annotation no longer `
        + `resolves onto live rows. Row ${found.row} col ${col}: ${JSON.stringify(anchoredRowText)}`,
      );
      runner.assert(
        state.annotations?.length === 1,
        `Reopening changed the set: ${JSON.stringify(state.annotations)}`,
      );
    });

    // The regression this whole change exists to prevent: the pane used to
    // throw its annotations away the moment the agent said anything else.
    //
    // This runs after the grid check, not before it. A second turn pushes the
    // first one up, and a long enough answer pushes it out of the scrollback
    // the restore preserves — at which point there are no rows left to project
    // onto and declining to paint is the containment gate working, not a lost
    // annotation. Surviving a new turn is a claim about the panel and the
    // daemon, so assert it there and leave the grid out of it.
    await runner.step('survives_a_second_turn', async () => {
      await submitPrompt(client, sessionId, paneId, prosePrompt('what a circuit breaker is and when it beats a retry'));
      await waitForTurn(observer, sessionId, 'the second turn');

      const state = await client.request('get_annotation_state', {});
      runner.assert(
        state.annotations?.length === 1 && state.annotations[0].quote === quote,
        `A new turn lost the annotation on the previous one. Wanted quote ${JSON.stringify(quote)}, `
        + `panel now: ${JSON.stringify(state.annotations)}`,
      );
      const stored = await daemonAnnotations(options.wsUrl, sessionId);
      runner.assert(
        stored.annotations.length === 1 && stored.annotations[0].quote === quote,
        `The daemon lost the annotation across a turn: ${JSON.stringify(stored)}`,
      );
    });

    // The editor is positioned against the window, but it is only usable
    // inside the pane it annotates: the sidebar and the neighbouring panes
    // paint over anything that reaches them. A drag on the first words of a
    // line anchors it at the pane's left edge, which is where it used to be
    // drawn half under the sidebar.
    await runner.step('the_editor_lands_inside_its_pane', async () => {
      await pollForDrag(client, runner, sessionId, paneId, 'the second turn');
      const state = await pollFor(
        async () => {
          const current = await client.request('get_annotation_state', {});
          return current.popupOpen && current.popupRect ? current : null;
        },
        'the editor to report its geometry',
        5_000,
      );
      const { popupRect, panelRect, paneRects, viewport } = state;
      runner.assert(
        Array.isArray(paneRects) && paneRects.length > 0,
        'No terminal grid reported its geometry, so this step can prove nothing',
      );
      runner.assert(
        paneRects.some((pane) => contains(pane, popupRect)),
        `The annotation editor was drawn outside every terminal pane, where the app's chrome covers it: `
        + `popup ${JSON.stringify(popupRect)}, panes ${JSON.stringify(paneRects)}, viewport ${JSON.stringify(viewport)}`,
      );
      // The other half: the panel is drawn over the editor, so an editor that
      // lands under it is on screen and unreachable.
      runner.assert(
        !panelRect || !overlaps(panelRect, popupRect),
        `The annotation editor was drawn under the annotations panel: `
        + `popup ${JSON.stringify(popupRect)}, panel ${JSON.stringify(panelRect)}`,
      );
    });

    // The panel is the list of what you wrote, so a row in it is clicked to
    // change what it says — and once it says something, the control that
    // removes it has to still be on the row rather than pushed below it.
    await runner.step('a_panel_row_opens_its_editor_and_keeps_its_remove_control', async () => {
      // Close the popup the previous step opened; a press outside is how a
      // user dismisses it, and it must not disturb a labelled annotation.
      await client.request('dom_click', { selector: '[data-testid="annotation-panel"] .anno-panel-title' });
      await sleep(300);

      await client.request('dom_click', { selector: '.anno-card-open' });
      const opened = await pollFor(
        async () => {
          const state = await client.request('get_annotation_state', {});
          return state.popupOpen && state.popupDraft !== null ? state : null;
        },
        'the panel row to open its comment editor',
        5_000,
      );
      runner.assert(
        opened.commentFocused,
        'The editor opened without the caret in its comment box, so the next sentence typed goes to the PTY',
      );

      const comment = 'checked against the real behaviour';
      await client.request('dom_type', { selector: '.anno-popup-text', text: comment });
      await client.request('dom_click', { selector: '.anno-popup-save' });

      const withComment = await pollFor(
        async () => {
          const state = await client.request('get_annotation_state', {});
          return state.annotations?.[0]?.comment === comment ? state : null;
        },
        'the comment to land on the panel row',
        5_000,
      );
      const [row] = withComment.annotations;
      runner.assert(
        row.rect && row.removeRect,
        `Panel row reported no geometry: ${JSON.stringify(row)}`,
      );
      // A comment wraps inside the row. The remove control must still start on
      // the row's first line — it used to be auto-placed after the comment,
      // which put it on a line of its own exactly when there was text to
      // remove. 12px is a tripwire, not a measurement of the real 5px offset.
      const offset = row.removeRect.top - row.rect.top;
      runner.assert(
        offset <= 12,
        `The remove control starts ${offset}px below the row's top, so a commented row pushed it onto its own line `
        + `(row ${JSON.stringify(row.rect)}, control ${JSON.stringify(row.removeRect)})`,
      );
      runner.assert(
        row.removeRect.left + row.removeRect.width <= row.rect.left + row.rect.width + 1,
        `The remove control overflows its row: ${JSON.stringify(row.removeRect)} vs ${JSON.stringify(row.rect)}`,
      );
      // Leave the surface as the user would: nothing open over the panel.
      await client.request('dom_click', { selector: '[data-testid="annotation-panel"] .anno-panel-title' });
      await sleep(300);
    });

    // The note is the sentence the marks qualify — "let's do x and y", with the
    // marks saying where. It is drafted in the panel and goes out with them, so
    // it is held by the daemon like they are and spent by the same send.
    await runner.step('a_note_is_drafted_beside_the_marks', async () => {
      await client.request('dom_type', { selector: '.anno-panel-note', text: NOTE });

      const held = await pollFor(
        async () => {
          const result = await daemonAnnotations(options.wsUrl, sessionId);
          return result.note === NOTE ? result : null;
        },
        'the daemon to hold the note',
        10_000,
      );
      runner.assert(
        held.annotations.length === 1,
        `Writing the note disturbed the marks it belongs to: ${JSON.stringify(held.annotations)}`,
      );
    });

    await runner.step('send_shortcut_submits_it_and_tombstones_it', async () => {
      // A real ⌘Return, not the button: the button is a click handler any unit
      // test can drive, while the keystroke has to survive AppKit, reach the
      // page's capture-phase listener, and be claimed by this pane instead of
      // going to the PTY.
      await driver.pressKeyCode(36, { command: true });
      const sent = await pollFor(
        async () => {
          const state = await client.request('get_annotation_state', {});
          return state.annotations?.length === 0 ? state : null;
        },
        'the panel to clear after Send all',
        10_000,
      );
      runner.assert(
        /sent 1 to the session/.test(sent.footer || ''),
        `Panel footer after Send all = ${JSON.stringify(sent.footer)}, want the sent confirmation`,
      );

      const after = await pollFor(
        async () => {
          const result = await daemonAnnotations(options.wsUrl, sessionId);
          return result.annotations.length === 0 ? result : null;
        },
        'the daemon to clear the sent set',
        10_000,
      );
      // The tombstone, not a save of the empty list: a save already in flight
      // must not be able to put the sent marks back.
      runner.assert(after.generation >= 2, `Generation after send = ${after.generation}, want the raised tombstone`);
      // The note was delivered with them, so it is spent with them — one left
      // behind is an instruction that goes out a second time.
      runner.assert(
        !after.note,
        `The daemon still holds the sent note: ${JSON.stringify(after.note)}`,
      );
      runner.assert(
        !sent.note,
        `The note box still holds what was just sent: ${JSON.stringify(sent.note)}`,
      );

      // The claim this whole path exists for: Send all SUBMITS. Text sitting in
      // the composer would satisfy any assertion about the pane's contents, so
      // the only proof that separates a paste from a send is the session taking
      // a turn on it — which is also what proves the Enter landed as a keypress
      // rather than being folded into the pasted block.
      await waitForTurn(observer, sessionId, 'the turn the annotation feedback opened');

      const lines = await readLines(client, sessionId, paneId);
      // Submitted means the composer no longer holds the payload. Read the
      // composer itself — the rows between the last two rules the agent draws
      // around it — and look for the payload's own words. A "❯ " prefix is not
      // enough: the agent draws a suggested prompt in there when it is empty,
      // and by prefix alone that is indistinguishable from text left unsent.
      const rules = lines
        .map((line, index) => (/^─{10,}/.test(line.trim()) ? index : -1))
        .filter((index) => index >= 0);
      const composer = rules.length >= 2
        ? lines.slice(rules[rules.length - 2] + 1, rules[rules.length - 1])
        : [];
      runner.assert(
        rules.length >= 2,
        `Could not find the composer in the pane, so this cannot prove the payload left it. Pane text:\n${lines.join('\n')}`,
      );
      const held = composer.filter((line) => (
        line.includes('Feedback on your last message')
        || line.includes(NOTE.slice(0, 24))
        || quote.trim().split(/\s+/).some((word) => word.length > 4 && line.includes(word))
      ));
      runner.assert(
        held.length === 0,
        `The feedback is still sitting in the prompt, so it was typed but never submitted. `
        + `Composer holds ${JSON.stringify(held)}. Pane text:\n${lines.join('\n')}`,
      );
    });

    const result = runner.finishSuccess({ sessionId, paneId, quote, anchoredRowText });
    console.log('[verify] PASS — terminal annotations: a past turn stayed annotated across a new turn and an app relaunch.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'terminal-annotations-failure', sessionId).catch(() => {});
    }
    const result = runner.finishFailure(error, { sessionId, quote });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const pane of workspace?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

// Importing this file for its helpers must not launch an app.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exitCode = 1;
  });
}
