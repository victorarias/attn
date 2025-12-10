# xterm.js Fixes to Match VS Code

**Date**: 2025-12-09
**Status**: Complete (Round 4 - Architectural Root Cause Found)
**File**: `app/src/components/Terminal.tsx`, `app/src/components/Terminal.css`

---

## HOW TO RE-RUN ANALYSIS

To verify our implementation matches VS Code or find additional differences:

### Step 1: Clone VS Code Reference
```bash
cd /Users/victor.arias/projects/claude-manager
git clone --depth 1 --branch 1.96.2 https://github.com/microsoft/vscode.git vscode-reference
```

### Step 2: Launch Subagent Analysis
Use 5 parallel subagents, each focusing on one area:

1. **Scrollbar & Padding Agent**: Compare scrollbar width handling, padding subtraction
2. **Dimension Calculation Agent**: Compare cols/rows formulas, font measurement
3. **Terminal Options Agent**: Compare XTerm constructor options
4. **Resize Flow Agent**: Compare debouncing strategy, visibility handling
5. **Initialization Agent**: Compare initialization sequence and timing

**IMPORTANT**: Also analyze CSS files:
- `vscode/src/vs/workbench/contrib/terminal/browser/media/terminal.css`
- `vscode/src/vs/workbench/contrib/terminal/browser/media/xterm.css`

### Step 3: Consensus Check
Have agents report differences. If there's disagreement, verify against VS Code source directly.

### Step 4: Cleanup
```bash
rm -rf /Users/victor.arias/projects/claude-manager/vscode-reference
```

---

## COMPLETED FIXES (Round 4 - Architectural Root Cause)

### 16. ResizeObserver for Container Resize (CRITICAL)
- **Root Cause**: We only listened for `window.addEventListener('resize')`, but container can resize from:
  - Sidebar collapse/expand
  - Display changes (active/inactive terminal)
  - Parent layout changes
- **Issue**: ResizeObserver was only used for initial "ready" detection, then disconnected
- **VS Code**: Uses top-down layout system where parent calls `layout(dimension)` on children
- **Our Equivalent**: Keep ResizeObserver running continuously on container
- **Fix**:
  - Unified resize detection through continuous ResizeObserver
  - Removed redundant window resize listener
  - ResizeObserver handles both initial ready + ongoing resize
- **Source**: Systematic debugging Phase 1-4 analysis

---

## COMPLETED FIXES (Round 1)

### 1. Font Measurement Fallback
- Added `measureFont()` function for DOM-based font measurement
- Used as fallback when xterm renderer not ready
- **Result**: Fixed line break explosion issue

### 2. DPI Change Detection
- Added media query listener for `(resolution: Xdppx)`
- Triggers dimension recalculation when DPR changes

### 3. Visibility Detection with requestIdleCallback
- Added IntersectionObserver to track terminal visibility
- Hidden terminals defer resizes using `requestIdleCallback`

### 4. WebGL Loading Order
- **Verified**: VS Code loads WebGL AFTER `open()` (not before)
- Our implementation matches VS Code

---

## COMPLETED FIXES (Round 2 - JS Consensus Analysis)

### 5. MAX_CANVAS_WIDTH Corrected
- **Issue**: We had 8192, VS Code uses 4096
- **Fix**: Changed to `const MAX_CANVAS_WIDTH = 4096`
- **Source**: terminalInstance.ts line 103

### 6. Scrollbar Width Fixed
- **Issue**: Our conditional logic was wrong - we checked `scrollback === 0` and `overviewRuler.width`
- **VS Code Reality**: ALWAYS uses hardcoded 14px (line 730 of terminalInstance.ts)
- **Fix**: Removed conditional logic, always use 14px

### 7. Visibility Flush Pattern Added
- **Issue**: VS Code has explicit flush when terminal becomes visible
- **Source**: terminalInstance.ts `setVisible()` calls `_resizeDebouncer.flush()`
- **Fix**: Added flush logic to IntersectionObserver callback when `wasHidden && nowVisible`

