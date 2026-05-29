import { isTauri } from '@tauri-apps/api/core';

const TERMINAL_LINK_HIT_TEST_DIR = 'debug';
const TERMINAL_LINK_HIT_TEST_FILE = `${TERMINAL_LINK_HIT_TEST_DIR}/terminal-link-hit-test.jsonl`;
const MAX_STRING_LENGTH = 500;

export interface TerminalLinkHitTestEvent {
  at: string;
  event: string;
  debugName: string;
  sessionId?: string;
  paneId?: string;
  runtimeId?: string;
  details: Record<string, unknown>;
}

let fileWriteChain: Promise<void> = Promise.resolve();

declare global {
  interface Window {
    __ATTN_TERMINAL_LINK_HIT_TEST_FILE?: string;
  }
}

function redactString(value: string): string {
  return value
    .replace(/\bsk-[A-Za-z0-9_-]{20,}\b/g, '[REDACTED]')
    .replace(/\b[A-Za-z0-9_-]{80,}\b/g, '[REDACTED_LONG_TOKEN]')
    .slice(0, MAX_STRING_LENGTH);
}

function sanitizeForLog(value: unknown): unknown {
  if (typeof value === 'string') return redactString(value);
  if (Array.isArray(value)) return value.map(sanitizeForLog);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>).map(([key, entry]) => [key, sanitizeForLog(entry)]),
  );
}

async function appendLinkHitTestEventToFile(entry: TerminalLinkHitTestEvent) {
  if (!isTauri()) return;
  try {
    const { mkdir, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(TERMINAL_LINK_HIT_TEST_DIR, {
      baseDir: BaseDirectory.AppLocalData,
      recursive: true,
    });
    await writeTextFile(
      TERMINAL_LINK_HIT_TEST_FILE,
      `${JSON.stringify(sanitizeForLog(entry))}\n`,
      { baseDir: BaseDirectory.AppLocalData, append: true, create: true },
    );
  } catch (error) {
    console.warn('[TerminalLinkHitTest] Failed to append hit-test event:', error);
  }
}

export function recordTerminalLinkHitTestEvent(
  event: Omit<TerminalLinkHitTestEvent, 'at'>,
) {
  if (typeof window !== 'undefined') {
    window.__ATTN_TERMINAL_LINK_HIT_TEST_FILE = `$APPLOCALDATA/${TERMINAL_LINK_HIT_TEST_FILE}`;
  }
  const entry: TerminalLinkHitTestEvent = {
    at: new Date().toISOString(),
    ...event,
  };
  fileWriteChain = fileWriteChain
    .catch(() => undefined)
    .then(() => appendLinkHitTestEventToFile(entry));
}

