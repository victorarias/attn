import {
  getTerminalTheme,
  type ResolvedTheme,
} from './terminalSizing';

const OSC_TERMINATOR_RE = '(?:\\u0007|\\u001b\\\\)';
const PRIMARY_DEVICE_ATTRIBUTES_QUERY_RE = /\u001b\[(?:0)?c/;
const PRIMARY_DEVICE_ATTRIBUTES_RESPONSE_RE = /\u001b\[\?[0-9;]*c/;

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

  if (
    PRIMARY_DEVICE_ATTRIBUTES_QUERY_RE.test(text)
    && !existingResponses.some((response) => PRIMARY_DEVICE_ATTRIBUTES_RESPONSE_RE.test(response))
  ) {
    responses.push('\u001b[?1;2c');
  }

  return responses;
}
