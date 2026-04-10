import { Terminal as XTerm } from '@xterm/xterm';
import { triggerShortcut } from '../../shortcuts/useShortcut';
import { isMacLikePlatform } from '../../shortcuts/platform';
import { recordPtyDecode } from '../../utils/ptyPerf';

export function decodePtyBytes(payload: string): Uint8Array {
  const startedAt = performance.now();
  const binaryStr = atob(payload);
  const bytes = Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
  recordPtyDecode(bytes.length, performance.now() - startedAt);
  return bytes;
}

export function encodePtyBytes(bytes: Uint8Array): string {
  let binary = '';
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    const chunk = bytes.subarray(offset, Math.min(offset + chunkSize, bytes.length));
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

export function snapshotTerminalText(terminal: XTerm | null): string {
  const buffer = terminal?.buffer.active;
  if (!buffer) {
    return '';
  }

  const lines: string[] = [];
  for (let i = 0; i < buffer.length; i++) {
    const line = buffer.getLine(i);
    if (line) {
      lines.push(line.translateToString(true));
    }
  }
  return lines.join('\n');
}

export function runtimeEnsureErrorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

export function isRetryableRuntimeEnsureError(error: unknown): boolean {
  const message = runtimeEnsureErrorMessage(error).toLowerCase();
  return (
    message.includes('pty backend is not configured') ||
    message.includes('websocket not connected') ||
    message.includes('attach session timed out') ||
    message.includes('spawn session timed out')
  );
}

export function waitForRuntimeEnsureRetry(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

export function installTerminalKeyHandler(sendToPty: (data: string) => void) {
  return (ev: KeyboardEvent) => {
    const accel = isMacLikePlatform() ? ev.metaKey : (ev.metaKey || ev.ctrlKey);
    if (ev.type === 'keydown' && accel && !ev.altKey) {
      if (!ev.shiftKey && ev.key.toLowerCase() === 't') {
        return !triggerShortcut('terminal.new');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitVertical');
      }
      if (ev.shiftKey && ev.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitHorizontal');
      }
      if (ev.shiftKey && ev.key.toLowerCase() === 'z') {
        return !triggerShortcut('terminal.toggleZoom');
      }
      if (ev.shiftKey && ev.key === 'Enter') {
        return !triggerShortcut('terminal.toggleMaximize');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'w') {
        triggerShortcut('terminal.close');
        return false;
      }
    }
    if (ev.key === 'Enter' && ev.shiftKey && !ev.ctrlKey && !ev.altKey) {
      if (ev.type === 'keydown') {
        sendToPty('\n');
      }
      return false;
    }
    return true;
  };
}

export function writeToTerminal(xterm: XTerm, data: string | Uint8Array): Promise<void> {
  return new Promise((resolve) => {
    xterm.write(data, resolve);
  });
}
