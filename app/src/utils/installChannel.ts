export type InstallChannel =
  | 'source'
  | 'release'
  | 'homebrew'
  | 'cask'
  | 'dmg'
  | 'unknown';

export function normalizeInstallChannel(channel?: string | null): InstallChannel {
  const normalized = (channel ?? '').trim().toLowerCase();
  if (normalized === '') return 'unknown';
  if (normalized === 'source') return 'source';
  if (normalized === 'release') return 'release';
  if (normalized === 'homebrew') return 'homebrew';
  if (normalized === 'cask') return 'cask';
  if (normalized === 'dmg') return 'dmg';
  return 'unknown';
}

export function shouldCheckForReleaseUpdates(channel?: string | null): boolean {
  const normalized = normalizeInstallChannel(channel);
  // Fail open: only explicit source installs skip release polling.
  return normalized !== 'source';
}
