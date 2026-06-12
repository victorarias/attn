import {
  getTerminalTheme,
  type ResolvedTheme,
} from './terminalSizing';

const OSC_TERMINATOR_RE = '(?:\\u0007|\\u001b\\\\)';

function oscQueryRe(code: number): RegExp {
  return new RegExp(`\\u001b\\]${code};\\?${OSC_TERMINATOR_RE}`);
}

function hasOscResponse(code: number, responses: readonly string[]): boolean {
  const prefix = `\u001b]${code};`;
  return responses.some((response) => response.startsWith(prefix));
}

function hexToTerminalRgb(color: string): string {
  const value = color.startsWith('#') ? color.slice(1) : color;
  if (value.length !== 6) return '0000/0000/0000';
  return `${value.slice(0, 2).repeat(2)}/${value.slice(2, 4).repeat(2)}/${value.slice(4, 6).repeat(2)}`;
}

function textFromTerminalWrite(data: string | Uint8Array): string {
  return typeof data === 'string' ? data : new TextDecoder().decode(data);
}

const CURSOR_POSITION_REPORT_RE = /\x1b\[\d+;\d+R/g;
const DEVICE_ATTRIBUTES_RESPONSE_RE = /\x1b\[\?[0-9;]*c/g;

// The daemon is the single authority for CPR (cursor position) and DA1 (device
// attributes) replies — it owns terminal geometry/capabilities (AGENTS.md
// pattern #7) and answers both directly from its read loop. This avoids a
// reattach race where fish's resize-triggered CPR+DA1 go unanswered while the
// frontend is mid-remount/replay, stalling the prompt for fish's ~10 s query
// timeout. The frontend must NOT also answer, or the shell reads the duplicate
// ESC[r;cR / ESC[?...c as stray input. Strip any CPR or DA1 the local terminal
// model emitted before forwarding responses to the PTY. (OSC color queries stay
// frontend-owned — they depend on the active theme — and are not stripped.)
export function stripDaemonOwnedResponses(response: string): string {
  return response
    .replace(CURSOR_POSITION_REPORT_RE, '')
    .replace(DEVICE_ATTRIBUTES_RESPONSE_RE, '');
}

export function buildTerminalQueryResponses(
  data: string | Uint8Array,
  resolvedTheme: ResolvedTheme,
  existingResponses: readonly string[] = [],
): string[] {
  const text = textFromTerminalWrite(data);
  if (!text) return [];

  const theme = getTerminalTheme(resolvedTheme);
  const responses: string[] = [];

  if (oscQueryRe(10).test(text) && !hasOscResponse(10, existingResponses)) {
    responses.push(`\u001b]10;rgb:${hexToTerminalRgb(theme.foreground)}\u001b\\`);
  }

  if (oscQueryRe(11).test(text) && !hasOscResponse(11, existingResponses)) {
    responses.push(`\u001b]11;rgb:${hexToTerminalRgb(theme.background)}\u001b\\`);
  }

  if (oscQueryRe(12).test(text) && !hasOscResponse(12, existingResponses)) {
    responses.push(`\u001b]12;rgb:${hexToTerminalRgb(theme.cursor)}\u001b\\`);
  }

  // DA1 (primary device attributes) is intentionally NOT answered here: the
  // daemon owns it (see stripDaemonOwnedResponses), so the frontend neither
  // generates nor forwards a DA1 reply.

  return responses;
}
