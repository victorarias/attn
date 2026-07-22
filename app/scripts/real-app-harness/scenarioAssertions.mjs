import {
  comparePaneNativePaintCoverage,
  comparePaneNativePaintRegression,
  evaluatePaneNativePaintCoverage,
} from './paneNativeAnalysis.mjs';
import { capturePaneNativeMetrics } from './paneNativeMetrics.mjs';

export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function compactTerminalText(text) {
  return String(text || '').replace(/\s+/g, '');
}

function terminalTextIncludes(text, needle, { allowWrapped = false } = {}) {
  if (!needle) {
    return true;
  }
  const source = String(text || '');
  if (source.includes(needle)) {
    return true;
  }
  if (!allowWrapped) {
    return false;
  }
  return compactTerminalText(source).includes(compactTerminalText(needle));
}

export function firstWorkspacePane(workspace) {
  return (workspace?.panes || [])[0] || null;
}

function isRetryableAutomationAbsence(error) {
  const message = error instanceof Error ? error.message : String(error || '');
  return message.includes('Session not found') || message.includes('Pane not found');
}

export async function waitForPaneState(client, sessionId, paneId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    try {
      lastState = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last request error: ${lastError instanceof Error ? lastError.message : 'none'}\nLast pane state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

export async function waitForPaneVisible(client, sessionId, paneId, timeoutMs = 20_000) {
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(
      state?.pane?.bounds &&
      state.pane.bounds.width >= 120 &&
      state.pane.bounds.height >= 80 &&
      state?.renderHealth?.flags?.terminalVisible !== false
    ),
    `pane ${paneId} visible`,
    timeoutMs,
  );
}

export async function scrollPaneToTop(client, sessionId, paneId, timeoutMs = 12_000) {
  await client.request('scroll_pane_to_top', { sessionId, paneId }, { timeoutMs: 20_000 });
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => {
      const visibleContent = state?.pane?.visibleContent || null;
      const viewportY = visibleContent?.viewportY ?? 0;
      const firstNonEmptyLine = visibleContent?.summary?.firstNonEmptyLine || '';
      return viewportY <= 1 || firstNonEmptyLine.includes('OpenAI Codex') || firstNonEmptyLine.startsWith('╭');
    },
    `pane ${paneId} viewport top`,
    timeoutMs,
  );
}

// A freshly-split pane's terminal only starts receiving pty_output after the
// frontend's `attach_session` RPC succeeds. `write_pane` bypasses the terminal and
// writes straight to the worker PTY, so input that arrives before attach
// runs in the shell but drops output into the worker's scrollback without
// reaching the terminal buffer (fresh_spawn attach omits replay). Gate writes
// on `runtimeAttached=true` so visible content reflects what the shell did.
// Returns { state, elapsedMs } — elapsedMs lets callers distinguish "gate
// was cosmetic" (trivially true) from "gate caught a real attach stall".
export async function waitForPaneAttached(
  client,
  sessionId,
  paneId,
  timeoutMs = 15_000,
) {
  const startedAt = Date.now();
  const state = await waitForPaneState(
    client,
    sessionId,
    paneId,
    (entry) => Boolean(entry?.pane?.runtimeAttached),
    `pane ${paneId} runtime attached`,
    timeoutMs,
  );
  return { state, elapsedMs: Date.now() - startedAt };
}

export async function waitForPaneInputFocus(
  client,
  sessionId,
  paneId,
  timeoutMs = 12_000,
  { stableMs = 0 } = {},
) {
  const focusedState = await waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(state?.inputFocused),
    `pane ${paneId} input focus`,
    timeoutMs,
  );

  if (stableMs <= 0) {
    return focusedState;
  }

  await sleep(stableMs);
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(state?.inputFocused),
    `pane ${paneId} stable input focus`,
    Math.max(1_000, timeoutMs),
  );
}

