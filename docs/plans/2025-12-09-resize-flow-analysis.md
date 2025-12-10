# Resize Flow Analysis: VS Code vs Our Implementation

## VS Code Resize Flow (Complete)

### Entry Point
VS Code does NOT use `window.addEventListener('resize')`. Instead, their workbench layout system calls `layout(dimension)` when the panel needs to resize.

### Step 1: layout(dimension) [terminalInstance.ts:1811]
```typescript
layout(dimension: dom.Dimension): void {
  if (dimension.width <= 0 || dimension.height <= 0) return;
  const terminalWidth = this._evaluateColsAndRows(dimension.width, dimension.height);
  if (!terminalWidth) return;
  this._resize();
}
```

### Step 2: _evaluateColsAndRows(width, height) [terminalInstance.ts:678]
```typescript
private _evaluateColsAndRows(width: number, height: number): number | null {
  if (!width || !height) return null;

  // CRITICAL: Gets dimension with padding subtracted
  const dimension = this._getDimension(width, height);
  if (!dimension) return null;

  // Get font metrics (charWidth, charHeight from xterm OR manual measurement)
  const font = this.xterm ? this.xterm.getFont() : this._terminalConfigurationService.getFont(...);

  // Calculate cols/rows using the scaled dimension formula
  const newRC = getXtermScaledDimensions(window, font, dimension.width, dimension.height);
  if (!newRC) return null;

  this._cols = newRC.cols;
  this._rows = newRC.rows;
  return dimension.width;
}
```

### Step 3: _getDimension(width, height) [terminalInstance.ts:719]
**CRITICAL: This subtracts padding from xterm element!**
```typescript
private _getDimension(width: number, height: number): ICanvasDimensions | undefined {
  const font = this.xterm.getFont();
  if (!font?.charWidth || !font?.charHeight) return undefined;
  if (!this.xterm?.raw.element) return undefined;

  // Get padding from XTERM ELEMENT (not container!)
  const computedStyle = getComputedStyle(this.xterm.raw.element);
  const horizontalPadding = parseInt(computedStyle.paddingLeft)
                          + parseInt(computedStyle.paddingRight)
                          + 14; // scrollbar padding
  const verticalPadding = parseInt(computedStyle.paddingTop)
                        + parseInt(computedStyle.paddingBottom);

  return {
    width: Math.min(Constants.MaxCanvasWidth, width - horizontalPadding),
    height: height - verticalPadding + (hasHorizontalScrollbar ? -5 : 0)
  };
}
```

### Step 4: getXtermScaledDimensions [xtermTerminal.ts:850]
```typescript
function getXtermScaledDimensions(w, font, width, height) {
  if (!font.charWidth || !font.charHeight) return null;

  const dpr = w.devicePixelRatio;

  // Width calculation
  const scaledWidthAvailable = width * dpr;
  const scaledCharWidth = font.charWidth * dpr + font.letterSpacing;
  const cols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);

  // Height calculation
  const scaledHeightAvailable = height * dpr;
  const scaledCharHeight = Math.ceil(font.charHeight * dpr);  // NOTE: Math.ceil
  const scaledLineHeight = Math.floor(scaledCharHeight * font.lineHeight);
  const rows = Math.max(Math.floor(scaledHeightAvailable / scaledLineHeight), 1);

  return { cols, rows };
}
```

### Step 5: xterm.getFont() → font metrics [terminalConfigurationService.ts:108]
```typescript
getFont(w, xtermCore, excludeDimensions) {
  // ... config setup ...

  // Get cell dimensions from xterm's render service
  if (xtermCore?._renderService?._renderer.value) {
    const cellDims = xtermCore._renderService.dimensions.css.cell;
    if (cellDims?.width && cellDims?.height) {
      return {
        fontFamily,
        fontSize,
        letterSpacing,
        lineHeight,
        // CRITICAL: Adjust for letterSpacing and lineHeight
        charHeight: cellDims.height / lineHeight,
        charWidth: cellDims.width - Math.round(letterSpacing) / w.devicePixelRatio
      };
    }
  }

  // Fallback: measure font manually with DOM element
  return this._measureFont(...);
}
```

### Step 6: _resize() [terminalInstance.ts:1850]
```typescript
private async _resize(immediate?: boolean): Promise<void> {
  let cols = this.cols;
  let rows = this.rows;

  // Update font settings if changed
  if (this._isVisible && this._layoutSettingsChanged) {
    this.xterm.raw.options.letterSpacing = font.letterSpacing;
    this.xterm.raw.options.lineHeight = font.lineHeight;
    // ... etc
    this._initDimensions();
    cols = this.cols;
    rows = this.rows;
  }

  if (isNaN(cols) || isNaN(rows)) return;

  // Call debouncer
  this._resizeDebouncer.resize(cols, rows, immediate ?? false);
}
```

