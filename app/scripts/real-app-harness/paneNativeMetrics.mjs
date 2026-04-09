import fs from 'node:fs';
import path from 'node:path';
import { PNG } from 'pngjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { analyzePanePixelCoverage } from './paneNativeAnalysis.mjs';

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

async function readImageMetadata(imagePath) {
  const buffer = fs.readFileSync(imagePath);
  const image = PNG.sync.read(buffer);
  return {
    width: image.width,
    height: image.height,
  };
}

async function extractCropPixels({ imagePath, cropPath, crop }) {
  const buffer = fs.readFileSync(imagePath);
  const image = PNG.sync.read(buffer);
  const x = Math.max(0, Math.min(Math.round(crop.x), image.width));
  const y = Math.max(0, Math.min(Math.round(crop.y), image.height));
  const width = Math.max(1, Math.min(Math.round(crop.width), image.width - x));
  const height = Math.max(1, Math.min(Math.round(crop.height), image.height - y));
  const cropped = new PNG({ width, height });

  for (let row = 0; row < height; row += 1) {
    for (let col = 0; col < width; col += 1) {
      const srcOffset = ((y + row) * image.width + (x + col)) * 4;
      const dstOffset = (row * width + col) * 4;
      cropped.data[dstOffset] = image.data[srcOffset];
      cropped.data[dstOffset + 1] = image.data[srcOffset + 1];
      cropped.data[dstOffset + 2] = image.data[srcOffset + 2];
      cropped.data[dstOffset + 3] = image.data[srcOffset + 3];
    }
  }

  if (cropPath) {
    fs.writeFileSync(cropPath, PNG.sync.write(cropped));
  }

  return {
    width,
    height,
    pixelsBase64: Buffer.from(cropped.data).toString('base64'),
    crop: {
      x,
      y,
      width,
      height,
      path: cropPath,
    },
  };
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

  const nativeScreenshot = await captureWindowScreenshot(client, screenshotPath, { bundleId });
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

async function captureWindowScreenshot(client, screenshotPath, { bundleId }) {
  try {
    return await captureFrontWindowScreenshot(screenshotPath, { bundleId });
  } catch {
    // Fall through to the app-side capture path.
  }

  const result = await client.request('capture_window_screenshot', {
    path: screenshotPath,
    bundleId,
  }, { timeoutMs: 20_000 });
  if (result?.path && result?.bounds) {
    return {
      source: 'ui_automation_window',
      bundleId,
      bounds: result.bounds,
      path: result.path,
    };
  }
  throw new Error(`capture_window_screenshot returned no image for ${bundleId}`);
}
