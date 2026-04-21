function safeRatio(value, total) {
  if (!Number.isFinite(value) || !Number.isFinite(total) || total <= 0) {
    return 0;
  }
  return value / total;
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

export function comparePaneNativePaintRegression(
  baseline,
  candidate,
  {
    maxBusyColumnRatioRegression = 0.08,
    maxBusyRowRatioRegression = 0.08,
    maxBBoxWidthRatioRegression = 0.08,
    maxBBoxHeightRatioRegression = 0.08,
    maxActivePixelRatioRegression = 0.03,
  } = {},
) {
  const regressions = {
    busyColumnRatioRegression: Math.max(0, (baseline?.busyColumnRatio || 0) - (candidate?.busyColumnRatio || 0)),
    busyRowRatioRegression: Math.max(0, (baseline?.busyRowRatio || 0) - (candidate?.busyRowRatio || 0)),
    bboxWidthRatioRegression: Math.max(0, (baseline?.bboxWidthRatio || 0) - (candidate?.bboxWidthRatio || 0)),
    bboxHeightRatioRegression: Math.max(0, (baseline?.bboxHeightRatio || 0) - (candidate?.bboxHeightRatio || 0)),
    activePixelRatioRegression: Math.max(0, (baseline?.activePixelRatio || 0) - (candidate?.activePixelRatio || 0)),
  };

  const failures = [];

  if (Number.isFinite(maxBusyColumnRatioRegression) && regressions.busyColumnRatioRegression > maxBusyColumnRatioRegression) {
    failures.push(
      `busyColumnRatioRegression=${Number(regressions.busyColumnRatioRegression.toFixed(6))} > ${maxBusyColumnRatioRegression}`
    );
  }
  if (Number.isFinite(maxBusyRowRatioRegression) && regressions.busyRowRatioRegression > maxBusyRowRatioRegression) {
    failures.push(
      `busyRowRatioRegression=${Number(regressions.busyRowRatioRegression.toFixed(6))} > ${maxBusyRowRatioRegression}`
    );
  }
  if (Number.isFinite(maxBBoxWidthRatioRegression) && regressions.bboxWidthRatioRegression > maxBBoxWidthRatioRegression) {
    failures.push(
      `bboxWidthRatioRegression=${Number(regressions.bboxWidthRatioRegression.toFixed(6))} > ${maxBBoxWidthRatioRegression}`
    );
  }
  if (Number.isFinite(maxBBoxHeightRatioRegression) && regressions.bboxHeightRatioRegression > maxBBoxHeightRatioRegression) {
    failures.push(
      `bboxHeightRatioRegression=${Number(regressions.bboxHeightRatioRegression.toFixed(6))} > ${maxBBoxHeightRatioRegression}`
    );
  }
  if (Number.isFinite(maxActivePixelRatioRegression) && regressions.activePixelRatioRegression > maxActivePixelRatioRegression) {
    failures.push(
      `activePixelRatioRegression=${Number(regressions.activePixelRatioRegression.toFixed(6))} > ${maxActivePixelRatioRegression}`
    );
  }

  return {
    ok: failures.length === 0,
    regressions,
    failures,
  };
}

// Text-grid equivalent of `analyzePanePixelCoverage`. Uses xterm's in-process
// buffer (cols × rows of cells, with glyph/whitespace) so coverage signal is
// independent of WKWebView compositor state — no screencap, no focus steal.
// Returns the same field shape consumed by evaluate/compare helpers above.
export function analyzePaneTextCoverage(
  {
    cols,
    lines,
  },
  {
    rowOccupancyRatioThreshold = 0,
    columnOccupancyRatioThreshold = 0,
  } = {},
) {
  const totalCols = Number.isFinite(cols) && cols > 0 ? Math.floor(cols) : 0;
  const rawLines = Array.isArray(lines) ? lines : [];
  const totalRows = rawLines.length;

  if (totalCols <= 0 || totalRows <= 0) {
    return {
      backgroundColor: null,
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

  const rowActivity = new Array(totalRows).fill(0);
  const columnActivity = new Array(totalCols).fill(0);
  let activeCellCount = 0;
  let firstCol = null;
  let firstRow = null;
  let lastCol = null;
  let lastRow = null;

  for (let r = 0; r < totalRows; r += 1) {
    const line = rawLines[r] || '';
    const lineCols = Math.min(line.length, totalCols);
    for (let c = 0; c < lineCols; c += 1) {
      const ch = line[c];
      // Treat anything but space and non-breaking space as an occupied cell.
      // This matches pixel coverage which fires on any glyph-drawn pixel.
      if (ch && ch !== ' ' && ch !== '\u00a0') {
        activeCellCount += 1;
        rowActivity[r] += 1;
        columnActivity[c] += 1;
        if (firstCol === null || c < firstCol) firstCol = c;
        if (lastCol === null || c > lastCol) lastCol = c;
        if (firstRow === null || r < firstRow) firstRow = r;
        if (lastRow === null || r > lastRow) lastRow = r;
      }
    }
  }

  const rowThreshold = Math.max(1, Math.round(totalCols * rowOccupancyRatioThreshold));
  const columnThreshold = Math.max(1, Math.round(totalRows * columnOccupancyRatioThreshold));
  const busyRowCount = rowActivity.filter((count) => count >= rowThreshold).length;
  const busyColumnCount = columnActivity.filter((count) => count >= columnThreshold).length;

  const bbox = (
    firstCol !== null &&
    firstRow !== null &&
    lastCol !== null &&
    lastRow !== null
  ) ? {
    x: firstCol,
    y: firstRow,
    width: lastCol - firstCol + 1,
    height: lastRow - firstRow + 1,
  } : null;

  const maxRowActivity = rowActivity.reduce((max, value) => Math.max(max, value), 0);
  const maxColumnActivity = columnActivity.reduce((max, value) => Math.max(max, value), 0);

  return {
    backgroundColor: null,
    activePixelCount: activeCellCount,
    activePixelRatio: safeRatio(activeCellCount, totalCols * totalRows),
    busyRowCount,
    busyColumnCount,
    busyRowRatio: safeRatio(busyRowCount, totalRows),
    busyColumnRatio: safeRatio(busyColumnCount, totalCols),
    rowThresholdPixels: rowThreshold,
    columnThresholdPixels: columnThreshold,
    bbox,
    bboxWidthRatio: safeRatio(bbox?.width ?? 0, totalCols),
    bboxHeightRatio: safeRatio(bbox?.height ?? 0, totalRows),
    maxRowActivity,
    maxRowActivityRatio: safeRatio(maxRowActivity, totalCols),
    maxColumnActivity,
    maxColumnActivityRatio: safeRatio(maxColumnActivity, totalRows),
  };
}
