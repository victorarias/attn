import { invoke, isTauri } from '@tauri-apps/api/core';

export const MAX_BROWSER_CONTROL_RESULT_BYTES = 24 * 1024 * 1024;

export interface BrowserHostRect {
  x: number;
  y: number;
  width: number;
  height: number;
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
  await invoke('browser_host_mount', { label, url, ...rect, visible });
}

export async function updateBrowserHost(label: string, rect: BrowserHostRect, visible: boolean): Promise<void> {
  if (!isTauri()) return;
  await invoke('browser_host_update', { label, ...rect, visible });
}

export async function unmountBrowserHost(label: string): Promise<void> {
  if (!isTauri()) return;
  await invoke('browser_host_unmount', { label });
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
