import { test, expect } from './fixtures';

// Repro for: opening a shell split blanks the agent pane (canvas shows a live
// cursor over blank cells; model still holds the content). The render trace
// (added in GhosttyTerminal.renderSurface, gated on __ATTN_RENDER_TRACE_ON)
// records, per paint: the cells-source offset, the printable cell count the
// MODEL holds, and the `quads` actually submitted to the GPU. That lets us tell
// "drew nothing despite content" (cells-source/offset) from "drew glyphs but
// surface stayed blank" (GL/canvas).

const ESC = '';
const BSU = `${ESC}[?2026h`;
const ESU = `${ESC}[?2026l`;

// Mirrors the `paint` events pushed into the diagnostics ring
// (see app/src/utils/terminalDiagnosticsLog.ts `PaintSample`). The ring stores
// a pane key (paneId/sessionId/debugName) plus the session id, not a single
// `debug` string.
interface RenderTraceEntry {
  at: number;
  kind: string;
  pane?: string;
  session?: string;
  force: boolean;
  offset: number;
  modelPrintable: number;
  quads: number | null;
  cellsArrayLen?: number | null;
  skipNull?: number | null;
  skipZeroWidth?: number | null;
  cols: number;
  rows: number;
}

function fullFrame(tag: string, rows = 50, cols = 140): string {
  let out = `${BSU}${ESC}[?25l${ESC}[2J${ESC}[H`;
  for (let r = 0; r < rows; r += 1) {
    out += `${ESC}[${r + 1};1H` + `${tag} line ${r} `.padEnd(cols, '.').slice(0, cols);
  }
  out += `${ESC}[H${ESU}`;
  return out;
}

async function emit(
  page: import('@playwright/test').Page,
  id: string,
  data: string,
) {
  await page.evaluate(({ id, data }) => window.__TEST_EMIT_PTY_DATA?.(id, data), { id, data });
}

async function readTrace(
  page: import('@playwright/test').Page,
  sessionId: string,
): Promise<RenderTraceEntry[]> {
  return page.evaluate((sid) => {
    const all = (window as Window & { __ATTN_RENDER_TRACE?: RenderTraceEntry[] }).__ATTN_RENDER_TRACE ?? [];
    return all.filter((entry) =>
      entry.kind === 'paint'
      && (entry.session === sid || (typeof entry.pane === 'string' && entry.pane.includes(sid))));
  }, sessionId) as Promise<RenderTraceEntry[]>;
}