// Fresh shell panes emit their first prompt ~hundreds of ms after attach,
// then continue a startup handshake (CPR/DA1 queries, bracketed-paste toggle,
// keyboard-mode setup). Typing during the handshake makes the shell raw-echo
// the first keystroke at col 6 *before* zle takes over — the visible token
// ends up rotated (e.g. `3tr502p76785` instead of `tr502p767853`). A prompt
// on screen is necessary but not sufficient; the shell must also be idle.
//
// The gate: require the prompt glyph at the tail of the buffer *and* wait
// for the terminal write-parse count to sit still for `idleMs`. That marks the
// end of the startup burst, after which keystrokes go straight into zle.
export async function waitForPaneShellReady(
  client,
  sessionId,
  paneId,
  {
    timeoutMs = 15_000,
    idleMs = 400,
    promptRegex = /[\$#%❯>»⟫]\s*$/,
    description,
  } = {},
) {
  const label = description || `shell prompt ready in pane ${paneId}`;
  const startedAt = Date.now();
  const pollMs = 100;
  let lastChangeAt = Date.now();
  let lastWriteCount = null;
  let lastText = '';

  while (Date.now() - startedAt < timeoutMs) {
    let text = '';
    let writeCount = null;
    try {
      const [textPayload, statePayload] = await Promise.all([
        client.request('read_pane_text', { sessionId, paneId }, { timeoutMs: 20_000 }),
        client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 }),
      ]);
      text = typeof textPayload?.text === 'string' ? textPayload.text : '';
      writeCount = statePayload?.renderHealth?.terminal?.writeParsedCount ?? null;
      lastText = text;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      await sleep(pollMs);
      continue;
    }

    if (writeCount !== lastWriteCount) {
      lastChangeAt = Date.now();
      lastWriteCount = writeCount;
    }

    const lines = text.split(/\r?\n/);
    while (lines.length > 0 && lines[lines.length - 1].trim() === '') {
      lines.pop();
    }
    const promptOnScreen = lines.length > 0 && promptRegex.test(lines[lines.length - 1]);
    const idleFor = Date.now() - lastChangeAt;
    if (promptOnScreen && (writeCount ?? 0) > 0 && idleFor >= idleMs) {
      return { text, writeCount };
    }
    await sleep(pollMs);
  }

  throw new Error(
    `Timed out waiting for ${label} (lastWriteCount=${lastWriteCount}). Tail:\n${lastText.slice(-400)}`
  );
}

