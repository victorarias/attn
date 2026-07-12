const CURSOR_POSITION_REPORT_RE = /\x1b\[\d+;\d+R/g;
const DEVICE_ATTRIBUTES_RESPONSE_RE = /\x1b\[\?[0-9;]*c/g;
const OSC_COLOR_RESPONSE_RE = /\x1b\]1[012];[^\x07\x1b]*(?:\x07|\x1b\\)/g;

// The daemon-side worker is the single authority for CPR (cursor position),
// DA1 (device attributes), and OSC 10/11/12 (foreground/background/cursor
// color) replies. It owns terminal geometry/capabilities (AGENTS.md pattern
// #7) and answers all three directly from its read loop, using the theme the
// app pushes down via set_terminal_theme. This avoids a reattach race where
// fish's resize-triggered CPR+DA1 go unanswered while the frontend is
// mid-remount/replay, stalling the prompt for fish's ~10 s query timeout.
// The frontend answers none of these queries itself — it only pushes theme
// changes — and must strip any CPR, DA1, or OSC color response the local
// terminal model emits before forwarding responses to the PTY, or the shell
// reads the duplicate reply as stray input.
export function stripDaemonOwnedResponses(response: string): string {
  return response
    .replace(CURSOR_POSITION_REPORT_RE, '')
    .replace(DEVICE_ATTRIBUTES_RESPONSE_RE, '')
    .replace(OSC_COLOR_RESPONSE_RE, '');
}
