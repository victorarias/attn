function safeRatio(value, total) {
  if (!Number.isFinite(value) || !Number.isFinite(total) || total <= 0) {
    return 0;
  }
  return value / total;
}

function dominantColorKey(samples) {
  const counts = new Map();
  for (const sample of samples) {
    const key = `${sample.r},${sample.g},${sample.b},${sample.a}`;
    counts.set(key, (counts.get(key) || 0) + 1);
  }

  let bestKey = '0,0,0,255';
  let bestCount = -1;
  for (const [key, count] of counts.entries()) {
    if (count > bestCount) {
      bestKey = key;
      bestCount = count;
    }
  }
  const [r, g, b, a] = bestKey.split(',').map((value) => Number.parseInt(value, 10));
  return { r, g, b, a };
}

function readPixel(data, width, x, y) {
  const offset = (y * width + x) * 4;
  return {
    r: data[offset] ?? 0,
    g: data[offset + 1] ?? 0,
    b: data[offset + 2] ?? 0,
    a: data[offset + 3] ?? 0,
  };
}

export function estimatePaneBackgroundColor(
  {
    width,
    height,
    data,
  },
  {
    insetPx = 2,
    sampleDivisor = 80,
  } = {},
) {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return { r: 0, g: 0, b: 0, a: 255 };
  }

  const maxX = Math.max(0, width - 1);
  const maxY = Math.max(0, height - 1);
  const insetX = Math.min(Math.max(0, Math.round(insetPx)), maxX);
  const insetY = Math.min(Math.max(0, Math.round(insetPx)), maxY);
  const stepX = Math.max(1, Math.floor(width / sampleDivisor));
  const stepY = Math.max(1, Math.floor(height / sampleDivisor));
  const samples = [];

  for (let x = insetX; x <= maxX - insetX; x += stepX) {
    samples.push(readPixel(data, width, x, insetY));
    samples.push(readPixel(data, width, x, Math.max(insetY, maxY - insetY)));
  }

  for (let y = insetY; y <= maxY - insetY; y += stepY) {
    samples.push(readPixel(data, width, insetX, y));
    samples.push(readPixel(data, width, Math.max(insetX, maxX - insetX), y));
  }

  if (samples.length === 0) {
    samples.push(readPixel(data, width, 0, 0));
  }

  return dominantColorKey(samples);
}

export function analyzePanePixelCoverage(
  {
    width,
    height,
    data,
  },
  {
    insetPx = 2,
    activityThreshold = 18,
  } = {},
) {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return {
      backgroundColor: { r: 0, g: 0, b: 0, a: 255 },
      activePixelCount: 0,
      activePixelRatio: 0,
      busyRowCount: 0,
      busyColumnCount: 0,
      busyRowRatio: 0,
      busyColumnRatio: 0,
      rowThresholdPixels: 0,
      columnThresholdPixels: 0,
      bbox: null,
      bboxWidthRatio: 0,
      bboxHeightRatio: 0,
      maxRowActivity: 0,
      maxRowActivityRatio: 0,
      maxColumnActivity: 0,
      maxColumnActivityRatio: 0,
    };
  }

  const backgroundColor = estimatePaneBackgroundColor({ width, height, data }, { insetPx });
  const rowThresholdPixels = Math.max(4, Math.round(width * 0.01));
  const columnThresholdPixels = Math.max(4, Math.round(height * 0.01));
  const rowActivity = new Array(height).fill(0);
  const columnActivity = new Array(width).fill(0);
  let activePixelCount = 0;
  let firstX = null;
  let firstY = null;
  let lastX = null;
  let lastY = null;

  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const pixel = readPixel(data, width, x, y);
      const delta = Math.max(
        Math.abs(pixel.r - backgroundColor.r),
        Math.abs(pixel.g - backgroundColor.g),
        Math.abs(pixel.b - backgroundColor.b),
      );

      if (pixel.a >= 200 && delta >= activityThreshold) {
        activePixelCount += 1;
        rowActivity[y] += 1;
        columnActivity[x] += 1;
        if (firstX === null || x < firstX) firstX = x;
        if (lastX === null || x > lastX) lastX = x;
        if (firstY === null || y < firstY) firstY = y;
        if (lastY === null || y > lastY) lastY = y;
      }
    }
  }

  const busyRowCount = rowActivity.filter((count) => count >= rowThresholdPixels).length;
  const busyColumnCount = columnActivity.filter((count) => count >= columnThresholdPixels).length;
  const bbox = (
    firstX !== null &&
    firstY !== null &&
    lastX !== null &&
    lastY !== null
  ) ? {
    x: firstX,
    y: firstY,
    width: lastX - firstX + 1,
    height: lastY - firstY + 1,
  } : null;

  const maxRowActivity = rowActivity.reduce((max, value) => Math.max(max, value), 0);
  const maxColumnActivity = columnActivity.reduce((max, value) => Math.max(max, value), 0);

  return {
    backgroundColor,
    activePixelCount,
    activePixelRatio: safeRatio(activePixelCount, width * height),
    busyRowCount,
    busyColumnCount,
    busyRowRatio: safeRatio(busyRowCount, height),
    busyColumnRatio: safeRatio(busyColumnCount, width),
    rowThresholdPixels,
    columnThresholdPixels,
    bbox,
    bboxWidthRatio: safeRatio(bbox?.width ?? 0, width),
    bboxHeightRatio: safeRatio(bbox?.height ?? 0, height),
    maxRowActivity,
    maxRowActivityRatio: safeRatio(maxRowActivity, width),
    maxColumnActivity,
    maxColumnActivityRatio: safeRatio(maxColumnActivity, height),
  };
}

