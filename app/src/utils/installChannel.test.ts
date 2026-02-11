import { describe, expect, it } from 'vitest';
import { normalizeInstallChannel, shouldCheckForReleaseUpdates } from './installChannel';

describe('installChannel', () => {
  it('defaults empty or missing to unknown', () => {
    expect(normalizeInstallChannel(undefined)).toBe('unknown');
    expect(normalizeInstallChannel('')).toBe('unknown');
    expect(normalizeInstallChannel('source')).toBe('source');
  });

  it('normalizes known release channels', () => {
    expect(normalizeInstallChannel('release')).toBe('release');
    expect(normalizeInstallChannel('  ReLeAsE ')).toBe('release');
    expect(normalizeInstallChannel('homebrew')).toBe('homebrew');
    expect(normalizeInstallChannel('cask')).toBe('cask');
    expect(normalizeInstallChannel('dmg')).toBe('dmg');
  });

  it('marks unknown channels as unknown', () => {
    expect(normalizeInstallChannel('nightly')).toBe('unknown');
  });

  it('checks for updates only on release-style channels', () => {
    expect(shouldCheckForReleaseUpdates('source')).toBe(false);
    expect(shouldCheckForReleaseUpdates(undefined)).toBe(true);
    expect(shouldCheckForReleaseUpdates('release')).toBe(true);
    expect(shouldCheckForReleaseUpdates('homebrew')).toBe(true);
    expect(shouldCheckForReleaseUpdates('cask')).toBe(true);
    expect(shouldCheckForReleaseUpdates('dmg')).toBe(true);
    expect(shouldCheckForReleaseUpdates('nightly')).toBe(true);
  });
});
