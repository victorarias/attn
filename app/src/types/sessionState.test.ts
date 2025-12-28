import { describe, it, expect } from 'vitest';
import { normalizeSessionState } from './sessionState';

describe('normalizeSessionState', () => {
  it('returns waiting_input for waiting_input', () => {
    expect(normalizeSessionState('waiting_input')).toBe('waiting_input');
  });

  it('returns working for working', () => {
    expect(normalizeSessionState('working')).toBe('working');
  });

  it('returns idle for idle', () => {
    expect(normalizeSessionState('idle')).toBe('idle');
  });

  it('returns pending_approval for pending_approval', () => {
    expect(normalizeSessionState('pending_approval')).toBe('pending_approval');
  });

  it('returns idle for unknown states', () => {
    expect(normalizeSessionState('unknown')).toBe('idle');
    expect(normalizeSessionState('')).toBe('idle');
    expect(normalizeSessionState('stopped')).toBe('idle');
  });

  it('returns idle for legacy waiting state', () => {
    // 'waiting' is only used for PRs, sessions should normalize to idle
    expect(normalizeSessionState('waiting')).toBe('idle');
  });
});
