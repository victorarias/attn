import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import xtermPkg from '@xterm/xterm';
import { snapshotVisibleTerminalFrame } from './terminalScreenSnapshotHelper.mjs';

const { Terminal } = xtermPkg;

function loadFixture() {
  const path = resolve(process.cwd(), 'src/test/fixtures/codex-main-resize-capture.json');
  return JSON.parse(readFileSync(path, 'utf8'));
}

function loadSplitFixture() {
  const path = resolve(process.cwd(), 'src/test/fixtures/tr205-first-split-capture.json');
  return JSON.parse(readFileSync(path, 'utf8'));
}

function loadRelaunchReplayFixture() {
  const path = resolve(process.cwd(), 'src/test/fixtures/tr206-relaunch-replay-bad.json');
  return JSON.parse(readFileSync(path, 'utf8'));
}

function decodeBase64(base64) {
  return Uint8Array.from(Buffer.from(base64, 'base64'));
}

function writeAsync(term, data) {
  return new Promise((resolve) => {
    term.write(data, resolve);
  });
}

async function feedStage(term, chunks) {
  for (const chunk of chunks) {
    await writeAsync(term, decodeBase64(chunk));
  }
}

function topSnapshot(term, rows = 12) {
  const buffer = term.buffer.active;
  const start = buffer.viewportY || 0;
  const lines = [];
  const wrapped = [];
  for (let i = 0; i < rows; i += 1) {
    const line = buffer.getLine(start + i);
    lines.push(line ? line.translateToString(true) : '');
    wrapped.push(line ? Boolean(line.isWrapped) : false);
  }
  return {
    viewportY: buffer.viewportY,
    lines,
    wrapped,
  };
}

function bufferContains(term, token) {
  const buffer = term.buffer.active;
  for (let i = 0; i < buffer.length; i += 1) {
    const line = buffer.getLine(i);
    if (line?.translateToString(true).includes(token)) {
      return true;
    }
  }
  return false;
}

