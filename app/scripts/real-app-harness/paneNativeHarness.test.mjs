import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  analyzePanePixelCoverage,
  comparePaneNativePaintCoverage,
  comparePaneNativePaintRegression,
  evaluatePaneNativePaintCoverage,
} from './paneNativeAnalysis.mjs';
import { analyzePngCropCoverage } from './paneNativeMetrics.mjs';
import { writeFixturePng } from './pngFixtureUtils.mjs';

const createdDirs = [];

function rgba(r, g, b, a = 255) {
  return [r, g, b, a];
}

function buildImage(width, height, background, painter) {
  const data = new Uint8Array(width * height * 4);

  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const color = painter?.(x, y) || background;
      const offset = (y * width + x) * 4;
      data[offset] = color[0];
      data[offset + 1] = color[1];
      data[offset + 2] = color[2];
      data[offset + 3] = color[3];
    }
  }

  return data;
}

function makeTempDir() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-pane-native-'));
  createdDirs.push(dir);
  return dir;
}

function writeFixtureImage(filePath, fixture) {
  writeFixturePng(filePath, fixture);
}

afterEach(() => {
  while (createdDirs.length > 0) {
    const dir = createdDirs.pop();
    if (dir) {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  }
});

describe('pane native analysis', () => {
  it('passes a healthy full-area synthetic pane', () => {
    const width = 120;
    const height = 80;
    const background = rgba(30, 30, 30);
    const data = buildImage(width, height, background, (x, y) => {
      if (x >= 12 && x <= 108 && y >= 10 && y <= 52) {
        return rgba(220, 220, 220);
      }
      return background;
    });

    const analysis = analyzePanePixelCoverage({ width, height, data });
    expect(analysis.busyColumnRatio).toBeGreaterThan(0.75);
    expect(analysis.busyRowRatio).toBeGreaterThan(0.45);
    expect(analysis.bboxWidthRatio).toBeGreaterThan(0.75);
    expect(analysis.bboxHeightRatio).toBeGreaterThan(0.5);

    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio: 0.5,
      minBusyRowRatio: 0.2,
      minBBoxWidthRatio: 0.5,
      minBBoxHeightRatio: 0.2,
    });
    expect(evaluation.ok).toBe(true);
    const comparison = comparePaneNativePaintCoverage(analysis, analysis);
    expect(comparison.ok).toBe(true);
    expect(comparison.deltas).toEqual({
      busyColumnRatioDelta: 0,
      busyRowRatioDelta: 0,
      bboxWidthRatioDelta: 0,
      bboxHeightRatioDelta: 0,
      activePixelRatioDelta: 0,
    });
  });

  it('fails a narrow left-strip render', () => {
    const width = 120;
    const height = 80;
    const background = rgba(30, 30, 30);
    const data = buildImage(width, height, background, (x, y) => {
      if (x >= 4 && x <= 20 && y >= 8 && y <= 58) {
        return rgba(230, 230, 230);
      }
      return background;
    });

    const analysis = analyzePanePixelCoverage({ width, height, data });
    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio: 0.45,
      minBusyRowRatio: 0.18,
      minBBoxWidthRatio: 0.45,
      minBBoxHeightRatio: 0.18,
    });
    expect(evaluation.ok).toBe(false);
    expect(evaluation.failures.some((failure) => failure.includes('busyColumnRatio'))).toBe(true);
    expect(evaluation.failures.some((failure) => failure.includes('bboxWidthRatio'))).toBe(true);
  });

  it('fails a footer-band render', () => {
    const width = 120;
    const height = 80;
    const background = rgba(30, 30, 30);
    const data = buildImage(width, height, background, (x, y) => {
      if (x >= 8 && x <= 112 && y >= 62 && y <= 74) {
        return rgba(210, 210, 210);
      }
      return background;
    });

    const analysis = analyzePanePixelCoverage({ width, height, data });
    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio: 0.45,
      minBusyRowRatio: 0.18,
      minBBoxWidthRatio: 0.45,
      minBBoxHeightRatio: 0.18,
    });
    expect(evaluation.ok).toBe(false);
    expect(evaluation.failures.some((failure) => failure.includes('busyRowRatio'))).toBe(true);
    expect(evaluation.failures.some((failure) => failure.includes('bboxHeightRatio'))).toBe(true);
  });

  it('analyzes real PNG fixtures written to disk', async () => {
    const dir = makeTempDir();
    const imagePath = path.join(dir, 'wide.png');
    const cropPath = path.join(dir, 'wide-crop.png');
    writeFixtureImage(imagePath, {
      width: 420,
      height: 260,
      background: [32, 32, 32, 255],
      rects: [
        { x: 18, y: 22, width: 270, height: 32, fill: [220, 220, 220, 255] },
        { x: 18, y: 74, width: 344, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 98, width: 332, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 122, width: 352, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 146, width: 338, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 170, width: 326, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 208, width: 380, height: 26, fill: [76, 76, 76, 255] },
      ],
    });

    const result = await analyzePngCropCoverage(imagePath, {
      cropPath,
      crop: { x: 0, y: 0, width: 420, height: 260 },
    });

    expect(fs.existsSync(cropPath)).toBe(true);
    expect(result.image).toEqual({ width: 420, height: 260 });
    expect(result.analysis.busyColumnRatio).toBeGreaterThan(0.75);
    expect(result.analysis.busyRowRatio).toBeGreaterThan(0.35);

    const evaluation = evaluatePaneNativePaintCoverage(result.analysis, {
      minBusyColumnRatio: 0.45,
      minBusyRowRatio: 0.18,
      minBBoxWidthRatio: 0.45,
      minBBoxHeightRatio: 0.18,
    });
    expect(evaluation.ok).toBe(true);
  });

  it('fails a real PNG fixture that paints only a narrow strip', async () => {
    const dir = makeTempDir();
    const imagePath = path.join(dir, 'narrow.png');
    writeFixtureImage(imagePath, {
      width: 420,
      height: 260,
      background: [32, 32, 32, 255],
      rects: [
        { x: 10, y: 18, width: 84, height: 28, fill: [220, 220, 220, 255] },
        { x: 10, y: 68, width: 78, height: 12, fill: [170, 170, 170, 255] },
        { x: 10, y: 92, width: 80, height: 12, fill: [170, 170, 170, 255] },
        { x: 10, y: 116, width: 76, height: 12, fill: [170, 170, 170, 255] },
        { x: 10, y: 140, width: 82, height: 12, fill: [170, 170, 170, 255] },
        { x: 10, y: 164, width: 74, height: 12, fill: [170, 170, 170, 255] },
      ],
    });

    const result = await analyzePngCropCoverage(imagePath, {
      crop: { x: 0, y: 0, width: 420, height: 260 },
    });

    const evaluation = evaluatePaneNativePaintCoverage(result.analysis, {
      minBusyColumnRatio: 0.45,
      minBusyRowRatio: 0.18,
      minBBoxWidthRatio: 0.45,
      minBBoxHeightRatio: 0.18,
    });
    expect(evaluation.ok).toBe(false);
    expect(evaluation.failures.some((failure) => failure.includes('busyColumnRatio'))).toBe(true);
    expect(evaluation.failures.some((failure) => failure.includes('bboxWidthRatio'))).toBe(true);
  });

  it('accepts a subtle healthy delta within tolerance', async () => {
    const dir = makeTempDir();
    const basePath = path.join(dir, 'base.png');
    const subtlePath = path.join(dir, 'subtle.png');
    const common = {
      width: 420,
      height: 260,
      background: [32, 32, 32, 255],
    };

    writeFixtureImage(basePath, {
      ...common,
      rects: [
        { x: 18, y: 22, width: 270, height: 32, fill: [220, 220, 220, 255] },
        { x: 18, y: 74, width: 344, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 98, width: 332, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 122, width: 352, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 146, width: 338, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 170, width: 326, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 208, width: 380, height: 26, fill: [76, 76, 76, 255] },
      ],
    });

    writeFixtureImage(subtlePath, {
      ...common,
      rects: [
        { x: 18, y: 22, width: 270, height: 32, fill: [220, 220, 220, 255] },
        { x: 18, y: 74, width: 344, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 98, width: 332, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 122, width: 352, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 146, width: 338, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 170, width: 326, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 194, width: 168, height: 10, fill: [170, 170, 170, 255] },
        { x: 18, y: 208, width: 380, height: 26, fill: [76, 76, 76, 255] },
      ],
    });

    const base = await analyzePngCropCoverage(basePath, {
      crop: { x: 0, y: 0, width: 420, height: 260 },
    });
    const subtle = await analyzePngCropCoverage(subtlePath, {
      crop: { x: 0, y: 0, width: 420, height: 260 },
    });

    const comparison = comparePaneNativePaintCoverage(base.analysis, subtle.analysis, {
      maxBusyColumnRatioDelta: 0.05,
      maxBusyRowRatioDelta: 0.05,
      maxBBoxWidthRatioDelta: 0.05,
      maxBBoxHeightRatioDelta: 0.05,
      maxActivePixelRatioDelta: 0.03,
    });
    expect(comparison.ok).toBe(true);
    expect(comparison.deltas.busyRowRatioDelta).toBeGreaterThan(0);
  });

  it('rejects a materially degraded before/after delta', async () => {
    const dir = makeTempDir();
    const basePath = path.join(dir, 'healthy.png');
    const degradedPath = path.join(dir, 'degraded.png');
    const common = {
      width: 420,
      height: 260,
      background: [32, 32, 32, 255],
    };

    writeFixtureImage(basePath, {
      ...common,
      rects: [
        { x: 18, y: 22, width: 270, height: 32, fill: [220, 220, 220, 255] },
        { x: 18, y: 74, width: 344, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 98, width: 332, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 122, width: 352, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 146, width: 338, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 170, width: 326, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 208, width: 380, height: 26, fill: [76, 76, 76, 255] },
      ],
    });

    writeFixtureImage(degradedPath, {
      ...common,
      rects: [
        { x: 18, y: 208, width: 180, height: 24, fill: [76, 76, 76, 255] },
        { x: 18, y: 74, width: 90, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 98, width: 88, height: 12, fill: [170, 170, 170, 255] },
        { x: 18, y: 122, width: 92, height: 12, fill: [170, 170, 170, 255] },
      ],
    });

    const base = await analyzePngCropCoverage(basePath, {
      crop: { x: 0, y: 0, width: 420, height: 260 },
    });
    const degraded = await analyzePngCropCoverage(degradedPath, {
      crop: { x: 0, y: 0, width: 420, height: 260 },
    });

    const comparison = comparePaneNativePaintCoverage(base.analysis, degraded.analysis, {
      maxBusyColumnRatioDelta: 0.08,
      maxBusyRowRatioDelta: 0.08,
      maxBBoxWidthRatioDelta: 0.08,
      maxBBoxHeightRatioDelta: 0.08,
      maxActivePixelRatioDelta: 0.03,
    });
    expect(comparison.ok).toBe(false);
    expect(comparison.failures.length).toBeGreaterThan(0);
  });

  it('allows callers to ignore selected delta metrics', () => {
    const baseline = {
      busyColumnRatio: 0.9,
      busyRowRatio: 0.4,
      bboxWidthRatio: 1,
      bboxHeightRatio: 1,
      activePixelRatio: 0.16,
    };
    const candidate = {
      busyColumnRatio: 0.86,
      busyRowRatio: 0.36,
      bboxWidthRatio: 0.96,
      bboxHeightRatio: 0.96,
      activePixelRatio: 0.07,
    };

    const strict = comparePaneNativePaintCoverage(baseline, candidate, {
      maxBusyColumnRatioDelta: 0.08,
      maxBusyRowRatioDelta: 0.08,
      maxBBoxWidthRatioDelta: 0.08,
      maxBBoxHeightRatioDelta: 0.08,
      maxActivePixelRatioDelta: 0.04,
    });
    expect(strict.ok).toBe(false);
    expect(strict.failures.some((failure) => failure.includes('activePixelRatioDelta'))).toBe(true);

    const reflowAllowed = comparePaneNativePaintCoverage(baseline, candidate, {
      maxBusyColumnRatioDelta: 0.08,
      maxBusyRowRatioDelta: 0.08,
      maxBBoxWidthRatioDelta: 0.08,
      maxBBoxHeightRatioDelta: 0.08,
      maxActivePixelRatioDelta: null,
    });
    expect(reflowAllowed.ok).toBe(true);
  });

  it('allows paint growth while still rejecting native paint regression', () => {
    const baseline = {
      busyColumnRatio: 0.86,
      busyRowRatio: 0.26,
      bboxWidthRatio: 0.96,
      bboxHeightRatio: 0.49,
      activePixelRatio: 0.06,
    };
    const expanded = {
      busyColumnRatio: 0.97,
      busyRowRatio: 0.56,
      bboxWidthRatio: 0.97,
      bboxHeightRatio: 0.71,
      activePixelRatio: 0.39,
    };
    const collapsed = {
      busyColumnRatio: 0.62,
      busyRowRatio: 0.11,
      bboxWidthRatio: 0.58,
      bboxHeightRatio: 0.14,
      activePixelRatio: 0.03,
    };

    const expandedComparison = comparePaneNativePaintRegression(baseline, expanded, {
      maxBusyColumnRatioRegression: 0.1,
      maxBusyRowRatioRegression: 0.1,
      maxBBoxWidthRatioRegression: 0.1,
      maxBBoxHeightRatioRegression: 0.1,
      maxActivePixelRatioRegression: null,
    });
    expect(expandedComparison.ok).toBe(true);
    expect(expandedComparison.regressions).toEqual({
      busyColumnRatioRegression: 0,
      busyRowRatioRegression: 0,
      bboxWidthRatioRegression: 0,
      bboxHeightRatioRegression: 0,
      activePixelRatioRegression: 0,
    });

    const collapsedComparison = comparePaneNativePaintRegression(baseline, collapsed, {
      maxBusyColumnRatioRegression: 0.1,
      maxBusyRowRatioRegression: 0.1,
      maxBBoxWidthRatioRegression: 0.1,
      maxBBoxHeightRatioRegression: 0.1,
      maxActivePixelRatioRegression: null,
    });
    expect(collapsedComparison.ok).toBe(false);
    expect(collapsedComparison.failures.some((failure) => failure.includes('busyRowRatioRegression'))).toBe(true);
    expect(collapsedComparison.failures.some((failure) => failure.includes('bboxHeightRatioRegression'))).toBe(true);
  });
});
