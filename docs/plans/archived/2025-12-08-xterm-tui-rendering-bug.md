# xterm.js TUI Rendering Bug

**Date:** 2025-12-08
**Status:** RESOLVED

## Problem

When running Claude Code (Ink-based TUI) inside the Tauri app's embedded xterm.js terminal:

1. **Line breaks growing**: Extra blank lines appear between content where there should be none
2. **Flashing during animations**: The "thinking" spinner causes the entire non-background content to flash
3. **Resizing fixes it**: If the window is resized, the line breaks disappear - suggesting a dimension mismatch

## Root Cause Analysis

### Confirmed: Initial PTY/Terminal Size Mismatch

Console logs revealed:
```
[Session] Spawning PTY with dimensions: {cols: 80, rows: 24, termCols: 80, termRows: 24}
[Terminal] After fit: {cols: 278, rows: 55}
```

The PTY spawns with default 80x24 dimensions BEFORE `fitAddon.fit()` runs, then fit() calculates the correct size (278x55) afterward. By then, Claude Code has already started rendering with wrong dimensions.

### Timeline of Events
1. `term.open(container)` - terminal opens with default 80x24
2. `onReady(term)` fires immediately
3. `connectTerminal` spawns PTY with 80x24 (wrong!)
4. `requestAnimationFrame` → `fitAddon.fit()` → correct dimensions (278x55)
5. `resizeSession` called but Claude Code already started with wrong size

## What We Tried

### 1. Move onReady inside requestAnimationFrame (after fit)
**Result:** Broke the app - terminal wouldn't accept input

### 2. Call fit() synchronously before onReady
**Result:** App became unfocusable, window issues

### 3. Resize PTY after spawn if dimensions changed
**Code added to sessions.ts:**
```typescript
// After PTY spawns and is stored in state:
const currentCols = terminal.cols;
const currentRows = terminal.rows;
if (currentCols !== cols || currentRows !== rows) {
  pty.resize(currentCols, currentRows);
}
```
**Result:** Still same issue - the resize happens but Claude Code may have already cached initial dimensions

### 4. Removed padding from .xterm CSS
**Rationale:** Padding inside .xterm causes FitAddon to miscalculate
**Result:** Did not fix the issue

### 5. Increased resize debounce from 100ms to 200ms
**Rationale:** Give more time for resize to propagate
**Result:** Reverted, did not help

## Research Findings

### Relevant xterm.js Issues
- [Issue #1914](https://github.com/xtermjs/xterm.js/issues/1914) - Terminal resize roundtrip race condition
- [Issue #4841](https://github.com/xtermjs/xterm.js/issues/4841) - FitAddon resizes incorrectly
- [Issue #3873](https://github.com/xtermjs/xterm.js/issues/3873) - Nvim/tmux don't resize with fitaddon
- [Issue #510](https://github.com/xtermjs/xterm.js/issues/510) - Resizing while in alt buffer breaks normal buffer

### Key Insights
1. PTY resize happens async - data in buffers may use old size assumption
2. TUI apps (Ink/Claude Code) use alternate screen buffer
3. Resize causes SIGWINCH to be sent to the TUI app
4. The fact that manual resize fixes it confirms dimension mismatch

## Current State

The code currently has:
- Debug logging in Terminal.tsx and sessions.ts (should be removed before commit)
- Post-spawn resize check in sessions.ts (may or may not help)
- No padding in Terminal.css

## Next Steps to Try

1. **Use ResizeObserver** instead of window resize event to detect container size changes more reliably

2. **Delay PTY spawn** - Wait for a confirmed fit() before spawning:
   ```typescript
   // In Terminal.tsx, only call onReady after fit confirms valid dimensions
   requestAnimationFrame(() => {
     fitAddon.fit();
     if (term.cols > 80) { // Sanity check that fit worked
       onReady(term);
     }
   });
   ```

3. **Double-fit approach** - Fit, wait a frame, fit again, then spawn

4. **Check if terminal container has correct dimensions before fit** - The container might have 0 dimensions initially

5. **Investigate Ink/Claude Code's SIGWINCH handling** - Maybe the TUI doesn't respond to resize after initial render

## Files Modified

- `app/src/components/Terminal.tsx` - Terminal component with xterm.js
- `app/src/components/Terminal.css` - Removed padding from .xterm
- `app/src/store/sessions.ts` - PTY spawning with post-spawn resize check

## Related Issues

- React StrictMode was disabled in main.tsx (caused double PTY spawning)
- PTY spawn uses fish shell for PATH resolution: `spawn('/opt/homebrew/bin/fish', ['-l', '-c', 'cm -y'], ...)`

---

## SOLUTION (2025-12-09)

### Root Cause Confirmed

Two separate race conditions were causing the rendering issues:

1. **Initial spawn race**: `onReady` fired synchronously after `term.open()` but BEFORE `fitAddon.fit()` ran in `requestAnimationFrame`. PTY spawned with default 80x24 dimensions.

2. **Resize race**: On window resize, `fitAddon.fit()` changed xterm.js display immediately, but `pty.resize()` (SIGWINCH) was async. Claude Code was still outputting data for old dimensions while xterm.js displayed in new dimensions.

### Fix Applied

#### Fix 1: ResizeObserver for Initial Spawn

Use `ResizeObserver` to wait for container to have real dimensions before calling `onReady`:

```typescript
const observer = new ResizeObserver((entries) => {
  const entry = entries[0];
  if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
    fitAddon.fit();
    if (term.cols > 0 && term.rows > 0) {
      observer.disconnect();
      onReady(term);  // NOW spawn PTY with correct dimensions
    }
  }
});
observer.observe(containerRef.current);
```

#### Fix 2: Resize PTY Before Fit

On resize, tell PTY about new dimensions BEFORE changing xterm.js display:

```typescript
const handleResize = () => {
  const proposedDims = fitAddon.proposeDimensions();
  if (proposedDims) {
    // 1. Tell PTY first (sends SIGWINCH to Claude Code)
    onResize(proposedDims.cols, proposedDims.rows);
    // 2. Wait for Claude Code to process
    setTimeout(() => {
      // 3. Then update xterm.js display
      fitAddon.fit();
    }, 50);
  }
};
```

### Key Learnings

1. **xterm.js defaults to 80x24** until `fit()` is called
2. **`proposeDimensions()`** calculates dimensions without applying them - use this to resize PTY first
3. **PTY resize is async** - there's no acknowledgment mechanism (see xterm.js #1914)
4. **Ink caches terminal dimensions** at startup - must spawn with correct size
5. **Order matters**: PTY resize → wait → xterm.js display resize

### Prevention

When working with xterm.js + PTY + TUI apps:
- Never spawn PTY until terminal has correct dimensions
- Always resize PTY before resizing xterm.js display
- Use `proposeDimensions()` to get dimensions without applying them