### Step 7: TerminalResizeDebouncer.resize() [terminalResizeDebouncer.ts:35]
```typescript
async resize(cols, rows, immediate) {
  this._latestX = cols;
  this._latestY = rows;

  // IMMEDIATE if requested OR small buffer
  if (immediate || buffer.normal.length < 200) {
    this._resizeBothCallback(cols, rows);  // xterm.resize + PTY update
    return;
  }

  // DEFERRED if not visible
  if (!this._isVisible()) {
    // Schedule idle callbacks for X and Y
    return;
  }

  // VISIBLE + LARGE BUFFER:
  // Y is immediate
  this._resizeYCallback(rows);  // xterm.resize(currentCols, newRows) + PTY update

  // X is debounced 100ms
  this._latestX = cols;
  this._debounceResizeX(cols);  // xterm.resize(newCols, currentRows) + PTY update
}
```

### Step 8: The actual resize callbacks [terminalInstance.ts:778]
```typescript
// Both axes
async (cols, rows) => {
  xterm.raw.resize(cols, rows);
  await this._updatePtyDimensions(xterm.raw);
}

// X only (debounced)
async (cols) => {
  xterm.raw.resize(cols, xterm.raw.rows);  // Keep current rows
  await this._updatePtyDimensions(xterm.raw);
}

// Y only (immediate)
async (rows) => {
  xterm.raw.resize(xterm.raw.cols, rows);  // Keep current cols
  await this._updatePtyDimensions(xterm.raw);
}
```

---

## Our Resize Flow

### Entry Point
We use `window.addEventListener('resize', handleResize)`.

### handleResize() [Terminal.tsx:224]
```typescript
const handleResize = () => {
  const dims = getScaledDimensions(container, term);
  if (!dims) return;

  const { cols, rows } = dims;
  const colsChanged = cols !== lastCols;
  const rowsChanged = rows !== lastRows;

  if (!colsChanged && !rowsChanged) return;

  // Small buffer: immediate
  if (term.buffer.normal.length < 200) {
    lastCols = cols;
    lastRows = rows;
    resizeTerminal(term, cols, rows);
    return;
  }

  // Y only: immediate
  if (rowsChanged && !colsChanged) {
    lastRows = rows;
    resizeTerminal(term, lastCols, rows);
    return;
  }

  // X changed: debounce X, do Y immediately if both changed
  clearTimeout(xResizeTimeout);
  if (rowsChanged) {
    lastRows = rows;
    term.resize(lastCols, rows);
    onResizeRef.current?.(lastCols, rows);
  }
  xResizeTimeout = setTimeout(() => {
    lastCols = cols;
    resizeTerminal(term, cols, lastRows);
  }, 100);
};
```

### getScaledDimensions() [Terminal.tsx:24]
```typescript
function getScaledDimensions(container, term, letterSpacing = 0, lineHeight = 1) {
  // Get dimensions from CONTAINER (not xterm element!)
  const style = getComputedStyle(container);
  const width = parseFloat(style.width);
  const height = parseFloat(style.height);

  if (width <= 0 || height <= 0) return null;

  const core = term._core;
  const renderer = core?._renderService?._renderer?.value;
  if (!renderer) return null;

  const cellDims = core._renderService.dimensions?.css?.cell;
  if (!cellDims?.width || !cellDims?.height) return null;

  // Extract char dimensions
  const dpr = window.devicePixelRatio;
  const charWidth = cellDims.width - Math.round(letterSpacing) / dpr;
  const charHeight = cellDims.height / lineHeight;

  // Calculate with VS Code formula
  const scaledWidthAvailable = width * dpr;
  const scaledCharWidth = charWidth * dpr + letterSpacing;
  const cols = Math.max(Math.floor(scaledWidthAvailable / scaledCharWidth), 1);

  const scaledHeightAvailable = height * dpr;
  const scaledCharHeight = Math.ceil(charHeight * dpr);
  const scaledLineHeight = Math.floor(scaledCharHeight * lineHeight);
  const rows = Math.max(Math.floor(scaledHeightAvailable / scaledLineHeight), 1);

  return { cols, rows };
}
```

---

## KEY DIFFERENCES

### 1. Container vs Element Dimensions
| Aspect | VS Code | Ours |
|--------|---------|------|
| Dimension source | xterm.raw.element | container |
| Padding subtraction | YES (paddingLeft + paddingRight + 14, paddingTop + paddingBottom) | NO |
| Scrollbar compensation | YES (+14px horizontal, -5px if horizontal scrollbar) | NO |

**POTENTIAL ISSUE**: We don't subtract padding or scrollbar space!

### 2. Resize Trigger
| Aspect | VS Code | Ours |
|--------|---------|------|
| Trigger | Workbench layout system calls layout() | window.addEventListener('resize') |
| Timing | Controlled by parent layout | Browser resize event |

### 3. Font Metrics
| Aspect | VS Code | Ours |
|--------|---------|------|
| Fallback | Manual DOM measurement if xterm not ready | Returns null |
| Source | xterm._renderService.dimensions.css.cell | Same |

### 4. Dimension Adjustment
| Aspect | VS Code | Ours |
|--------|---------|------|
| Padding subtraction | horizontalPadding, verticalPadding | None |
| MaxCanvasWidth limit | YES (Constants.MaxCanvasWidth) | NO |

---

## FIXES TO IMPLEMENT

1. **Subtract padding from xterm element (not container)**
2. **Add scrollbar compensation (+14px horizontal)**
3. **Consider MaxCanvasWidth limit**
4. **Consider using xterm.raw.element for dimension source**
