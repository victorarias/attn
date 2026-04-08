#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { analyzePngCropCoverage } from './paneNativeMetrics.mjs';
import { comparePaneNativePaintCoverage, evaluatePaneNativePaintCoverage } from './paneNativeAnalysis.mjs';

const execFileAsync = promisify(execFile);

function timestampSlug() {
  return new Date().toISOString().replace(/[:.]/g, '-');
}

function buildFixture(name, variant = 'base') {
  const width = 980;
  const height = 520;
  const background = [33, 31, 32, 255];
  const text = [209, 209, 209, 255];
  const muted = [136, 136, 136, 255];
  const accent = [57, 184, 200, 255];
  const footer = [63, 63, 63, 255];

  const rects = [
    { x: 16, y: 18, width: 372, height: 146, fill: [34, 32, 33, 255], outline: [154, 154, 154, 255], outlineWidth: 2 },
    { x: 28, y: 38, width: 12, height: 6, fill: muted },
    { x: 48, y: 30, width: 218, height: 18, fill: text },
    { x: 285, y: 31, width: 108, height: 14, fill: muted },
    { x: 42, y: 88, width: 82, height: 12, fill: muted },
    { x: 218, y: 88, width: 170, height: 12, fill: text },
    { x: 512, y: 88, width: 92, height: 12, fill: accent },
    { x: 42, y: 122, width: 118, height: 12, fill: muted },
    { x: 220, y: 122, width: 208, height: 12, fill: text },
    { x: 30, y: 214, width: 54, height: 16, fill: text },
    { x: 102, y: 214, width: 378, height: 14, fill: text },
    { x: 14, y: 302, width: 948, height: 94, fill: footer },
    { x: 20, y: 334, width: 18, height: 16, fill: text },
    { x: 44, y: 333, width: 414, height: 16, fill: muted },
    { x: 44, y: 408, width: 390, height: 14, fill: muted },
  ];

  if (variant === 'subtle') {
    rects.splice(10, 0,
      { x: 30, y: 246, width: 344, height: 14, fill: [188, 188, 188, 255] },
      { x: 30, y: 270, width: 328, height: 14, fill: [188, 188, 188, 255] },
    );
    rects[11] = { x: 14, y: 302, width: 948, height: 94, fill: [61, 61, 61, 255] };
    rects[13] = { x: 44, y: 333, width: 402, height: 16, fill: muted };
    rects[14] = { x: 44, y: 408, width: 412, height: 14, fill: muted };
  }

  return {
    name,
    width,
    height,
    background,
    rects,
  };
}

async function writeFixtureImage(filePath, fixture) {
  const script = `
import json
import sys
from PIL import Image, ImageDraw

payload = json.loads(sys.argv[1])
image = Image.new("RGBA", (payload["width"], payload["height"]), tuple(payload["background"]))
draw = ImageDraw.Draw(image)

for rect in payload.get("rects", []):
    fill = tuple(rect["fill"])
    x0 = rect["x"]
    y0 = rect["y"]
    x1 = rect["x"] + rect["width"] - 1
    y1 = rect["y"] + rect["height"] - 1
    draw.rectangle([x0, y0, x1, y1], fill=fill)
    if rect.get("outline"):
        outline_width = int(rect.get("outlineWidth", 1))
        for offset in range(outline_width):
            draw.rectangle(
                [x0 - offset, y0 - offset, x1 + offset, y1 + offset],
                outline=tuple(rect["outline"]),
            )

image.save(payload["filePath"])
`;

  await execFileAsync('python3', ['-c', script, JSON.stringify({
    ...fixture,
    filePath,
  })], {
    timeout: 20_000,
  });
}

function deltaSummary(left, right) {
  return {
    busyColumnRatioDelta: Number(Math.abs((left.busyColumnRatio || 0) - (right.busyColumnRatio || 0)).toFixed(6)),
    busyRowRatioDelta: Number(Math.abs((left.busyRowRatio || 0) - (right.busyRowRatio || 0)).toFixed(6)),
    bboxWidthRatioDelta: Number(Math.abs((left.bboxWidthRatio || 0) - (right.bboxWidthRatio || 0)).toFixed(6)),
    bboxHeightRatioDelta: Number(Math.abs((left.bboxHeightRatio || 0) - (right.bboxHeightRatio || 0)).toFixed(6)),
    activePixelRatioDelta: Number(Math.abs((left.activePixelRatio || 0) - (right.activePixelRatio || 0)).toFixed(6)),
  };
}

function renderMetricTable(metrics) {
  return `
    <table>
      <tr><th>Metric</th><th>Value</th></tr>
      <tr><td>busyColumnRatio</td><td>${metrics.busyColumnRatio.toFixed(3)}</td></tr>
      <tr><td>busyRowRatio</td><td>${metrics.busyRowRatio.toFixed(3)}</td></tr>
      <tr><td>bboxWidthRatio</td><td>${metrics.bboxWidthRatio.toFixed(3)}</td></tr>
      <tr><td>bboxHeightRatio</td><td>${metrics.bboxHeightRatio.toFixed(3)}</td></tr>
      <tr><td>activePixelRatio</td><td>${metrics.activePixelRatio.toFixed(3)}</td></tr>
    </table>
  `;
}

