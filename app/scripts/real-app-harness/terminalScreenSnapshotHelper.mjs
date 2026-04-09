function colorModeForCell(cell) {
  return {
    fgMode: cell.isFgRGB() ? 'rgb' : cell.isFgPalette() ? 'palette' : 'default',
    bgMode: cell.isBgRGB() ? 'rgb' : cell.isBgPalette() ? 'palette' : 'default',
  };
}

function styleForCell(cell) {
  const modes = colorModeForCell(cell);
  return {
    bold: Boolean(cell.isBold()),
    italic: Boolean(cell.isItalic()),
    underline: Boolean(cell.isUnderline()),
    blink: Boolean(cell.isBlink()),
    inverse: Boolean(cell.isInverse()),
    fgMode: modes.fgMode,
    fgColor: cell.getFgColor(),
    bgMode: modes.bgMode,
    bgColor: cell.getBgColor(),
  };
}

function isDefaultStyle(style) {
  return !style.bold
    && !style.italic
    && !style.underline
    && !style.blink
    && !style.inverse
    && style.fgMode === 'default'
    && style.bgMode === 'default';
}

function appendAnsiColorParams(params, mode, color, foreground) {
  if (mode === 'default') {
    return;
  }
  if (mode === 'palette') {
    if (color < 8) {
      params.push((foreground ? 30 : 40) + color);
      return;
    }
    if (color < 16) {
      params.push((foreground ? 90 : 100) + (color - 8));
      return;
    }
    params.push(foreground ? 38 : 48, 5, color);
    return;
  }
  params.push(
    foreground ? 38 : 48,
    2,
    (color >> 16) & 0xff,
    (color >> 8) & 0xff,
    color & 0xff,
  );
}

function styleToAnsi(style) {
  const params = [0];
  if (style.bold) params.push(1);
  if (style.italic) params.push(3);
  if (style.underline) params.push(4);
  if (style.blink) params.push(5);
  if (style.inverse) params.push(7);
  appendAnsiColorParams(params, style.fgMode, style.fgColor, true);
  appendAnsiColorParams(params, style.bgMode, style.bgColor, false);
  return `\x1b[${params.join(';')}m`;
}

function cursorMoveAnsi(row, col) {
  return `\x1b[${row};${col}H`;
}

export function snapshotVisibleTerminalFrame(terminal) {
  const buffer = terminal?.buffer.active;
  if (!terminal || !buffer || terminal.rows <= 0 || terminal.cols <= 0) {
    return '';
  }

  const viewportY = buffer.viewportY;
  const reusableCell = buffer.getNullCell?.();
  let out = '\x1b[?25l\x1b[?7l\x1b[0m\x1b[H\x1b[2J';

  for (let row = 0; row < terminal.rows; row += 1) {
    const line = buffer.getLine(viewportY + row);
    out += cursorMoveAnsi(row + 1, 1);
    if (!line) {
      out += '\x1b[0m\x1b[K';
      continue;
    }

    let lastPaintCol = -1;
    for (let col = 0; col < terminal.cols; col += 1) {
      const cell = line.getCell(col, reusableCell);
      if (!cell) {
        continue;
      }
      const chars = cell.getChars() || ' ';
      const width = cell.getWidth();
      const style = styleForCell(cell);
      if (chars !== ' ' || !isDefaultStyle(style)) {
        lastPaintCol = col + Math.max(width, 1) - 1;
      }
    }

    let currentStyle = null;
    for (let col = 0; col <= lastPaintCol; col += 1) {
      const cell = line.getCell(col, reusableCell);
      if (!cell || cell.getWidth() === 0) {
        continue;
      }
      const nextStyle = styleForCell(cell);
      if (!currentStyle || JSON.stringify(nextStyle) !== JSON.stringify(currentStyle)) {
        out += styleToAnsi(nextStyle);
        currentStyle = nextStyle;
      }
      out += cell.getChars() || ' ';
    }

    out += lastPaintCol < terminal.cols - 1 ? '\x1b[0m\x1b[K' : '\x1b[0m';
  }

  out += '\x1b[0m\x1b[?7h';
  return out;
}