### 8. Initial cols/rows in Constructor
- **Issue**: We didn't set initial dimensions, xterm defaulted to 80x24
- **VS Code**: Passes explicit cols/rows to constructor
- **Fix**: Pre-calculate dimensions using DOM font measurement before creating terminal

---

## COMPLETED FIXES (Round 3 - CSS Analysis)

**Previous CSS** was only 10 lines (width/height 100%). VS Code has 4 CSS files totaling 500+ lines.

### 9. Container Overflow Hidden
- **Issue**: Our container allowed content to overflow
- **VS Code**: Uses `overflow: hidden` on container
- **Fix**: Added `overflow: hidden` to `.terminal-container`

### 10. Container Positioning
- **Issue**: Missing relative positioning for absolute children
- **VS Code**: Uses `position: relative` on wrapper elements
- **Fix**: Added `position: relative` and `box-sizing: border-box`

### 11. xterm Element Positioning
- **Issue**: `.xterm` element wasn't explicitly positioned
- **VS Code**: Uses `position: absolute; top: 0; bottom: 0; left: 0; right: 0;`
- **Fix**: Added absolute positioning to `.terminal-container .xterm`
- **Source**: terminal.css lines 88-95

### 12. Viewport Right Offset (CRITICAL for scrollbar)
- **Issue**: Viewport extended to edge, conflicting with scrollbar
- **VS Code**: Uses `right: 14px` on `.xterm-viewport`
- **Fix**: Added `right: 14px` to viewport CSS
- **Source**: terminal.css lines 163-167

### 13. Z-Index Layering
- **Issue**: Missing proper z-index stacking
- **VS Code**: Has specific z-index for viewport (30), screen (31), accessibility (32)
- **Fix**: Added proper z-index values

### 14. Scrollbar Visibility Styling
- **Issue**: Missing scrollbar visibility transitions
- **VS Code**: Has `.visible` and `.invisible.fade` classes with transitions
- **Fix**: Added scrollbar visibility CSS from xterm.css

### 15. test-terminal.tsx WebGL Order
- **Issue**: Had incorrect comment "VS Code loads WebGL BEFORE open()"
- **Fix**: Corrected to match main Terminal.tsx (WebGL AFTER open)

---

## SUMMARY TABLE

| Round | Fix | Category |
|-------|-----|----------|
| 1 | Font measurement fallback | JS |
| 1 | DPI change detection | JS |
| 1 | Visibility detection | JS |
| 1 | WebGL loading order | JS |
| 2 | MAX_CANVAS_WIDTH = 4096 | JS |
| 2 | Scrollbar always 14px | JS |
| 2 | Visibility flush pattern | JS |
| 2 | Initial cols/rows | JS |
| 3 | overflow: hidden | CSS |
| 3 | position: relative | CSS |
| 3 | xterm absolute positioning | CSS |
| 3 | viewport right: 14px | CSS |
| 3 | z-index layering | CSS |
| 3 | scrollbar visibility | CSS |
| **4** | **ResizeObserver for container resize** | **JS (ROOT CAUSE)** |

---

## NOT IMPLEMENTED (Low Priority)

### Horizontal Scrollbar Compensation
VS Code subtracts 5px when horizontal scrollbar is present, but horizontal scrollbars are rare in terminals.

### 20px Left Padding for Gutter
VS Code adds 20px left padding for command decorations gutter. We don't use this feature.

---

## REFERENCES

- VS Code terminal: `vscode/src/vs/workbench/contrib/terminal/browser/`
- Key JS files:
  - `terminalInstance.ts` - main terminal logic, dimension calculation
  - `xtermTerminal.ts` - xterm wrapper, getXtermScaledDimensions
  - `terminalConfigurationService.ts` - font measurement
  - `terminalResizeDebouncer.ts` - resize debouncing
- Key CSS files:
  - `browser/media/terminal.css` - terminal container/wrapper styles
  - `browser/media/xterm.css` - xterm element styles, scrollbar