function renderDeltaTable(delta) {
  return `
    <table>
      <tr><th>Delta</th><th>Value</th></tr>
      <tr><td>busyColumnRatio</td><td>${delta.busyColumnRatioDelta.toFixed(6)}</td></tr>
      <tr><td>busyRowRatio</td><td>${delta.busyRowRatioDelta.toFixed(6)}</td></tr>
      <tr><td>bboxWidthRatio</td><td>${delta.bboxWidthRatioDelta.toFixed(6)}</td></tr>
      <tr><td>bboxHeightRatio</td><td>${delta.bboxHeightRatioDelta.toFixed(6)}</td></tr>
      <tr><td>activePixelRatio</td><td>${delta.activePixelRatioDelta.toFixed(6)}</td></tr>
    </table>
  `;
}

function renderPairEvaluation(evaluation) {
  const status = evaluation.ok ? 'within tolerance' : 'outside tolerance';
  const failures = evaluation.failures || [];
  return `
    <div class="pill">${status}</div>
    ${failures.length > 0 ? `<p>${failures.join(', ')}</p>` : ''}
  `;
}

function writeHtml(outputPath, summary) {
  const html = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Pane Native Demo</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #111213;
        --panel: #1c1b1c;
        --panel-2: #232223;
        --text: #ece8dd;
        --muted: #a8a093;
        --line: #3f3b37;
        --accent: #7ac7d1;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        padding: 32px;
        background: radial-gradient(circle at top left, #1d2428, var(--bg) 40%);
        color: var(--text);
        font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      }
      h1, h2, p { margin: 0 0 12px; }
      p { color: var(--muted); max-width: 900px; line-height: 1.45; }
      .section {
        margin-top: 28px;
        padding: 20px;
        border: 1px solid var(--line);
        background: rgba(28, 27, 28, 0.88);
      }
      .pair {
        display: grid;
        grid-template-columns: 1fr 1fr;
        gap: 16px;
        margin-top: 16px;
      }
      .card {
        padding: 14px;
        border: 1px solid var(--line);
        background: var(--panel-2);
      }
      .card img {
        width: 100%;
        display: block;
        border: 1px solid #55514b;
        background: #211f20;
      }
      .meta {
        margin-top: 12px;
      }
      table {
        width: 100%;
        border-collapse: collapse;
        margin-top: 10px;
      }
      th, td {
        text-align: left;
        padding: 6px 8px;
        border-top: 1px solid var(--line);
      }
      th { color: var(--accent); font-weight: 600; }
      .delta {
        margin-top: 16px;
        max-width: 520px;
      }
      .pill {
        display: inline-block;
        margin-top: 8px;
        padding: 4px 8px;
        border: 1px solid var(--line);
        color: var(--accent);
      }
    </style>
  </head>
  <body>
    <h1>Pane Native Assertion Demo</h1>
    <p>This demo does not do image A equals image B. The actual harness scores each pane image independently for width and height paint coverage. These pairs are here so you can visually inspect what “identical” and “slightly different” look like, while also seeing the per-image scores and pairwise metric deltas.</p>

    <div class="section">
      <h2>Identical Pair</h2>
      <p>These two images are byte-for-byte equivalent. Their metric deltas should be zero.</p>
      <div class="pair">
        <div class="card">
          <img src="${summary.identical.left.fileName}" alt="identical left" />
          <div class="meta">
            <div class="pill">${summary.identical.left.evaluation.ok ? 'passes' : 'fails'}</div>
            ${renderMetricTable(summary.identical.left.analysis)}
          </div>
        </div>
        <div class="card">
          <img src="${summary.identical.right.fileName}" alt="identical right" />
          <div class="meta">
            <div class="pill">${summary.identical.right.evaluation.ok ? 'passes' : 'fails'}</div>
            ${renderMetricTable(summary.identical.right.analysis)}
          </div>
        </div>
      </div>
      <div class="delta">
        ${renderPairEvaluation(summary.identical.pairEvaluation)}
        ${renderDeltaTable(summary.identical.delta)}
      </div>
    </div>

    <div class="section">
      <h2>Subtle Pair</h2>
      <p>These are intentionally close. The subtle version adds two extra mid-body lines and slightly changes footer/body spread. They should both still look healthy, but their coverage metrics are not identical.</p>
      <div class="pair">
        <div class="card">
          <img src="${summary.subtle.left.fileName}" alt="subtle base" />
          <div class="meta">
            <div class="pill">${summary.subtle.left.evaluation.ok ? 'passes' : 'fails'}</div>
            ${renderMetricTable(summary.subtle.left.analysis)}
          </div>
        </div>
        <div class="card">
          <img src="${summary.subtle.right.fileName}" alt="subtle variant" />
          <div class="meta">
            <div class="pill">${summary.subtle.right.evaluation.ok ? 'passes' : 'fails'}</div>
            ${renderMetricTable(summary.subtle.right.analysis)}
          </div>
        </div>
      </div>
      <div class="delta">
        ${renderPairEvaluation(summary.subtle.pairEvaluation)}
        ${renderDeltaTable(summary.subtle.delta)}
      </div>
    </div>
  </body>
</html>`;

  fs.writeFileSync(outputPath, html, 'utf8');
}

async function main() {
  const outputDir = path.join(os.tmpdir(), `attn-pane-native-demo-${timestampSlug()}`);
  fs.mkdirSync(outputDir, { recursive: true });

  const files = {
    identicalLeft: path.join(outputDir, 'identical-left.png'),
    identicalRight: path.join(outputDir, 'identical-right.png'),
    subtleLeft: path.join(outputDir, 'subtle-left.png'),
    subtleRight: path.join(outputDir, 'subtle-right.png'),
  };

  const baseFixture = buildFixture('identical-left', 'base');
  const subtleBaseFixture = buildFixture('subtle-left', 'base');
  const subtleVariantFixture = buildFixture('subtle-right', 'subtle');

  await writeFixtureImage(files.identicalLeft, baseFixture);
  fs.copyFileSync(files.identicalLeft, files.identicalRight);
  await writeFixtureImage(files.subtleLeft, subtleBaseFixture);
  await writeFixtureImage(files.subtleRight, subtleVariantFixture);

  const crop = { x: 0, y: 0, width: baseFixture.width, height: baseFixture.height };
  const identicalLeft = await analyzePngCropCoverage(files.identicalLeft, { crop });
  const identicalRight = await analyzePngCropCoverage(files.identicalRight, { crop });
  const subtleLeft = await analyzePngCropCoverage(files.subtleLeft, { crop });
  const subtleRight = await analyzePngCropCoverage(files.subtleRight, { crop });

  const thresholds = {
    minBusyColumnRatio: 0.45,
    minBusyRowRatio: 0.18,
    minBBoxWidthRatio: 0.45,
    minBBoxHeightRatio: 0.18,
  };

  const summary = {
    explanation: {
      mode: 'independent-pane-scoring',
      note: 'The harness does not compare image A to image B. It scores each pane crop independently. Pairwise deltas here are for human inspection only.',
      thresholds,
    },
    identical: {
      left: {
        fileName: path.basename(files.identicalLeft),
        path: files.identicalLeft,
        analysis: identicalLeft.analysis,
        evaluation: evaluatePaneNativePaintCoverage(identicalLeft.analysis, thresholds),
      },
      right: {
        fileName: path.basename(files.identicalRight),
        path: files.identicalRight,
        analysis: identicalRight.analysis,
        evaluation: evaluatePaneNativePaintCoverage(identicalRight.analysis, thresholds),
      },
      delta: deltaSummary(identicalLeft.analysis, identicalRight.analysis),
      pairEvaluation: comparePaneNativePaintCoverage(identicalLeft.analysis, identicalRight.analysis, {
        maxBusyColumnRatioDelta: 0.05,
        maxBusyRowRatioDelta: 0.05,
        maxBBoxWidthRatioDelta: 0.05,
        maxBBoxHeightRatioDelta: 0.05,
        maxActivePixelRatioDelta: 0.03,
      }),
    },
    subtle: {
      left: {
        fileName: path.basename(files.subtleLeft),
        path: files.subtleLeft,
        analysis: subtleLeft.analysis,
        evaluation: evaluatePaneNativePaintCoverage(subtleLeft.analysis, thresholds),
      },
      right: {
        fileName: path.basename(files.subtleRight),
        path: files.subtleRight,
        analysis: subtleRight.analysis,
        evaluation: evaluatePaneNativePaintCoverage(subtleRight.analysis, thresholds),
      },
      delta: deltaSummary(subtleLeft.analysis, subtleRight.analysis),
      pairEvaluation: comparePaneNativePaintCoverage(subtleLeft.analysis, subtleRight.analysis, {
        maxBusyColumnRatioDelta: 0.05,
        maxBusyRowRatioDelta: 0.05,
        maxBBoxWidthRatioDelta: 0.05,
        maxBBoxHeightRatioDelta: 0.05,
        maxActivePixelRatioDelta: 0.03,
      }),
    },
  };

  const summaryPath = path.join(outputDir, 'summary.json');
  const htmlPath = path.join(outputDir, 'index.html');
  fs.writeFileSync(summaryPath, `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
  writeHtml(htmlPath, summary);

  console.log(JSON.stringify({
    outputDir,
    htmlPath,
    summaryPath,
    files: {
      identicalLeft: files.identicalLeft,
      identicalRight: files.identicalRight,
      subtleLeft: files.subtleLeft,
      subtleRight: files.subtleRight,
    },
  }, null, 2));
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