test('agent pane stays painted after opening a shell split', async ({ page, daemon }) => {
  await daemon.start();
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean; __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE_ON = true;
    (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = [];
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-split';
  const terminal = await setupAgent(page, daemon, agentId);

  // 1) Paint a full frame and confirm it actually rendered (high quad count).
  await emit(page, agentId, fullFrame('OLD'));
  await expect
    .poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId), { timeout: 5000 })
    .toContain('OLD line 0');
  await expect
    .poll(async () => {
      const trace = await readTrace(page, agentId);
      const last = trace[trace.length - 1];
      return last?.quads ?? 0;
    }, { timeout: 5000 })
    .toBeGreaterThan(50);

  const sizeBefore = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);

  // Reset the trace so we only inspect post-split paints.
  await page.evaluate(() => { (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = []; });

  // 2) Put the agent mid synchronized-frame (open BSU, no close), exactly like
  //    the live daemon log showed right before the split SIGWINCH.
  await emit(page, agentId, `${BSU}${ESC}[?25l${ESC}[H${ESC}[21C${ESC}[40B`);

  // 3) Open the shell split (Cmd+D). This resizes the agent pane.
  await terminal.click({ position: { x: 80, y: 8 } });
  await page.keyboard.press('Meta+d');

  // 4) Confirm the split actually resized the agent pane.
  await expect
    .poll(async () => {
      const size = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);
      if (!size || !sizeBefore) return 'no-size';
      return size.rows !== sizeBefore.rows || size.cols !== sizeBefore.cols ? 'resized' : 'same';
    }, { timeout: 8000 })
    .toBe('resized');

  const sizeAfter = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);

  // 5) Agent responds to SIGWINCH with a fresh full redraw (2026 + 2J + content).
  await emit(page, agentId, fullFrame('NEW', Math.max(10, (sizeAfter?.rows ?? 24) - 1), Math.max(20, (sizeAfter?.cols ?? 80))));

  // Let paints settle.
  await page.waitForTimeout(800);

  const trace = await readTrace(page, agentId);
  const tail = trace.slice(-12);
  const last = trace[trace.length - 1];

  console.log('=== SPLIT-BLANK REPRO DIAGNOSTICS ===');
  console.log('sizeBefore', JSON.stringify(sizeBefore), 'sizeAfter', JSON.stringify(sizeAfter));
  console.log('post-split render count:', trace.length);
  console.log('last render:', JSON.stringify(last));
  console.log('tail renders:');
  for (const entry of tail) {
    console.log(`  force=${entry.force} offset=${entry.offset} modelPrintable=${entry.modelPrintable} quads=${entry.quads} ${entry.cols}x${entry.rows}`);
  }
  const modelText = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId);
  console.log('model contains NEW line 0:', modelText.includes('NEW line 0'));

  await terminal.screenshot({ path: 'test-results/split-blank-agent.png' }).catch(() => {});

  // The model must hold the redraw (sanity: bytes were applied).
  expect(modelText).toContain('NEW line 0');
  // The bug: the agent paints blank despite the model holding content. If the
  // last paint drew (near) nothing, we've reproduced it.
  expect(last, 'expected at least one agent paint after the split redraw').toBeTruthy();
  expect(last!.quads ?? 0, `agent surface drew ${last?.quads} quads while model held ${last?.modelPrintable} printable cells`).toBeGreaterThan(50);
});

async function setupAgent(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string; workspace_id?: string }) => Promise<void> },
  agentId: string,
) {
  const workspaceId = `workspace-${agentId}`;
  await page.evaluate(({ sessionId, workspaceId }) => {
    window.__TEST_INJECT_SESSION?.({ id: sessionId, label: 'Agent Split', state: 'working', cwd: '/tmp/test/agent-split', workspaceId });
  }, { sessionId: agentId, workspaceId });
  await daemon.injectSession({ id: agentId, label: 'Agent Split', state: 'working', directory: '/tmp/test/agent-split', workspace_id: workspaceId });
  await page.locator(`[data-testid="session-${agentId}"]`).click();
  const terminal = page.locator(`[data-pane-session-id="${agentId}"][data-pane-kind="agent"] .terminal-container`);
  await expect(terminal).toBeVisible({ timeout: 5000 });
  await waitForPaneReady(page, agentId);
  return terminal;
}

// The terminal CONTAINER becoming visible is not the same as the pane being
// ready to receive PTY data. `__TEST_EMIT_PTY_DATA` delivers to the live
// terminal handle and silently DROPS the event when no handle is registered yet
// (useGhosttyPaneRuntime.deliverEvent: `if (!terminal) return`). The handle is
// only registered once the Ghostty WASM model has finished loading
// (handleTerminalReady), which lands strictly after the DOM container is
// visible. Emitting in that window loses the bytes and the model never updates,
// so the downstream `toContain('… line 0')` poll times out — the flake. In prod
// this race cannot happen: data only arrives in response to attach, which is
// itself fired from handleTerminalReady (after the handle exists). `getPaneSize`
// returns a size only once the handle is registered, so it is the precise
// "emit will be delivered" signal to gate on.
async function waitForPaneReady(
  page: import('@playwright/test').Page,
  sessionId: string,
) {
  await expect
    .poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, sessionId), {
      timeout: 10000,
    })
    .not.toBeNull();
}