export async function waitForPaneText(client, sessionId, paneId, predicate, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastPayload = null;
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    try {
      lastPayload = await client.request('read_pane_text', { sessionId, paneId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    if (predicate(lastPayload?.text || '')) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last request error: ${lastError instanceof Error ? lastError.message : 'none'}\nLast pane text tail:\n${(lastPayload?.text || '').slice(-800)}`
  );
}

export async function waitForPaneStyle(client, sessionId, paneId, predicate, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastPayload = null;
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    try {
      lastPayload = await client.request('read_pane_style', { sessionId, paneId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    if (predicate(lastPayload?.style || null, lastPayload)) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last request error: ${lastError instanceof Error ? lastError.message : 'none'}\nLast pane style:\n${JSON.stringify(lastPayload, null, 2)}`
  );
}

export async function waitForPaneTextChange(
  client,
  sessionId,
  paneId,
  previousText,
  description = `pane ${paneId} text change`,
  timeoutMs = 15_000,
) {
  const startedAt = Date.now();
  let lastPayload = null;
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    try {
      lastPayload = await client.request('read_pane_text', { sessionId, paneId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    const nextText = typeof lastPayload?.text === 'string' ? lastPayload.text : '';
    if (nextText !== previousText) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last request error: ${lastError instanceof Error ? lastError.message : 'none'}\nLast pane text tail:\n${(lastPayload?.text || '').slice(-800)}`
  );
}

export async function waitForSessionWorkspace(client, sessionId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastWorkspace = null;
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    try {
      lastWorkspace = await client.request('get_workspace', { sessionId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    if (predicate(lastWorkspace)) {
      return lastWorkspace;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last request error: ${lastError instanceof Error ? lastError.message : 'none'}\nLast workspace:\n${JSON.stringify(lastWorkspace, null, 2)}`
  );
}

export async function waitForFirstWorkspacePane(client, sessionId, description, timeoutMs = 20_000) {
  const workspace = await waitForSessionWorkspace(
    client,
    sessionId,
    (entry) => Boolean(firstWorkspacePane(entry)?.paneId),
    description || `first pane for session ${sessionId}`,
    timeoutMs,
  );
  return firstWorkspacePane(workspace);
}

function newRuntimePanes(workspace, existingPaneIds) {
  return (workspace?.panes || []).filter(
    (pane) => !existingPaneIds.has(pane.paneId) && Boolean(pane.runtimeId),
  );
}

export async function waitForNewShellPane(client, sessionId, existingPaneIds, description, timeoutMs = 20_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => newRuntimePanes(workspace, existingPaneIds).length > 0,
    description,
    timeoutMs,
  ).then((workspace) => {
    const newPanes = newRuntimePanes(workspace, existingPaneIds);
    const activePane = newPanes.find((pane) => pane.paneId === workspace.activePaneId);
    return activePane || newPanes[0];
  });
}

async function waitForPaneVisibleContent(
  client,
  sessionId,
  paneId,
  predicate,
  description,
  timeoutMs = 20_000,
) {
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => predicate(state?.pane?.visibleContent || null, state),
    description,
    timeoutMs,
  );
}

// Evaluates the same gates assertPaneVisibleContent waits on, as a standalone
// array so callers can both drive the pass/fail predicate and render a compact
// per-gate report on timeout (see formatGateReport). The `contains` entry is
// omitted entirely when no needle was requested, since "OK"/"FAIL" for a gate
// that was never checked would be misleading in the report.
export function evaluateVisibleContentGates(
  visibleContent,
  {
    contains = null,
    allowWrappedContains = false,
    minNonEmptyLines = 2,
    minDenseLines = 1,
    minCharCount = 20,
    minMaxLineLength = 20,
  } = {},
) {
  const summary = visibleContent?.summary || {};
  const joined = (visibleContent?.lines || []).join('\n');
  const gates = [];

  if (contains) {
    const actual = terminalTextIncludes(joined, contains, { allowWrapped: allowWrappedContains });
    gates.push({
      gate: 'contains',
      actual,
      required: String(contains).slice(0, 40),
      ok: actual,
    });
  }

  const nonEmptyLines = summary.nonEmptyLineCount || 0;
  gates.push({
    gate: 'nonEmptyLines',
    actual: nonEmptyLines,
    required: minNonEmptyLines,
    ok: nonEmptyLines >= minNonEmptyLines,
  });

  const denseLines = summary.denseLineCount || 0;
  gates.push({
    gate: 'denseLines',
    actual: denseLines,
    required: minDenseLines,
    ok: denseLines >= minDenseLines,
  });

  const charCount = summary.charCount || 0;
  gates.push({
    gate: 'charCount',
    actual: charCount,
    required: minCharCount,
    ok: charCount >= minCharCount,
  });

  const maxLineLength = summary.maxLineLength || 0;
  gates.push({
    gate: 'maxLineLength',
    actual: maxLineLength,
    required: minMaxLineLength,
    ok: maxLineLength >= minMaxLineLength,
  });

  return gates;
}

export function formatGateReport(gates) {
  return gates
    .map((gate) => {
      if (gate.gate === 'contains') {
        return `contains ${gate.ok ? 'OK' : 'FAIL'}`;
      }
      return `${gate.gate} ${gate.actual}/${gate.required} ${gate.ok ? 'OK' : 'FAIL'}`;
    })
    .join(' | ');
}

function trimmedVisibleLines(visibleContent, maxLines = 30) {
  return (visibleContent?.lines || [])
    .map((line) => String(line || '').trimEnd())
    .slice(0, maxLines)
    .map((line) => `| ${line}`)
    .join('\n');
}

export async function assertPaneVisibleContent(
  client,
  sessionId,
  paneId,
  {
    contains = null,
    allowWrappedContains = false,
    minNonEmptyLines = 2,
    minDenseLines = 1,
    minCharCount = 20,
    minMaxLineLength = 20,
    timeoutMs = 20_000,
    description = `pane ${paneId} visible content`,
  } = {},
) {
  const gateOptions = {
    contains,
    allowWrappedContains,
    minNonEmptyLines,
    minDenseLines,
    minCharCount,
    minMaxLineLength,
  };
  const startedAt = Date.now();
  let lastVisibleContent = null;
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    let state = null;
    try {
      state = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    const visibleContent = state?.pane?.visibleContent || null;
    if (visibleContent) {
      lastVisibleContent = visibleContent;
      if (evaluateVisibleContentGates(visibleContent, gateOptions).every((gate) => gate.ok)) {
        return state;
      }
    }
    await sleep(200);
  }

  const gates = lastVisibleContent ? evaluateVisibleContentGates(lastVisibleContent, gateOptions) : [];
  const lines = trimmedVisibleLines(lastVisibleContent);
  throw new Error(
    [
      `Timed out waiting for ${description}.`,
      gates.length > 0 ? formatGateReport(gates) : `No visible content observed. Last request error: ${lastError instanceof Error ? lastError.message : 'none'}`,
      lines ? `Last visible lines:\n${lines}` : '',
    ].filter(Boolean).join('\n')
  );
}

function visibleContentAnchorLines(
  visibleContent,
  {
    maxAnchors = 6,
    minLineLength = 6,
    ignoreAnchorPatterns = [],
  } = {},
) {
  return (visibleContent?.lines || [])
    .map((line) => String(line || '').trim())
    .filter((line) => line.length >= minLineLength)
    .filter((line) => ignoreAnchorPatterns.every((pattern) => !pattern.test(line)))
    .slice(0, maxAnchors);
}

// After a resize (e.g. split_pane), the terminal keeps the pre-resize buffer around
// until the agent responds to SIGWINCH with a fresh redraw. Sampling the pane
// during that window captures stale wide content and misrepresents the post-
// resize baseline. Wait until no visible line exceeds the pane's current
// column count — that's the observable signal that reflow has landed.
export async function waitForPaneReflowed(client, sessionId, paneId, timeoutMs = 20_000, description) {
  const label = description || `pane ${paneId} reflowed to current geometry`;
  return waitForPaneVisibleContent(
    client,
    sessionId,
    paneId,
    (visibleContent) => {
      if (!visibleContent) {
        return false;
      }
      const cols = visibleContent.cols;
      const maxLineLength = visibleContent.summary?.maxLineLength;
      if (typeof cols !== 'number' || typeof maxLineLength !== 'number') {
        return false;
      }
      return maxLineLength <= cols;
    },
    label,
    timeoutMs,
  );
}

// Anchors restricted to lines containing the token — agent TUIs (claude
// especially) collapse or reflow echoed prompt instructions on re-render, so
// non-token lines are not stable anchors.
export function tokenAnchorIgnorePatterns(token) {
  return [/^\s*$/u, new RegExp(`^(?!.*${token})`)];
}

export async function assertPaneVisibleContentPreserved(
  client,
  sessionId,
  paneId,
  baselineVisibleContent,
  {
    minNonEmptyLineRatio = 0.5,
    minCharCountRatio = 0.4,
    maxAnchors = 6,
    minAnchorMatches = 3,
    minLineLength = 6,
    ignoreAnchorPatterns = [],
    timeoutMs = 20_000,
    description = `pane ${paneId} visible content preserved`,
  } = {},
) {
  if (!baselineVisibleContent) {
    throw new Error(`${description} requires baseline visible content`);
  }

  const baselineSummary = baselineVisibleContent.summary || {};
  const anchors = visibleContentAnchorLines(baselineVisibleContent, {
    maxAnchors,
    minLineLength,
    ignoreAnchorPatterns,
  });
  // An empty anchor list (e.g. ignoreAnchorPatterns filtered every line) must not become
  // an unpassable assert — fall back to the ratio gates only.
  const requiredAnchorMatches = anchors.length === 0
    ? 0
    : Math.min(Math.max(1, minAnchorMatches), anchors.length);
  const effectiveDescription = anchors.length === 0
    ? `${description} (no stable anchors after filtering — ratios only)`
    : description;
  const requiredNonEmptyLines = Math.max(
    2,
    Math.floor((baselineSummary.nonEmptyLineCount || anchors.length || 2) * minNonEmptyLineRatio),
  );
  const requiredCharCount = Math.max(
    20,
    Math.floor((baselineSummary.charCount || 20) * minCharCountRatio),
  );

  const startedAt = Date.now();
  let lastState = null;
  let lastMatches = [];
  let lastError = null;

  while (Date.now() - startedAt < timeoutMs) {
    try {
      lastState = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
      lastError = null;
    } catch (error) {
      if (!isRetryableAutomationAbsence(error)) {
        throw error;
      }
      lastError = error;
      await sleep(200);
      continue;
    }
    const visibleContent = lastState?.pane?.visibleContent || null;
    if (!visibleContent) {
      await sleep(200);
      continue;
    }
    const summary = visibleContent.summary || {};
    const joined = (visibleContent.lines || []).join('\n');
    lastMatches = anchors.filter((anchor) => terminalTextIncludes(joined, anchor, { allowWrapped: true }));
    const anchorsFullyRecovered = anchors.length > 0 && lastMatches.length === anchors.length;
    const charCountRecovered = (summary.charCount || 0) >= requiredCharCount;
    if (
      (
        (
          (summary.nonEmptyLineCount || 0) >= requiredNonEmptyLines &&
          charCountRecovered &&
          lastMatches.length >= requiredAnchorMatches
        ) ||
        (
          anchorsFullyRecovered &&
          charCountRecovered
        )
      )
    ) {
      return {
        state: lastState,
        anchors,
        matches: lastMatches,
      };
    }
    await sleep(200);
  }

  const lastSummary = lastState?.pane?.visibleContent?.summary || {};
  const lastNonEmptyLines = lastSummary.nonEmptyLineCount || 0;
  const lastCharCount = lastSummary.charCount || 0;
  const lastLines = trimmedVisibleLines(lastState?.pane?.visibleContent || null);

  throw new Error(
    [
      `Timed out waiting for ${effectiveDescription}.`,
      `Required anchors (${requiredAnchorMatches}/${anchors.length}): ${JSON.stringify(anchors)}`,
      `Matched anchors (${lastMatches.length}): ${JSON.stringify(lastMatches)}`,
      `nonEmptyLines ${lastNonEmptyLines}/${requiredNonEmptyLines} ${lastNonEmptyLines >= requiredNonEmptyLines ? 'OK' : 'FAIL'}`,
      `charCount ${lastCharCount}/${requiredCharCount} ${lastCharCount >= requiredCharCount ? 'OK' : 'FAIL'}`,
      `Last request error: ${lastError instanceof Error ? lastError.message : 'none'}`,
      lastLines ? `Last visible lines:\n${lastLines}` : '',
    ].filter(Boolean).join('\n')
  );
}

export async function assertPaneStyleSummaryPreserved(
  client,
  sessionId,
  paneId,
  baselineStyle,
  {
    minStyledCellRatio = 0.6,
    minStyledLineRatio = 0.6,
    minBoldCellRatio = 0.5,
    minUnderlineCellRatio = 0.5,
    minInverseCellRatio = 0.5,
    minFgPaletteCellRatio = 0.5,
    minFgRgbCellRatio = 0.5,
    minBgPaletteCellRatio = 0.5,
    minBgRgbCellRatio = 0.5,
    minUniqueStyleRatio = 0.5,
    timeoutMs = 20_000,
    description = `pane ${paneId} style preservation`,
  } = {},
) {
  const baselineSummary = baselineStyle?.summary || {};
  const minimumCount = (value, ratio) => {
    if (!Number.isFinite(value) || value <= 0) {
      return 0;
    }
    return Math.max(1, Math.floor(value * ratio));
  };

  return waitForPaneStyle(
    client,
    sessionId,
    paneId,
    (style) => {
      const summary = style?.summary || {};
      return (
        (summary.styledCellCount || 0) >= minimumCount(baselineSummary.styledCellCount || 0, minStyledCellRatio) &&
        (summary.styledLineCount || 0) >= minimumCount(baselineSummary.styledLineCount || 0, minStyledLineRatio) &&
        (summary.boldCellCount || 0) >= minimumCount(baselineSummary.boldCellCount || 0, minBoldCellRatio) &&
        (summary.underlineCellCount || 0) >= minimumCount(baselineSummary.underlineCellCount || 0, minUnderlineCellRatio) &&
        (summary.inverseCellCount || 0) >= minimumCount(baselineSummary.inverseCellCount || 0, minInverseCellRatio) &&
        (summary.fgPaletteCellCount || 0) >= minimumCount(baselineSummary.fgPaletteCellCount || 0, minFgPaletteCellRatio) &&
        (summary.fgRgbCellCount || 0) >= minimumCount(baselineSummary.fgRgbCellCount || 0, minFgRgbCellRatio) &&
        (summary.bgPaletteCellCount || 0) >= minimumCount(baselineSummary.bgPaletteCellCount || 0, minBgPaletteCellRatio) &&
        (summary.bgRgbCellCount || 0) >= minimumCount(baselineSummary.bgRgbCellCount || 0, minBgRgbCellRatio) &&
        (summary.uniqueStyleCount || 0) >= minimumCount(baselineSummary.uniqueStyleCount || 0, minUniqueStyleRatio)
      );
    },
    description,
    timeoutMs,
  );
}

export async function assertPaneUsesVisibleWidth(
  client,
  sessionId,
  paneId,
  {
    minMaxOccupiedWidthRatio = 0.65,
    minWideLineCount = 2,
    minMedianOccupiedWidthRatio = 0.45,
    timeoutMs = 20_000,
    description = `pane ${paneId} uses visible width`,
  } = {},
) {
  return waitForPaneVisibleContent(
    client,
    sessionId,
    paneId,
    (visibleContent) => {
      if (!visibleContent) {
        return false;
      }
      const summary = visibleContent.summary || {};
      return (
        (summary.maxOccupiedWidthRatio || 0) >= minMaxOccupiedWidthRatio &&
        (summary.wideLineCount || 0) >= minWideLineCount &&
        (summary.medianOccupiedWidthRatio || 0) >= minMedianOccupiedWidthRatio
      );
    },
    description,
    timeoutMs,
  );
}

export async function assertPaneCoverage(
  client,
  sessionId,
  paneId,
  {
    minWidthRatio = 0.85,
    minHeightRatio = 0.85,
    timeoutMs = 20_000,
    description = `pane ${paneId} coverage`,
  } = {},
) {
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => {
      const widthRatio = state?.renderHealth?.fill?.terminalSurfaceVsPaneBody?.width ?? 0;
      const heightRatio = state?.renderHealth?.fill?.terminalSurfaceVsPaneBody?.height ?? 0;
      return widthRatio >= minWidthRatio && heightRatio >= minHeightRatio;
    },
    description,
    timeoutMs,
  );
}

export async function assertPaneNativePaintCoverage(
  client,
  runDir,
  prefix,
  sessionId,
  paneId,
  {
    target = 'paneBody',
    minBusyColumnRatio = 0.45,
    minBusyRowRatio = 0.18,
    minBBoxWidthRatio = 0.45,
    minBBoxHeightRatio = 0.18,
    timeoutMs = 4_000,
    retryIntervalMs = 250,
    description = `pane ${paneId} native paint coverage`,
  } = {},
) {
  const startedAt = Date.now();
  let lastMetrics = null;
  let lastEvaluation = null;

  while (Date.now() - startedAt < timeoutMs) {
    const metrics = await capturePaneNativeMetrics(
      client,
      runDir,
      prefix,
      sessionId,
      paneId,
      { target },
    );

    const analysis = metrics.analysis || {};
    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio,
      minBusyRowRatio,
      minBBoxWidthRatio,
      minBBoxHeightRatio,
    });

    if (evaluation.ok) {
      return metrics;
    }

    lastMetrics = metrics;
    lastEvaluation = evaluation;
    await sleep(retryIntervalMs);
  }

  throw new Error(
    `${description} failed: ${(lastEvaluation?.failures || []).join(', ')}.\n${JSON.stringify(lastMetrics, null, 2)}`
  );
}

