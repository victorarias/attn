import { invoke, isTauri } from '@tauri-apps/api/core';
import { recordUiDiag } from '../utils/uiDiagnosticsLog';

export const MAX_BROWSER_CONTROL_RESULT_BYTES = 24 * 1024 * 1024;

export interface BrowserHostRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

const tracedBrowserHostState = new Map<string, { visible: boolean; suspicious: boolean }>();

function shouldTraceBrowserUpdate(label: string, rect: BrowserHostRect, visible: boolean): boolean {
  const suspicious = visible && (
    rect.width >= window.innerWidth * 0.9
    || rect.height >= window.innerHeight * 0.9
    || rect.width <= 1
    || rect.height <= 1
  );
  const previous = tracedBrowserHostState.get(label);
  tracedBrowserHostState.set(label, { visible, suspicious });
  return !previous || previous.visible !== visible || suspicious !== previous.suspicious;
}

export function serializeBrowserControlResultMessage(
  message: {
    cmd: 'browser_control_result';
    request_id: string;
    success: boolean;
    data?: string;
    error?: string;
  },
  maxBytes = MAX_BROWSER_CONTROL_RESULT_BYTES,
): string {
  const serialized = JSON.stringify(message);
  const bytes = new TextEncoder().encode(serialized).byteLength;
  if (bytes > maxBytes) {
    throw new Error(
      `serialized browser control result is ${bytes} bytes; the maximum supported result is ${maxBytes} bytes`,
    );
  }
  return serialized;
}

function safeLabelPart(value: string): string {
  return value.replace(/[^a-zA-Z0-9_-]/g, '-');
}

export function browserHostLabel(workspaceId: string, tileId: string): string {
  return `browser-${safeLabelPart(workspaceId)}-${safeLabelPart(tileId)}`;
}

export async function mountBrowserHost(
  label: string,
  url: string,
  rect: BrowserHostRect,
  visible: boolean,
): Promise<void> {
  if (!isTauri()) {
    throw new Error('In-app browser hosting requires the Tauri app');
  }
  recordUiDiag({ kind: 'browser_host_request', action: 'mount', label, rect, visible });
  tracedBrowserHostState.set(label, { visible, suspicious: false });
  try {
    await invoke('browser_host_mount', { label, url, ...rect, visible });
    recordUiDiag({ kind: 'browser_host_result', action: 'mount', label, rect, visible });
  } catch (error) {
    recordUiDiag({ kind: 'browser_host_error', action: 'mount', label, rect, visible, error: String(error) });
    throw error;
  }
}

export async function updateBrowserHost(label: string, rect: BrowserHostRect, visible: boolean): Promise<void> {
  if (!isTauri()) return;
  const trace = shouldTraceBrowserUpdate(label, rect, visible);
  if (trace) recordUiDiag({ kind: 'browser_host_request', action: 'update', label, rect, visible });
  try {
    await invoke('browser_host_update', { label, ...rect, visible });
    if (trace) recordUiDiag({ kind: 'browser_host_result', action: 'update', label, rect, visible });
  } catch (error) {
    recordUiDiag({ kind: 'browser_host_error', action: 'update', label, rect, visible, error: String(error) });
    throw error;
  }
}

export async function unmountBrowserHost(label: string): Promise<void> {
  if (!isTauri()) return;
  recordUiDiag({ kind: 'browser_host_request', action: 'unmount', label });
  try {
    await invoke('browser_host_unmount', { label });
    tracedBrowserHostState.delete(label);
    recordUiDiag({ kind: 'browser_host_result', action: 'unmount', label });
  } catch (error) {
    recordUiDiag({ kind: 'browser_host_error', action: 'unmount', label, error: String(error) });
    throw error;
  }
}

export function clearBrowserHostFocus(): void {
  if (!isTauri()) return;
  void invoke('browser_host_clear_focus').catch((error) => {
    console.warn('[BrowserHost] Failed to clear browser focus:', error);
  });
}

export function claimBrowserHostFocus(label: string): void {
  if (!isTauri()) return;
  void invoke('browser_host_claim_focus', { label }).catch((error) => {
    console.warn('[BrowserHost] Failed to claim browser focus:', error);
  });
}

export function isBrowserHostOwnedTarget(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest('[data-browser-host-owner]') !== null;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

export async function controlBrowserHost(
  workspaceId: string,
  tileId: string,
  action: string,
  params?: string,
  selector?: string,
  text?: string,
): Promise<string> {
  const label = browserHostLabel(workspaceId, tileId);
  const deadline = Date.now() + 5_000;
  for (;;) {
    try {
      return await invoke<string>('browser_host_control', { label, action, params, selector, text });
    } catch (error) {
      if (!String(error).includes('not mounted') || Date.now() >= deadline) {
        throw error;
      }
      await delay(100);
    }
  }
}
