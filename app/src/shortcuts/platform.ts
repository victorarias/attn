// app/src/shortcuts/platform.ts
// Keep shortcut modifier semantics consistent across the app.
//
// "Accelerator" key:
// - macOS: Command (meta)
// - Windows/Linux: Control

export function isMacLikePlatform(): boolean {
  if (typeof navigator === 'undefined') return false;

  const platform = String(navigator.platform || '').toLowerCase();
  if (platform.includes('mac')) return true;

  // Fallback for environments where platform is empty/overridden.
  const ua = String(navigator.userAgent || '').toLowerCase();
  return ua.includes('mac os') || ua.includes('macintosh');
}

export function isAccelKeyPressed(e: KeyboardEvent): boolean {
  return isMacLikePlatform() ? e.metaKey : e.ctrlKey;
}