function assertPaneNativePaintDelta(
  baselineMetrics,
  candidateMetrics,
  {
    maxBusyColumnRatioDelta = 0.08,
    maxBusyRowRatioDelta = 0.08,
    maxBBoxWidthRatioDelta = 0.08,
    maxBBoxHeightRatioDelta = 0.08,
    maxActivePixelRatioDelta = 0.03,
    description = 'pane native paint delta',
  } = {},
) {
  const evaluation = comparePaneNativePaintCoverage(
    baselineMetrics?.analysis || null,
    candidateMetrics?.analysis || null,
    {
      maxBusyColumnRatioDelta,
      maxBusyRowRatioDelta,
      maxBBoxWidthRatioDelta,
      maxBBoxHeightRatioDelta,
      maxActivePixelRatioDelta,
    },
  );

  if (!evaluation.ok) {
    throw new Error(
      `${description} failed: ${evaluation.failures.join(', ')}.\n${JSON.stringify({
        baseline: baselineMetrics,
        candidate: candidateMetrics,
        comparison: evaluation,
      }, null, 2)}`
    );
  }

  return evaluation;
}

async function assertPaneNativePaintStable(
  client,
  runDir,
  prefix,
  sessionId,
  paneId,
  baselineMetrics,
  options = {},
) {
  const candidateMetrics = await capturePaneNativeMetrics(
    client,
    runDir,
    prefix,
    sessionId,
    paneId,
    { target: options.target || 'paneBody' },
  );

  const comparison = assertPaneNativePaintDelta(baselineMetrics, candidateMetrics, options);
  return {
    baselineMetrics,
    candidateMetrics,
    comparison,
  };
}

