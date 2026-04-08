import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { analyzePanePixelCoverage } from './paneNativeAnalysis.mjs';

const execFileAsync = promisify(execFile);

function clampRect(value, max) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.max(0, Math.min(Math.round(value), Math.max(0, max)));
}

function resolveTargetMetrics(state, target) {
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

async function runPythonJson(script, payload) {
  const { stdout, stderr } = await execFileAsync('python3', ['-c', script, JSON.stringify(payload)], {
    timeout: 20_000,
    maxBuffer: 16 * 1024 * 1024,
  });

  if (stderr?.trim()) {
    throw new Error(stderr.trim());
  }

  return JSON.parse(stdout);
}

async function readImageMetadata(imagePath) {
  const script = `
import json
import sys
from PIL import Image

payload = json.loads(sys.argv[1])
image = Image.open(payload["imagePath"]).convert("RGBA")
image_width, image_height = image.size
print(json.dumps({
    "width": image_width,
    "height": image_height,
}))
`;

  return runPythonJson(script, { imagePath });
}

async function extractCropPixels({ imagePath, cropPath, crop }) {
  const script = `
import base64
import json
import sys
from PIL import Image

payload = json.loads(sys.argv[1])
image = Image.open(payload["imagePath"]).convert("RGBA")
image_width, image_height = image.size
crop = payload["crop"]
x = max(0, min(int(round(crop["x"])), image_width))
y = max(0, min(int(round(crop["y"])), image_height))
width = max(1, min(int(round(crop["width"])), image_width - x))
height = max(1, min(int(round(crop["height"])), image_height - y))
cropped = image.crop((x, y, x + width, y + height))
crop_width, crop_height = cropped.size
crop_path = payload.get("cropPath")
if crop_path:
    cropped.save(crop_path)

raw = base64.b64encode(cropped.tobytes()).decode("ascii")
print(json.dumps({
    "width": crop_width,
    "height": crop_height,
    "pixelsBase64": raw,
    "crop": {
        "x": x,
        "y": y,
        "width": crop_width,
        "height": crop_height,
        "path": crop_path,
    },
}))
`;

  return runPythonJson(script, {
    imagePath,
    cropPath,
    crop,
  });
}

export async function analyzePngCropCoverage(
  imagePath,
  {
    cropPath = null,
    crop,
    insetPx = 2,
    activityThreshold = 18,
  },
) {
  const imageMetadata = await readImageMetadata(imagePath);
  const extracted = await extractCropPixels({
    imagePath,
    cropPath,
    crop,
  });
  const pixelData = Uint8Array.from(Buffer.from(extracted.pixelsBase64, 'base64'));
  const analysis = analyzePanePixelCoverage({
    width: extracted.width,
    height: extracted.height,
    data: pixelData,
  }, {
    insetPx,
    activityThreshold,
  });

  return {
    image: imageMetadata,
    crop: extracted.crop,
    analysis,
  };
}

export async function capturePaneNativeMetrics(
  client,
  runDir,
  prefix,
  sessionId,
  paneId,
  {
    target = 'paneBody',
    insetPx = 2,
    activityThreshold = 18,
    bundleId = 'com.attn.manager',
  } = {},
) {
  const state = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
  const targetBounds = resolveTargetMetrics(state, target);
  const screenshotPath = path.join(runDir, `${prefix}-${paneId}-${target}-window.png`);
  const cropPath = path.join(runDir, `${prefix}-${paneId}-${target}-crop.png`);
  const summaryPath = path.join(runDir, `${prefix}-${paneId}-${target}-analysis.json`);

  const nativeScreenshot = await captureFrontWindowScreenshot(screenshotPath, { bundleId });
  const nativeWidth = nativeScreenshot?.bounds?.width || 0;
  const nativeHeight = nativeScreenshot?.bounds?.height || 0;
  const imageMetadata = await readImageMetadata(screenshotPath);
  const scaleX = nativeWidth > 0 ? imageMetadata.width / nativeWidth : 1;
  const scaleY = nativeHeight > 0 ? imageMetadata.height / nativeHeight : 1;
  const scaledCrop = {
    x: clampRect(targetBounds.x * scaleX, imageMetadata.width),
    y: clampRect(targetBounds.y * scaleY, imageMetadata.height),
    width: clampRect(targetBounds.width * scaleX, imageMetadata.width),
    height: clampRect(targetBounds.height * scaleY, imageMetadata.height),
  };

  const analyzed = await analyzePngCropCoverage(screenshotPath, {
    cropPath,
    crop: scaledCrop,
    insetPx,
    activityThreshold,
  });

  const result = {
    sessionId,
    paneId,
    target,
    cssBounds: targetBounds,
    nativeScreenshot,
    scale: {
      x: scaleX,
      y: scaleY,
    },
    crop: analyzed.crop,
    image: analyzed.image,
    analysis: analyzed.analysis,
    paneState: {
      bounds: state?.pane?.bounds || null,
      paneBodyBounds: state?.pane?.dom?.paneBody?.bounds || null,
      xtermScreenBounds: state?.pane?.dom?.xtermScreen?.bounds || null,
      visibleContent: state?.pane?.visibleContent || null,
      renderHealth: state?.renderHealth || null,
    },
  };

  fs.writeFileSync(summaryPath, `${JSON.stringify(result, null, 2)}\n`, 'utf8');
  return result;
}
