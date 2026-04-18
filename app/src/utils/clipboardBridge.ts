import { isTauri } from '@tauri-apps/api/core';

// Tauri's native clipboard bypasses the webview permission model. Use it for
// writes that originate outside a user gesture (e.g. OSC 52 sequences arriving
// on the PTY stream, or rapid onSelectionChange callbacks that lose transient
// user activation mid-flight). Falls back to navigator.clipboard in the browser.
export async function writeClipboardText(text: string): Promise<void> {
  if (isTauri()) {
    const { writeText } = await import('@tauri-apps/plugin-clipboard-manager');
    await writeText(text);
    return;
  }
  await navigator.clipboard.writeText(text);
}