function assertPaneNativePaintNotWorse(
  baselineMetrics,
  candidateMetrics,
  {
    maxBusyColumnRatioRegression = 0.08,
    maxBusyRowRatioRegression = 0.08,
    maxBBoxWidthRatioRegression = 0.08,
    maxBBoxHeightRatioRegression = 0.08,
    maxActivePixelRatioRegression = 0.03,
    description = 'pane native paint regression',
  } = {},
) {
  const evaluation = comparePaneNativePaintRegression(
    baselineMetrics?.analysis || null,
    candidateMetrics?.analysis || null,
    {
      maxBusyColumnRatioRegression,
      maxBusyRowRatioRegression,
      maxBBoxWidthRatioRegression,
      maxBBoxHeightRatioRegression,
      maxActivePixelRatioRegression,
    },
  );

  if (!evaluation.ok) {
    throw new Error(
      `${description} failed: ${evaluation.failures.join(', ')}.\n${JSON.stringify({
        baseline: baselineMetrics,
        candidate: candidateMetrics,
        comparison: evaluation,
      }, null, 2)}`
    );
  }

  return evaluation;
}

export async function assertPaneNativePaintRecovered(
  client,
  runDir,
  prefix,
  sessionId,
  paneId,
  baselineMetrics,
  options = {},
) {
  const candidateMetrics = await capturePaneNativeMetrics(
    client,
    runDir,
    prefix,
    sessionId,
    paneId,
    { target: options.target || 'paneBody' },
  );

  const comparison = assertPaneNativePaintNotWorse(baselineMetrics, candidateMetrics, options);
  return {
    baselineMetrics,
    candidateMetrics,
    comparison,
  };
}

