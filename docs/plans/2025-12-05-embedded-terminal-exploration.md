# Embedded Terminal Exploration

**Date:** 2025-12-05
**Status:** Exploration complete, implementation deferred

## Goal

Embed Claude Code sessions directly inside the dashboard instead of using tmux, providing:
1. Reduced switching overhead (see Claude without leaving dashboard)
2. Eliminate tmux dependency
3. Unified experience (one app that IS the terminal)

## What We Tried

### Approach: Direct PTY + VT100 Emulation

**Libraries used:**
- `github.com/creack/pty` - PTY allocation
- `github.com/hinshun/vt10x` - VT100 terminal emulator
- `github.com/taigrr/bubbleterm` - Bubbletea terminal component (version conflicts prevented use)

**Implementation:**
1. Created `internal/terminal/terminal.go` - PTY wrapper with vt10x
2. Created `internal/terminal/model.go` - Bubbletea model for popup
3. Added 'n' key to dashboard to spawn new Claude session in popup
4. Attempted to render terminal with ANSI colors preserved

**Problems encountered:**

1. **Version conflicts**: bubbleterm requires bubbletea v2 (beta), our code uses v1. The v2 beta has dependency conflicts with charm's x/* packages.

2. **Color stripping**: lipgloss's `Render()` function strips ANSI escape codes. Had to bypass lipgloss entirely and render borders manually.

3. **No scrollback**: vt10x only maintains the visible screen, not history. Real scrollback requires either:
   - Capturing lines as they scroll off (complex state management)
   - Keeping raw PTY output and replaying through fresh vt10x instance
   - Neither approach is straightforward

4. **Complex terminal features**: Claude Code uses advanced features (cursor movement, line clearing, spinners, 256-color) that require full xterm emulation - not just basic VT100.

5. **Each character on new line**: Without proper VT100 parsing, cursor movement escape sequences were displayed literally, causing each keystroke to appear on a new line.

## Learnings

1. **Terminal emulation is hard** - It's years of work to properly emulate xterm. Libraries like vt10x handle basics but not the full feature set modern CLI apps expect.

2. **tmux already solved this** - tmux handles terminal emulation, scrollback, colors, resizing, etc. Fighting it is fighting decades of development.

3. **bubbleterm acknowledges this** - Their own docs say "Running tmux inside the emulator fixes these issues, as tmux handles its own damage tracking."

4. **lipgloss and ANSI don't mix** - Can't pass raw ANSI content through lipgloss styling functions; they strip escape codes.

## Viable Paths Forward

### Option A: tmux capture-pane Approach (Recommended)

**Concept:** Don't emulate a terminal. Use tmux's `capture-pane` to get rendered content and display it in the dashboard.

**How it works:**
```bash
# Capture pane content with ANSI colors
tmux capture-pane -p -e -t <target>

# Capture with scrollback (last 1000 lines)
tmux capture-pane -p -e -S -1000 -t <target>

# Send input
tmux send-keys -t <target> "text here"
```

**Implementation:**
1. Add a "preview pane" to dashboard showing selected session's tmux content
2. Poll `capture-pane` on tick (every 100-200ms)
3. Render captured content directly (already has ANSI codes)
4. Handle scroll with Shift+Up/Down by adjusting `-S` parameter
5. Optionally send keys via `send-keys` for basic interaction

**Pros:**
- tmux handles all terminal emulation
- Scrollback is built-in
- Colors work automatically
- Battle-tested, production-ready

**Cons:**
- Still requires tmux
- Slight latency from polling
- Full interaction still needs popup or pane switch

**Reference:** [tmuxwatch](https://github.com/steipete/tmuxwatch) uses this approach

### Option B: Enhanced tmux Popup (Current Approach)

**Concept:** Don't try to embed. Keep dashboard as a session manager, use tmux popup for full interaction.

**Current behavior:**
- Press Enter on session → `tmux display-popup` opens with session
- Full terminal experience inside popup
- Close popup → back to dashboard

**Enhancements possible:**
- Faster popup opening
- Remember popup size preferences
- Quick-switch between sessions without closing popup
- Preview pane using capture-pane (hybrid with Option A)

**Pros:**
- Already working
- Full terminal fidelity
- No new complexity

**Cons:**
- Context switch to popup
- Can't see dashboard while interacting

### Option C: Hybrid Approach

Combine A and B:
- Dashboard shows capture-pane preview of selected session
- Press Enter for full popup interaction
- Best of both: visibility + full control when needed

## Recommendation

Start with **Option A** (capture-pane preview) as an enhancement to current dashboard. This gives visibility into sessions without the complexity of terminal emulation. Keep popup for full interaction.

If Option A works well, evaluate whether full embedding (eliminating popup) is still needed.

## Files Created (To Be Removed)

- `internal/terminal/terminal.go`
- `internal/terminal/model.go`

## References

- [tmuxwatch](https://github.com/steipete/tmuxwatch) - TUI using capture-pane
- [bubbleterm](https://pkg.go.dev/github.com/taigrr/bubbleterm) - Go terminal emulator
- [vt10x](https://github.com/hinshun/vt10x) - VT10x parser
- [creack/pty](https://github.com/creack/pty) - PTY allocation
