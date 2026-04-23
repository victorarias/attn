import { describe, expect, it } from 'vitest';
import { daemonProfileMatches, healthURLFromWS, profileMismatchMessage } from './buildProfile';

describe('daemonProfileMatches', () => {
  it('treats missing/empty profile as default', () => {
    // The default build expects "default", so missing → match.
    expect(daemonProfileMatches(undefined)).toBe(true);
    expect(daemonProfileMatches(null)).toBe(true);
    expect(daemonProfileMatches('')).toBe(true);
    expect(daemonProfileMatches('   ')).toBe(true);
    expect(daemonProfileMatches('default')).toBe(true);
  });

  it('treats a non-default profile as mismatch for default build', () => {
    expect(daemonProfileMatches('dev')).toBe(false);
    expect(daemonProfileMatches('staging')).toBe(false);
  });
});

describe('healthURLFromWS', () => {
  it('rewrites ws → http and replaces path', () => {
    expect(healthURLFromWS('ws://127.0.0.1:29849/ws')).toBe('http://127.0.0.1:29849/health');
  });

  it('rewrites wss → https', () => {
    expect(healthURLFromWS('wss://example.com:443/ws')).toBe('https://example.com/health');
  });

  it('returns empty string on invalid input', () => {
    expect(healthURLFromWS('not a url')).toBe('');
  });
});

describe('profileMismatchMessage', () => {
  it('mentions both expected and reported profiles', () => {
    const msg = profileMismatchMessage('dev');
    expect(msg).toContain('dev');
    expect(msg).toContain('default');
    expect(msg).toMatch(/refus/i);
  });

  it('uses "default" when the reported profile is missing', () => {
    const msg = profileMismatchMessage(undefined);
    expect(msg).toMatch(/"default"/);
  });
});