export async function captureSessionArtifacts(client, runDir, prefix, sessionId) {
  const writeJson = async (name, action, payload) => {
    try {
      const result = await client.request(action, payload);
      const fs = await import('node:fs');
      fs.writeFileSync(`${runDir}/${name}`, `${JSON.stringify(result, null, 2)}\n`, 'utf8');
    } catch (error) {
      const fs = await import('node:fs');
      fs.writeFileSync(
        `${runDir}/${name.replace(/\.json$/, '.txt')}`,
        error instanceof Error ? error.stack || error.message : String(error),
        'utf8',
      );
    }
  };

  await writeJson(`${prefix}-native-window.json`, 'capture_native_window_screenshot', {
    path: `${runDir}/${prefix}-native-window.png`,
  });
  await writeJson(`${prefix}-workspace.json`, 'get_workspace', { sessionId });
  await writeJson(`${prefix}-session-ui-state.json`, 'get_session_ui_state', { sessionId });
  await writeJson(`${prefix}-structured-snapshot.json`, 'capture_structured_snapshot', {
    sessionIds: [sessionId],
    includePaneText: false,
  });
  await writeJson(`${prefix}-render-health.json`, 'capture_render_health', {
    sessionIds: [sessionId],
  });
  await writeJson(`${prefix}-perf-snapshot.json`, 'capture_perf_snapshot', {
    sessionIds: [sessionId],
    settleFrames: 2,
    includeMemory: false,
  });
}
