#!/usr/bin/env node

import { performance } from 'node:perf_hooks';

const cols = 151;
const rows = 46;
const frames = 500;
const cells = Array.from({ length: cols * rows }, (_, index) => ({
  codepoint: 32 + index % 95,
  flags: index % 19 === 0 ? 1 : index % 31 === 0 ? 2 : 0,
  fg_r: 224,
  fg_g: 224,
  fg_b: 224,
  bg_r: index % 17 === 0 ? 32 : 0,
  bg_g: 0,
  bg_b: 0,
}));

const stringGlyphs = new Map();
const numericGlyphs = new Map();
for (let codepoint = 32; codepoint < 127; codepoint += 1) {
  for (let style = 0; style < 4; style += 1) {
    const styleText = `${style & 1 ? 'italic ' : ''}${style & 2 ? 'bold ' : ''}`;
    stringGlyphs.set(`${styleText}${String.fromCodePoint(codepoint)}`, codepoint);
    numericGlyphs.set(codepoint * 4 + style, codepoint);
  }
}

function oldCellLoop() {
  let checksum = 0;
  for (let frame = 0; frame < frames; frame += 1) {
    for (const cell of cells) {
      const fg = { r: cell.fg_r, g: cell.fg_g, b: cell.fg_b };
      const bg = { r: cell.bg_r, g: cell.bg_g, b: cell.bg_b };
      const style = `${cell.flags & 2 ? 'italic ' : ''}${cell.flags & 1 ? 'bold ' : ''}`;
      const key = `${style}${String.fromCodePoint(cell.codepoint)}`;
      checksum += (stringGlyphs.get(key) ?? 0) + fg.r + bg.r;
    }
  }
  return checksum;
}

function newCellLoop() {
  let checksum = 0;
  for (let frame = 0; frame < frames; frame += 1) {
    for (const cell of cells) {
      const fg = cell.fg_r << 16 | cell.fg_g << 8 | cell.fg_b;
      const bg = cell.bg_r << 16 | cell.bg_g << 8 | cell.bg_b;
      const style = (cell.flags & 2 ? 1 : 0) | (cell.flags & 1 ? 2 : 0);
      checksum += (numericGlyphs.get(cell.codepoint * 4 + style) ?? 0)
        + (fg >>> 16 & 0xff)
        + (bg >>> 16 & 0xff);
    }
  }
  return checksum;
}

function measure(run) {
  const startedAt = performance.now();
  const checksum = run();
  return { ms: performance.now() - startedAt, checksum };
}

measure(oldCellLoop);
measure(newCellLoop);

const runs = Array.from({ length: 5 }, () => {
  const oldResult = measure(oldCellLoop);
  const newResult = measure(newCellLoop);
  if (oldResult.checksum !== newResult.checksum) {
    throw new Error(`checksum mismatch: ${oldResult.checksum} != ${newResult.checksum}`);
  }
  return {
    oldMs: Number(oldResult.ms.toFixed(2)),
    newMs: Number(newResult.ms.toFixed(2)),
    speedup: Number((oldResult.ms / newResult.ms).toFixed(2)),
  };
});

console.log(JSON.stringify({ cols, rows, frames, runs }, null, 2));
