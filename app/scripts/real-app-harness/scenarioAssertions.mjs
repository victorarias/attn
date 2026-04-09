import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
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

export function terminalTextIncludes(text, needle, { allowWrapped = false } = {}) {
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

export function shellPanes(workspace) {
  return (workspace?.panes || []).filter((pane) => pane.kind === 'shell');
}

export async function waitForPaneState(client, sessionId, paneId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last pane state:\n${JSON.stringify(lastState, null, 2)}`
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

export async function waitForPaneInputFocus(client, sessionId, paneId, timeoutMs = 12_000) {
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(state?.inputFocused),
    `pane ${paneId} input focus`,
    timeoutMs,
  );
}

export async function waitForPaneText(client, sessionId, paneId, predicate, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastPayload = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastPayload = await client.request('read_pane_text', { sessionId, paneId }, { timeoutMs: 20_000 });
    if (predicate(lastPayload?.text || '')) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last pane text tail:\n${(lastPayload?.text || '').slice(-800)}`
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

  while (Date.now() - startedAt < timeoutMs) {
    lastPayload = await client.request('read_pane_text', { sessionId, paneId }, { timeoutMs: 20_000 });
    const nextText = typeof lastPayload?.text === 'string' ? lastPayload.text : '';
    if (nextText !== previousText) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last pane text tail:\n${(lastPayload?.text || '').slice(-800)}`
  );
}

export async function waitForSessionWorkspace(client, sessionId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastWorkspace = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastWorkspace = await client.request('get_workspace', { sessionId }, { timeoutMs: 20_000 });
    if (predicate(lastWorkspace)) {
      return lastWorkspace;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last workspace:\n${JSON.stringify(lastWorkspace, null, 2)}`
  );
}

export async function waitForNewShellPane(client, sessionId, existingPaneIds, description, timeoutMs = 20_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => shellPanes(workspace).some((pane) => !existingPaneIds.has(pane.paneId)),
    description,
    timeoutMs,
  ).then((workspace) => {
    const newShells = shellPanes(workspace).filter((pane) => !existingPaneIds.has(pane.paneId));
    const activeShell = newShells.find((pane) => pane.paneId === workspace.activePaneId);
    return activeShell || newShells[0];
  });
}

export async function waitForPaneVisibleContent(
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
  return waitForPaneVisibleContent(
    client,
    sessionId,
    paneId,
    (visibleContent) => {
      if (!visibleContent) {
        return false;
      }
      const summary = visibleContent.summary || {};
      const joined = (visibleContent.lines || []).join('\n');
      if (contains && !terminalTextIncludes(joined, contains, { allowWrapped: allowWrappedContains })) {
        return false;
      }
      return (
        (summary.nonEmptyLineCount || 0) >= minNonEmptyLines &&
        (summary.denseLineCount || 0) >= minDenseLines &&
        (summary.charCount || 0) >= minCharCount &&
        (summary.maxLineLength || 0) >= minMaxLineLength
      );
    },
    description,
    timeoutMs,
  );
}

function visibleContentAnchorLines(
  visibleContent,
  {
    maxAnchors = 6,
    minLineLength = 6,
  } = {},
) {
  return (visibleContent?.lines || [])
    .map((line) => String(line || '').trim())
    .filter((line) => line.length >= minLineLength)
    .slice(0, maxAnchors);
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
    timeoutMs = 20_000,
    description = `pane ${paneId} visible content preserved`,
  } = {},
) {
  if (!baselineVisibleContent) {
    throw new Error(`${description} requires baseline visible content`);
  }

  const baselineSummary = baselineVisibleContent.summary || {};
  const anchors = visibleContentAnchorLines(baselineVisibleContent, { maxAnchors, minLineLength });
  const requiredAnchorMatches = Math.min(Math.max(1, minAnchorMatches), Math.max(1, anchors.length));
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

  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
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

  throw new Error(
    [
      `Timed out waiting for ${description}.`,
      `Required anchors (${requiredAnchorMatches}/${anchors.length}): ${JSON.stringify(anchors)}`,
      `Matched anchors (${lastMatches.length}): ${JSON.stringify(lastMatches)}`,
      `Baseline summary: ${JSON.stringify(baselineSummary)}`,
      `Last summary: ${JSON.stringify(lastState?.pane?.visibleContent?.summary || null)}`,
      `Last visible lines: ${JSON.stringify((lastState?.pane?.visibleContent?.lines || []).slice(0, 18), null, 2)}`,
    ].join('\n')
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
      const widthRatio = state?.renderHealth?.fill?.xtermScreenVsPaneBody?.width ?? 0;
      const heightRatio = state?.renderHealth?.fill?.xtermScreenVsPaneBody?.height ?? 0;
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
    activityThreshold = 18,
    insetPx = 2,
    description = `pane ${paneId} native paint coverage`,
  } = {},
) {
  const metrics = await capturePaneNativeMetrics(
    client,
    runDir,
    prefix,
    sessionId,
    paneId,
    {
      target,
      activityThreshold,
      insetPx,
    },
  );

  const analysis = metrics.analysis || {};
  const evaluation = evaluatePaneNativePaintCoverage(analysis, {
    minBusyColumnRatio,
    minBusyRowRatio,
    minBBoxWidthRatio,
    minBBoxHeightRatio,
  });

  if (!evaluation.ok) {
    throw new Error(
      `${description} failed: ${evaluation.failures.join(', ')}.\n${JSON.stringify(metrics, null, 2)}`
    );
  }

  return metrics;
}

export function assertPaneNativePaintDelta(
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

export async function assertPaneNativePaintStable(
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
    {
      target: options.target || 'paneBody',
      activityThreshold: options.activityThreshold ?? 18,
      insetPx: options.insetPx ?? 2,
      bundleId: options.bundleId || 'com.attn.manager',
    },
  );

  const comparison = assertPaneNativePaintDelta(baselineMetrics, candidateMetrics, options);
  return {
    baselineMetrics,
    candidateMetrics,
    comparison,
  };
}

export function assertPaneNativePaintNotWorse(
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
    {
      target: options.target || 'paneBody',
      activityThreshold: options.activityThreshold ?? 18,
      insetPx: options.insetPx ?? 2,
      bundleId: options.bundleId || 'com.attn.manager',
    },
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
  await writeJson(`${prefix}-pane-debug.json`, 'dump_pane_debug', {});
  await writeJson(`${prefix}-terminal-runtime-trace.json`, 'dump_terminal_runtime_trace', {});

  try {
    await client.request('capture_window_screenshot', {
      path: `${runDir}/${prefix}-native-window.png`,
      bundleId: 'com.attn.manager',
    }, { timeoutMs: 20_000 });
  } catch (error) {
    try {
      await captureFrontWindowScreenshot(`${runDir}/${prefix}-native-window.png`);
      return;
    } catch (nativeError) {
      const fs = await import('node:fs');
      fs.writeFileSync(
        `${runDir}/${prefix}-native-window.txt`,
        nativeError instanceof Error ? nativeError.stack || nativeError.message : String(nativeError),
        'utf8',
      );
      return;
    }
  }
}
