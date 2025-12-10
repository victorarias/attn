# VS Code xterm.js Integration Comparison

**Date**: 2025-12-09
**Purpose**: Document exact differences between VS Code's xterm.js integration and our implementation to achieve parity.

## VS Code Reference

- **Repository**: microsoft/vscode (tag 1.96.2)
- **xterm.js version**: `@xterm/xterm` 5.6.0-beta.70
- **Key files**:
  - `src/vs/workbench/contrib/terminal/browser/xterm/xtermTerminal.ts`
  - `src/vs/workbench/contrib/terminal/browser/terminalInstance.ts`
  - `src/vs/workbench/contrib/terminal/browser/terminalResizeDebouncer.ts`

---

## Critical Differences

### 1. FitAddon Usage

| Aspect | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| FitAddon | **NOT USED AT ALL** | Used for `fit()` and `proposeDimensions()` | ❌ DIFFERENT |

**VS Code approach**: Calculates dimensions manually using container CSS pixels, font metrics, and devicePixelRatio. Calls `raw.resize(cols, rows)` directly.

**Action Required**: Remove FitAddon dependency, calculate dimensions ourselves.

---

### 2. Dimension Calculation

| Aspect | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| Method | Manual calculation with devicePixelRatio | FitAddon.proposeDimensions() | ❌ DIFFERENT |
| devicePixelRatio | Always multiplied | Not used | ❌ DIFFERENT |

**VS Code formula** (from `getXtermScaledDimensions()`):
```typescript
const scaledWidthAvailable = dimension.width * window.devicePixelRatio;
const scaledCharWidth = font.charWidth * window.devicePixelRatio + font.letterSpacing;
cols = Math.floor(scaledWidthAvailable / scaledCharWidth);

const scaledHeightAvailable = dimension.height * window.devicePixelRatio;
const scaledCharHeight = font.charHeight * window.devicePixelRatio;
const scaledLineHeight = Math.floor(scaledCharHeight * font.lineHeight);
rows = Math.floor(scaledHeightAvailable / scaledLineHeight);
```

**Action Required**: Implement manual dimension calculation with devicePixelRatio.

---

### 3. Resize Sequence

| Aspect | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| Order | **xterm FIRST, PTY SECOND** | xterm first, PTY second | ✅ SAME |
| Method | `raw.resize(cols, rows)` | `term.resize(cols, rows)` | ✅ SAME |

**Action Required**: None - already correct.

---

### 4. Resize Debouncing

| Aspect | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| Y-axis (rows) | **Immediate** (no debounce) | 100ms debounce | ❌ DIFFERENT |
| X-axis (cols) | **100ms debounce** | 100ms debounce | ✅ SAME |
| Small buffers | Immediate if < 200 lines | Always debounced | ❌ DIFFERENT |

**VS Code rationale**: Row changes are cheap (no text reflow), column changes are expensive (reflow).

**Action Required**: Implement axis-independent debouncing.

---

### 5. Renderer Strategy

| Aspect | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| Primary | WebGL | WebGL | ✅ SAME |
| Fallback | **DOM** (no Canvas) | DOM (had Canvas before) | ✅ SAME |
| Context loss | Dispose, switch to DOM, refresh dimensions | Dispose, log warning | ⚠️ PARTIAL |

**VS Code context loss handler**:
```typescript
this._webglAddon.onContextLoss(() => {
  this._logService.info(`Webgl lost context, disposing of webgl renderer`);
  this._disposeOfWebglRenderer();
});

private _disposeOfWebglRenderer(): void {
  this._webglAddon?.dispose();
  this._webglAddon = undefined;
  // WebGL cell dimensions differ from DOM - must refresh
  this._onDidRequestRefreshDimensions.fire();
}
```

**Action Required**: After WebGL context loss, trigger dimension refresh.

---

### 6. Terminal Options