describe('snapshotVisibleTerminalFrame', () => {
  it('reproduces the TR-205 first-split header loss from the exact app PTY sequence', async () => {
    const fixture = loadSplitFixture();
    const term = new Terminal({
      cols: fixture.wideSize[0],
      rows: fixture.wideSize[1],
      allowProposedApi: true,
    });

    await feedStage(term, fixture.baselineChunks);
    const baselineWide = topSnapshot(term);

    term.resize(fixture.splitSize[0], fixture.splitSize[1]);
    await feedStage(term, fixture.splitChunks);
    const afterSplit = topSnapshot(term);

    expect(fixture.source).toContain('first split');
    expect(baselineWide.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
    expect(afterSplit.lines.some((line) => line.includes('OpenAI Codex'))).toBe(false);
    expect(afterSplit.lines.some((line) => line.includes('TR205ANCHOR'))).toBe(true);
  });

  it('isolates the TR-205 first-split failure to the final narrow redraw chunk', async () => {
    const fixture = loadSplitFixture();
    const term = new Terminal({
      cols: fixture.wideSize[0],
      rows: fixture.wideSize[1],
      allowProposedApi: true,
    });

    await feedStage(term, fixture.baselineChunks);
    const baselineWide = topSnapshot(term);

    await feedStage(term, [fixture.splitChunks.at(-1)]);
    const afterFinalChunk = topSnapshot(term);

    expect(baselineWide.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
    expect(afterFinalChunk.lines.some((line) => line.includes('OpenAI Codex'))).toBe(false);
    expect(afterFinalChunk.lines.some((line) => line.includes('TR205ANCHOR'))).toBe(true);
  });

  it('shows the TR-205 header is still present after resize and returns when the viewport is repinned', async () => {
    const fixture = loadSplitFixture();
    const term = new Terminal({
      cols: fixture.wideSize[0],
      rows: fixture.wideSize[1],
      allowProposedApi: true,
      scrollback: 1000,
    });

    await feedStage(term, fixture.baselineChunks);
    const baselineWide = topSnapshot(term);

    term.resize(fixture.splitSize[0], fixture.splitSize[1]);
    const afterResize = topSnapshot(term);

    term.scrollToTop();
    const afterRepin = topSnapshot(term);

    expect(baselineWide.viewportY).toBeLessThanOrEqual(1);
    expect(afterResize.viewportY).toBeGreaterThan(baselineWide.viewportY);
    expect(afterResize.lines.some((line) => line.includes('OpenAI Codex'))).toBe(false);
    expect(afterRepin.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
  });

  it('shows the TR-205 split bytes preserve the header in buffer only when the wide-to-narrow transition is replayed', async () => {
    const fixture = loadSplitFixture();
    const wideReplay = Buffer.concat(fixture.baselineChunks.map((chunk) => decodeBase64(chunk)));
    const narrowReplay = Buffer.concat(fixture.splitChunks.map((chunk) => decodeBase64(chunk)));

    const segmented = new Terminal({
      cols: fixture.wideSize[0],
      rows: fixture.wideSize[1],
      allowProposedApi: true,
      scrollback: 5000,
    });

    await writeAsync(segmented, wideReplay);
    segmented.resize(fixture.splitSize[0], fixture.splitSize[1]);
    await writeAsync(segmented, narrowReplay);

    const flattened = new Terminal({
      cols: fixture.splitSize[0],
      rows: fixture.splitSize[1],
      allowProposedApi: true,
      scrollback: 5000,
    });

    await writeAsync(flattened, Buffer.concat([wideReplay, narrowReplay]));

    expect(bufferContains(segmented, 'OpenAI Codex')).toBe(true);
    expect(bufferContains(segmented, 'TR205ANCHOR')).toBe(true);
    expect(bufferContains(flattened, 'OpenAI Codex')).toBe(false);
    expect(bufferContains(flattened, 'TR205ANCHOR')).toBe(true);

    segmented.scrollToTop();
    const segmentedTop = topSnapshot(segmented);
    expect(segmentedTop.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
  });

  it('does not recover the Codex header on a fresh xterm without replayed history', async () => {
    const fixture = loadFixture();
    const live = new Terminal({
      cols: fixture.initialSize[0],
      rows: fixture.initialSize[1],
      allowProposedApi: true,
    });

    await feedStage(live, fixture.launch);
    live.resize(fixture.resizeSmall[0], fixture.resizeSmall[1]);
    await feedStage(live, fixture.resizeSmallChunks);

    live.resize(fixture.resizeLarge[0], fixture.resizeLarge[1]);
    await feedStage(live, fixture.resizeLargeChunks);
    const liveLarge = topSnapshot(live);

    const noReplay = new Terminal({
      cols: fixture.resizeSmall[0],
      rows: fixture.resizeSmall[1],
      allowProposedApi: true,
    });

    noReplay.resize(fixture.resizeLarge[0], fixture.resizeLarge[1]);
    await feedStage(noReplay, fixture.resizeLargeChunks);
    const noReplayLarge = topSnapshot(noReplay);

    expect(fixture.source).toContain('standalone codex');
    expect(liveLarge.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
    expect(noReplayLarge.lines.some((line) => line.includes('OpenAI Codex'))).toBe(false);
  });

  it('keeps Codex resize recovery when raw history replays the current wide state', async () => {
    const fixture = loadFixture();
    const replayed = new Terminal({
      cols: fixture.resizeLarge[0],
      rows: fixture.resizeLarge[1],
      allowProposedApi: true,
    });

    await feedStage(replayed, fixture.launch);
    await feedStage(replayed, fixture.resizeSmallChunks);
    await feedStage(replayed, fixture.resizeLargeChunks);

    const restoredWide = topSnapshot(replayed);
    replayed.resize(fixture.resizeSmall[0], fixture.resizeSmall[1]);
    await feedStage(replayed, fixture.resizeSmallChunks);
    replayed.resize(fixture.resizeLarge[0], fixture.resizeLarge[1]);
    await feedStage(replayed, fixture.resizeLargeChunks);
    const recoveredWide = topSnapshot(replayed);

    expect(restoredWide.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
    expect(recoveredWide.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
  });

  it('does not treat visible-frame replay as xterm-state-equivalent for Codex resize recovery', async () => {
    const fixture = loadFixture();
    const live = new Terminal({
      cols: fixture.initialSize[0],
      rows: fixture.initialSize[1],
      allowProposedApi: true,
    });

    await feedStage(live, fixture.launch);
    live.resize(fixture.resizeSmall[0], fixture.resizeSmall[1]);
    await feedStage(live, fixture.resizeSmallChunks);
    const liveSmall = topSnapshot(live);

    const replayPayload = snapshotVisibleTerminalFrame(live);
    const replay = new Terminal({
      cols: fixture.resizeSmall[0],
      rows: fixture.resizeSmall[1],
      allowProposedApi: true,
    });
    await writeAsync(replay, replayPayload);
    const replaySmall = topSnapshot(replay);

    live.resize(fixture.resizeLarge[0], fixture.resizeLarge[1]);
    await feedStage(live, fixture.resizeLargeChunks);
    const liveLarge = topSnapshot(live);

    replay.resize(fixture.resizeLarge[0], fixture.resizeLarge[1]);
    await feedStage(replay, fixture.resizeLargeChunks);
    const replayLarge = topSnapshot(replay);

    expect(fixture.source).toContain('standalone codex');
    expect(liveSmall.wrapped.some(Boolean)).toBe(true);
    expect(replaySmall.wrapped.some(Boolean)).toBe(false);
    expect(liveLarge.lines.some((line) => line.includes('OpenAI Codex'))).toBe(true);
    expect(replayLarge.lines.some((line) => line.includes('OpenAI Codex'))).toBe(false);
    expect(replayLarge.lines).toEqual(replaySmall.lines);
    expect(liveLarge.viewportY).not.toBe(replayLarge.viewportY);
  });

  it('replays the TR-206 bad relaunch payload into a fresh xterm with the header body missing at the top', async () => {
    const fixture = loadRelaunchReplayFixture();
    const raw = Buffer.from(fixture.scrollbackBase64, 'base64').toString('utf8');
    const term = new Terminal({
      cols: fixture.cols,
      rows: fixture.rows,
      allowProposedApi: true,
      scrollback: 2000,
    });

    await feedStage(term, [fixture.scrollbackBase64]);
    term.scrollToTop();
    const replayedTop = topSnapshot(term, 8);
    const topText = replayedTop.lines.join('\n');

    expect(fixture.source).toContain('TR-206');
    expect(raw.includes('OpenAI Codex')).toBe(true);
    expect(topText.includes('OpenAI Codex')).toBe(false);
    expect(replayedTop.lines.some((line) => line.includes('╭'))).toBe(true);
    expect(replayedTop.lines.some((line) => line.includes(fixture.token))).toBe(true);
  });

  it('shows the TR-206 bad relaunch replay still emits terminal query responses while historical bytes are being restored', async () => {
    const fixture = loadRelaunchReplayFixture();
    const term = new Terminal({
      cols: fixture.cols,
      rows: fixture.rows,
      allowProposedApi: true,
      scrollback: 2000,
    });
    const replies = [];

    term.onData((data) => replies.push(data));
    await feedStage(term, [fixture.scrollbackBase64]);

    expect(replies.length).toBeGreaterThan(0);
    expect(replies).toContain('\u001b[?1;2c');
    expect(replies.some((data) => /^\u001b\[\d+;\d+R$/.test(data))).toBe(true);
  });
});