function chunks(value: string, size: number): string[] {
  const out: string[] = [];
  for (let i = 0; i < value.length; i += size) out.push(value.slice(i, i + size));
  return out;
}

function dumpTail(label: string, trace: RenderTraceEntry[]) {
  console.log(`=== ${label} ===`);
  console.log('post-split render count:', trace.length);
  for (const entry of trace.slice(-12)) {
    console.log(`  force=${entry.force} offset=${entry.offset} modelPrintable=${entry.modelPrintable} quads=${entry.quads} ${entry.cols}x${entry.rows}`);
  }
}

// Variant: redraw delivered in chunks that race the split's fit()/resize.
test('agent stays painted when split races a chunked redraw', async ({ page, daemon }) => {
  await daemon.start();
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean; __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE_ON = true;
    (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = [];
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-race';
  const terminal = await setupAgent(page, daemon, agentId);

  await emit(page, agentId, fullFrame('OLD'));
  await expect.poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId), { timeout: 5000 }).toContain('OLD line 0');
  await page.evaluate(() => { (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = []; });

  // Open a synchronized frame (no close) — mid-frame, like the live log.
  await emit(page, agentId, `${BSU}${ESC}[?25l${ESC}[H${ESC}[21C${ESC}[40B`);

  // Fire the split, then immediately spam the redraw in small chunks WITHOUT
  // awaiting the split to settle, so fit()/resize lands mid-redraw.
  await terminal.click({ position: { x: 80, y: 8 } });
  await page.keyboard.press('Meta+d');
  const redraw = fullFrame('NEW', 44, 75);
  for (const chunk of chunks(redraw, 180)) {
    await emit(page, agentId, chunk);
  }

  await page.waitForTimeout(1200);
  const trace = await readTrace(page, agentId);
  dumpTail('CHUNKED RACE', trace);
  const last = trace[trace.length - 1];
  console.log('last:', JSON.stringify(last));
  await terminal.screenshot({ path: 'test-results/split-blank-race.png' }).catch(() => {});
  expect(last?.quads ?? 0, `agent drew ${last?.quads} quads, model had ${last?.modelPrintable}`).toBeGreaterThan(50);
});

// Variant: the user is scrolled up (viewportOffset != 0) when the split lands.
// This matches the symptom: a live cursor over frozen/blank cells.
test('agent stays painted when split lands while scrolled up', async ({ page, daemon }) => {
  await daemon.start();
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean; __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE_ON = true;
    (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = [];
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-scroll';
  const terminal = await setupAgent(page, daemon, agentId);

  // Build scrollback so wheel-up produces a non-zero viewport offset.
  const scrollback = Array.from({ length: 200 }, (_, i) => `HIST line ${String(i).padStart(3, '0')}`).join('\r\n');
  await emit(page, agentId, `${ESC}[2J${ESC}[H${scrollback}`);
  await emit(page, agentId, fullFrame('OLD'));
  await expect.poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId), { timeout: 5000 }).toContain('OLD line 0');

  // Scroll up.
  await terminal.hover({ position: { x: 80, y: 200 } });
  await page.mouse.wheel(0, -600);
  await page.evaluate(() => { (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = []; });
  await emit(page, agentId, `${BSU}${ESC}[?25l${ESC}[H${ESC}[21C${ESC}[40B`);

  await page.keyboard.press('Meta+d');
  await page.waitForTimeout(300);
  await emit(page, agentId, fullFrame('NEW', 44, 75));
  await page.waitForTimeout(1000);

  const trace = await readTrace(page, agentId);
  dumpTail('SCROLLED-UP SPLIT', trace);
  const last = trace[trace.length - 1];
  console.log('last:', JSON.stringify(last));
  await terminal.screenshot({ path: 'test-results/split-blank-scrolled.png' }).catch(() => {});
  // Diagnostic only: report whether a stale scrollback slice was painted.
  console.log('offset on last paint:', last?.offset, 'force:', last?.force, 'quads:', last?.quads);
});
