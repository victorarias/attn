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

describe('snapshotVisibleTerminalFrame', () => {
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
});