| Option | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| `allowProposedApi` | `true` | `true` | ✅ SAME |
| `windowOptions.getWinSizePixels` | `true` | `true` | ✅ SAME |
| `windowOptions.getCellSizePixels` | `true` | `true` | ✅ SAME |
| `windowOptions.getWinSizeChars` | `true` | `true` | ✅ SAME |
| `scrollOnEraseInDisplay` | Not set (default) | `true` | ⚠️ Extra |
| `cursorBlink` | From config | `true` | ✅ OK |
| `fontSize` | From config | `14` | ✅ OK |
| `scrollback` | From config | `10000` | ✅ OK |
| `convertEol` | Not set | `true` | ⚠️ Extra |
| `documentOverride` | `layoutService.mainContainer.ownerDocument` | Not set | ❓ |
| `fastScrollModifier` | `'alt'` | Not set | ❌ MISSING |
| `overviewRuler` | `{ width: 14, showTopBorder: true }` | Not set | ❌ MISSING |
| `drawBoldTextInBrightColors` | From config | Not set | ❌ MISSING |
| `minimumContrastRatio` | From config | Not set | ❌ MISSING |

**Action Required**: Add missing options, review extra options.

---

### 7. Addon Loading Order

**VS Code order**:
1. MarkNavigationAddon (custom, always)
2. DecorationAddon (custom, always)
3. ShellIntegrationAddon (custom, always)
4. ClipboardAddon (async, always)
5. WebglAddon (on attachToElement, conditional)
6. Others (on demand)

**Our order**:
1. FitAddon (always)
2. WebLinksAddon (always)
3. WebglAddon (after open)

**Key difference**: VS Code loads WebGL addon **BEFORE** calling `open()` to prevent DOM renderer initialization. Comment in code: "TODO: Move before open so the DOM renderer doesn't initialize"

**Action Required**: Load WebGL before `term.open()`.

---

### 8. PTY Spawn Timing

| Aspect | VS Code | Our Implementation | Status |
|--------|---------|-------------------|--------|
| xterm creation | First | First | ✅ SAME |
| PTY spawn | After xterm ready with dimensions | After onReady with dimensions | ✅ SAME |

**Action Required**: None - already correct.

---

## Implementation Plan

### Phase 1: Remove FitAddon, Manual Dimensions

1. Remove FitAddon import and usage
2. Implement `getXtermScaledDimensions()` helper:
   ```typescript
   function getXtermScaledDimensions(
     container: HTMLElement,
     term: XTerm
   ): { cols: number; rows: number } | null {
     const style = getComputedStyle(container);
     const width = parseFloat(style.width);
     const height = parseFloat(style.height);

     if (width <= 0 || height <= 0) return null;

     const dpr = window.devicePixelRatio;
     const core = (term as any)._core;
     const charWidth = core._renderService.dimensions.css.cell.width;
     const charHeight = core._renderService.dimensions.css.cell.height;

     // VS Code uses letterSpacing from config, we use 0
     const letterSpacing = 0;
     const lineHeight = 1; // VS Code uses config, we use 1

     const scaledWidthAvailable = width * dpr;
     const scaledCharWidth = charWidth * dpr + letterSpacing;
     const cols = Math.max(1, Math.floor(scaledWidthAvailable / scaledCharWidth));

     const scaledHeightAvailable = height * dpr;
     const scaledCharHeight = charHeight * dpr;
     const scaledLineHeight = Math.floor(scaledCharHeight * lineHeight);
     const rows = Math.max(1, Math.floor(scaledHeightAvailable / scaledLineHeight));

     return { cols, rows };
   }
   ```

3. Replace all `fitAddon.fit()` and `proposeDimensions()` calls

### Phase 2: Smart Debouncing

1. Implement axis-independent debouncing:
   - Y-axis: immediate
   - X-axis: 100ms debounce
   - Small buffers (< 200 lines): immediate for both

### Phase 3: Addon Loading Order

1. Move WebGL addon loading BEFORE `term.open()`
2. Handle case where WebGL fails before open

### Phase 4: Missing Options

1. Add `fastScrollModifier: 'alt'`
2. Add `overviewRuler: { width: 14, showTopBorder: true }`
3. Review and test other missing options

### Phase 5: Context Loss Handling

1. After WebGL dispose, trigger full dimension recalculation
2. Ensure DOM renderer gets correct cell dimensions

---

## Verification Checklist

- [ ] FitAddon completely removed
- [ ] Dimensions calculated with devicePixelRatio
- [ ] Y-axis resize is immediate
- [ ] X-axis resize is debounced 100ms
- [ ] Small buffer resize is immediate
- [ ] WebGL loaded before open()
- [ ] Context loss triggers dimension refresh
- [ ] All VS Code terminal options present
- [ ] No spinner flicker on TUI apps
- [ ] No line break explosion on resize