export function evaluatePaneNativePaintCoverage(
  analysis,
  {
    minBusyColumnRatio = 0.45,
    minBusyRowRatio = 0.18,
    minBBoxWidthRatio = 0.45,
    minBBoxHeightRatio = 0.18,
  } = {},
) {
  const failures = [];

  if ((analysis?.busyColumnRatio || 0) < minBusyColumnRatio) {
    failures.push(`busyColumnRatio=${Number((analysis?.busyColumnRatio || 0).toFixed(3))} < ${minBusyColumnRatio}`);
  }
  if ((analysis?.busyRowRatio || 0) < minBusyRowRatio) {
    failures.push(`busyRowRatio=${Number((analysis?.busyRowRatio || 0).toFixed(3))} < ${minBusyRowRatio}`);
  }
  if ((analysis?.bboxWidthRatio || 0) < minBBoxWidthRatio) {
    failures.push(`bboxWidthRatio=${Number((analysis?.bboxWidthRatio || 0).toFixed(3))} < ${minBBoxWidthRatio}`);
  }
  if ((analysis?.bboxHeightRatio || 0) < minBBoxHeightRatio) {
    failures.push(`bboxHeightRatio=${Number((analysis?.bboxHeightRatio || 0).toFixed(3))} < ${minBBoxHeightRatio}`);
  }

  return {
    ok: failures.length === 0,
    failures,
  };
}

export function comparePaneNativePaintCoverage(
  baseline,
  candidate,
  {
    maxBusyColumnRatioDelta = 0.08,
    maxBusyRowRatioDelta = 0.08,
    maxBBoxWidthRatioDelta = 0.08,
    maxBBoxHeightRatioDelta = 0.08,
    maxActivePixelRatioDelta = 0.03,
  } = {},
) {
  const deltas = {
    busyColumnRatioDelta: Math.abs((baseline?.busyColumnRatio || 0) - (candidate?.busyColumnRatio || 0)),
    busyRowRatioDelta: Math.abs((baseline?.busyRowRatio || 0) - (candidate?.busyRowRatio || 0)),
    bboxWidthRatioDelta: Math.abs((baseline?.bboxWidthRatio || 0) - (candidate?.bboxWidthRatio || 0)),
    bboxHeightRatioDelta: Math.abs((baseline?.bboxHeightRatio || 0) - (candidate?.bboxHeightRatio || 0)),
    activePixelRatioDelta: Math.abs((baseline?.activePixelRatio || 0) - (candidate?.activePixelRatio || 0)),
  };

  const failures = [];

  if (Number.isFinite(maxBusyColumnRatioDelta) && deltas.busyColumnRatioDelta > maxBusyColumnRatioDelta) {
    failures.push(
      `busyColumnRatioDelta=${Number(deltas.busyColumnRatioDelta.toFixed(6))} > ${maxBusyColumnRatioDelta}`
    );
  }
  if (Number.isFinite(maxBusyRowRatioDelta) && deltas.busyRowRatioDelta > maxBusyRowRatioDelta) {
    failures.push(
      `busyRowRatioDelta=${Number(deltas.busyRowRatioDelta.toFixed(6))} > ${maxBusyRowRatioDelta}`
    );
  }
  if (Number.isFinite(maxBBoxWidthRatioDelta) && deltas.bboxWidthRatioDelta > maxBBoxWidthRatioDelta) {
    failures.push(
      `bboxWidthRatioDelta=${Number(deltas.bboxWidthRatioDelta.toFixed(6))} > ${maxBBoxWidthRatioDelta}`
    );
  }
  if (Number.isFinite(maxBBoxHeightRatioDelta) && deltas.bboxHeightRatioDelta > maxBBoxHeightRatioDelta) {
    failures.push(
      `bboxHeightRatioDelta=${Number(deltas.bboxHeightRatioDelta.toFixed(6))} > ${maxBBoxHeightRatioDelta}`
    );
  }
  if (Number.isFinite(maxActivePixelRatioDelta) && deltas.activePixelRatioDelta > maxActivePixelRatioDelta) {
    failures.push(
      `activePixelRatioDelta=${Number(deltas.activePixelRatioDelta.toFixed(6))} > ${maxActivePixelRatioDelta}`
    );
  }

  return {
    ok: failures.length === 0,
    deltas,
    failures,
  };
}
