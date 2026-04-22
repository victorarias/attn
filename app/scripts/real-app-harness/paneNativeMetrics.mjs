import fs from 'node:fs';
import path from 'node:path';
import { analyzePaneTextCoverage } from './paneNativeAnalysis.mjs';

function resolveTargetBounds(state, target) {
  const pane = state?.pane || null;
  const dom = pane?.dom || null;
  const selected = (
    target === 'paneBody' ? dom?.paneBody?.bounds
      : target === 'xtermScreen' ? dom?.xtermScreen?.bounds
      : target === 'terminalContainer' ? dom?.terminalContainer?.bounds
      : pane?.bounds
  ) || pane?.bounds || null;

  if (
    !selected ||
    !Number.isFinite(selected.x) ||
    !Number.isFinite(selected.y) ||
    !Number.isFinite(selected.width) ||
    !Number.isFinite(selected.height)
  ) {
    throw new Error(`Pane ${state?.paneId || 'unknown'} is missing ${target} bounds`);
  }

  return {
    x: selected.x,
    y: selected.y,
    width: selected.width,
    height: selected.height,
  };
}

// Pane coverage assertions used to sample WKWebView-composited pixels. That
// path required attn frontmost because occluded WebViews serve stale backing
// store, and the focus steal was visually disruptive. xterm's in-process
// buffer is the rendering ground truth for terminal content, so we analyse
// cell occupancy instead of pixel activity — no screencap, no focus steal.
export async function capturePaneNativeMetrics(
  client,
  runDir,
  prefix,
  sessionId,
  paneId,
  {
    target = 'paneBody',
  } = {},
) {
  const state = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
  const targetBounds = resolveTargetBounds(state, target);
  const summaryPath = path.join(runDir, `${prefix}-${paneId}-${target}-analysis.json`);

  const visibleContent = state?.pane?.visibleContent || null;
  if (!visibleContent || !Array.isArray(visibleContent.lines) || visibleContent.lines.length === 0) {
    throw new Error(
      `Pane ${paneId} has no visible content to analyse (cols=${visibleContent?.cols ?? 'n/a'}, lines=${visibleContent?.lines?.length ?? 'n/a'})`
    );
  }

  const analysis = analyzePaneTextCoverage({
    cols: visibleContent.cols,
    lines: visibleContent.lines,
  });

  const result = {
    sessionId,
    paneId,
    target,
    cssBounds: targetBounds,
    grid: {
      cols: visibleContent.cols,
      rows: visibleContent.lines.length,
    },
    analysis,
    paneState: {
      bounds: state?.pane?.bounds || null,
      paneBodyBounds: state?.pane?.dom?.paneBody?.bounds || null,
      xtermScreenBounds: state?.pane?.dom?.xtermScreen?.bounds || null,
      visibleContent,
      renderHealth: state?.renderHealth || null,
    },
  };

  fs.mkdirSync(runDir, { recursive: true });
  fs.writeFileSync(summaryPath, `${JSON.stringify(result, null, 2)}\n`, 'utf8');
  return result;
}
