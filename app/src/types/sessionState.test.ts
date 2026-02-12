import { describe, it, expect } from 'vitest';
import { isAttentionSessionState, normalizeSessionState } from './sessionState';

describe('normalizeSessionState', () => {
  it('returns launching for launching', () => {
    expect(normalizeSessionState('launching')).toBe('launching');
  });

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

  it('returns unknown for unknown states', () => {
    expect(normalizeSessionState('unknown')).toBe('unknown');
    expect(normalizeSessionState('')).toBe('unknown');
    expect(normalizeSessionState('stopped')).toBe('unknown');
  });

  it('returns unknown for legacy waiting state', () => {
    // 'waiting' is only used for PRs, sessions should never receive it
    expect(normalizeSessionState('waiting')).toBe('unknown');
  });
});

describe('isAttentionSessionState', () => {
  it('returns true for attention states', () => {
    expect(isAttentionSessionState('waiting_input')).toBe(true);
    expect(isAttentionSessionState('pending_approval')).toBe(true);
    expect(isAttentionSessionState('unknown')).toBe(true);
  });

  it('returns false for non-attention states', () => {
    expect(isAttentionSessionState('launching')).toBe(false);
    expect(isAttentionSessionState('working')).toBe(false);
    expect(isAttentionSessionState('idle')).toBe(false);
  });
});
