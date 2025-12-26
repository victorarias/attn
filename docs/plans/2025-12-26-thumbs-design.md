# Thumbs: Quick Pattern Selection for Terminal

## Overview

A tmux-thumbs inspired feature for quickly selecting and acting on patterns (URLs, file paths, IP:port) from terminal output without using the mouse.

**Trigger**: `Cmd+F` on active terminal session

**Core flow**:
1. User presses `Cmd+F`
2. Frontend extracts last 1000 lines from xterm.js buffer
3. Text sent to Rust via Tauri command for pattern extraction
4. Rust returns deduplicated list of `{type, value, hint}` matches
5. Modal opens showing patterns with letter hints
6. User types hint to copy, or `Shift+hint` to open/launch

## Pattern Types

| Type | Examples | Regex |
|------|----------|-------|
| URL | `https://github.com/...`, `localhost:3000/api` | `https?://[^\s<>"')\]]+` or `localhost:\d+[^\s]*` |
| Absolute path | `/Users/victor/projects/attn` | `/[\w./-]+` |
| Relative path | `./src/main.rs`, `../config.json` | `\.\.?/[\w./-]+` |
| IP:port | `192.168.1.1:8080`, `127.0.0.1:3000` | `\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+` |

## Keyboard Interaction

| Key | Action |
|-----|--------|
| `a-z` | Copy pattern with matching hint to clipboard |
| `Shift+a-z` | Open/launch pattern (browser for URLs, Finder for paths) |
| `/` | Enter filter mode (search within patterns) |
| `Escape` | Exit filter mode if filtering, otherwise close modal |
| `Escape` (not filtering) | Close modal |
| Click outside | Close modal |

For two-letter hints (`aa`, `ab`, ...): buffer first keystroke, wait 300ms or until second key.

## Modal UI

```
┌──────────────────────────────────────────────┐
│  Quick Find                            [×]   │
├──────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────┐ │
│  │ / Filter...                             │ │  ← visible only in filter mode
│  └─────────────────────────────────────────┘ │
│                                              │
│  a   🔗  https://github.com/foo/bar/pull/12  │
│  b   📁  /Users/victor/projects/attn/cmd/... │
│  c   🔗  localhost:3000                      │
│  d   📁  ./internal/daemon/websocket.go      │
│  e   🌐  192.168.1.1:8080                    │
│                                              │
│  ─────────────────────────────────────────── │
│  type hint to copy · ⇧+hint to open · / search│
└──────────────────────────────────────────────┘
```

**Styling**:
- Dark theme matching attn aesthetic
- Hints in monospace, slightly dimmed
- Pattern values use CSS `text-overflow: ellipsis` (no hard character limit)
- Full value shown on hover via `title` attribute
- Subtle fade-in animation (150ms)

**Empty state**: "No URLs, paths, or addresses found"

## Architecture

### Rust Backend (Tauri Command)

New file: `src-tauri/src/thumbs.rs`

```rust
#[derive(Serialize)]
pub struct PatternMatch {
    pub pattern_type: String,  // "url" | "path" | "ip_port"
    pub value: String,         // the matched text
    pub hint: String,          // "a", "b", ... "aa", "ab", ...
}

#[tauri::command]
pub fn extract_patterns(text: String) -> Vec<PatternMatch> {
    // 1. Run regex patterns against text
    // 2. Deduplicate by value
    // 3. Sort by type priority (urls first, then paths, then ip:port)
    // 4. Assign hints (a-z, then aa-az, ba-bz...)
    // 5. Return matches
}
```

**Hint generation**: First 26 patterns get `a-z`. Additional patterns get `aa`, `ab`, etc. Most terminal sessions won't exceed 26 unique patterns.

### Frontend Integration

**Extract text from xterm buffer**:
```typescript
function getTerminalText(terminal: XTerm, lines: number = 1000): string {
  const buffer = terminal.buffer.active;
  const startLine = Math.max(0, buffer.length - lines);
  const textLines: string[] = [];

  for (let i = startLine; i < buffer.length; i++) {
    const line = buffer.getLine(i);
    if (line) textLines.push(line.translateToString(true));
  }

  return textLines.join('\n');
}
```

**Trigger handler**:
```typescript
async function handleQuickFind() {
  const text = getTerminalText(terminalRef.current, 1000);
  const patterns = await invoke<PatternMatch[]>('extract_patterns', { text });
  setThumbsPatterns(patterns);
  setThumbsOpen(true);
}
```

**Actions**:
- Copy: `navigator.clipboard.writeText(value)` or Tauri clipboard API
- Open URL/IP: `openUrl()` from `@tauri-apps/plugin-opener` (already in use)
- Open path: Tauri shell command to `open` (macOS)

### New Component

`app/src/components/ThumbsModal.tsx`

```typescript
interface ThumbsModalProps {
  isOpen: boolean;
  patterns: PatternMatch[];
  onClose: () => void;
  onSelect: (value: string, action: 'copy' | 'open') => void;
}
```

## Edge Cases

| Case | Behavior |
|------|----------|
| No patterns found | Show empty state message |
| Modal already open + `Cmd+F` | Refresh pattern list (re-scan buffer) |
| No active terminal | `Cmd+F` does nothing |
| Path doesn't exist | Open fails gracefully (no error modal) |
| Very long pattern value | CSS truncation with ellipsis, full value on hover |

## Feedback

- Toast: "Copied to clipboard"
- Toast: "Opened in browser"
- Toast: "Opening file..."

## Out of Scope (v1)

- Overlay mode (hints directly on terminal)
- Custom pattern configuration
- Git SHAs, UUIDs, beads IDs
- Full-text search within scrollback
- Pattern history/recents

## Implementation Tasks

1. Create `src-tauri/src/thumbs.rs` with `extract_patterns` command
2. Register command in `src-tauri/src/lib.rs`
3. Create `ThumbsModal.tsx` component
4. Add `Cmd+F` keyboard handler to terminal container
5. Wire up copy/open actions
6. Add toast feedback
7. Style modal to match attn theme
