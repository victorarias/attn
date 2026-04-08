import fs from 'node:fs';
import { PNG } from 'pngjs';

function clampByte(value) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.max(0, Math.min(255, Math.round(value)));
}

function setPixel(data, width, x, y, color) {
  const offset = (y * width + x) * 4;
  data[offset] = clampByte(color[0]);
  data[offset + 1] = clampByte(color[1]);
  data[offset + 2] = clampByte(color[2]);
  data[offset + 3] = clampByte(color[3] ?? 255);
}

function fillRect(data, width, height, rect, color) {
  const x0 = Math.max(0, Math.round(rect.x));
  const y0 = Math.max(0, Math.round(rect.y));
  const x1 = Math.min(width - 1, Math.round(rect.x + rect.width - 1));
  const y1 = Math.min(height - 1, Math.round(rect.y + rect.height - 1));

  for (let y = y0; y <= y1; y += 1) {
    for (let x = x0; x <= x1; x += 1) {
      setPixel(data, width, x, y, color);
    }
  }
}

function drawOutline(data, width, height, rect, color, outlineWidth = 1) {
  for (let offset = 0; offset < outlineWidth; offset += 1) {
    const expanded = {
      x: rect.x - offset,
      y: rect.y - offset,
      width: rect.width + offset * 2,
      height: rect.height + offset * 2,
    };

    fillRect(data, width, height, { x: expanded.x, y: expanded.y, width: expanded.width, height: 1 }, color);
    fillRect(data, width, height, { x: expanded.x, y: expanded.y + expanded.height - 1, width: expanded.width, height: 1 }, color);
    fillRect(data, width, height, { x: expanded.x, y: expanded.y, width: 1, height: expanded.height }, color);
    fillRect(data, width, height, { x: expanded.x + expanded.width - 1, y: expanded.y, width: 1, height: expanded.height }, color);
  }
}

export function createFixturePngBuffer(fixture) {
  const width = Math.max(1, Math.round(fixture.width));
  const height = Math.max(1, Math.round(fixture.height));
  const png = new PNG({ width, height });
  const background = fixture.background || [0, 0, 0, 255];

  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      setPixel(png.data, width, x, y, background);
    }
  }

  for (const rect of fixture.rects || []) {
    fillRect(png.data, width, height, rect, rect.fill);
    if (rect.outline) {
      drawOutline(png.data, width, height, rect, rect.outline, rect.outlineWidth || 1);
    }
  }

  return PNG.sync.write(png);
}

export function writeFixturePng(filePath, fixture) {
  fs.writeFileSync(filePath, createFixturePngBuffer(fixture));
}
