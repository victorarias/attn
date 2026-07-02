import { describe, it, expect } from 'vitest';
import { isTicketOrphaned } from './ticketOrphan';
import { TicketStatus } from '../types/generated';

describe('isTicketOrphaned', () => {
  const stamped = '2026-06-27T10:30:00Z';

  it('is true for every open status with reconciled_at set', () => {
    for (const status of [
      TicketStatus.Todo,
      TicketStatus.Working,
      TicketStatus.Blocked,
      TicketStatus.InReview,
    ]) {
      expect(isTicketOrphaned({ status, reconciled_at: stamped })).toBe(true);
    }
  });

  it('is false for terminal statuses even when stamped', () => {
    for (const status of [TicketStatus.Done, TicketStatus.Failed, TicketStatus.Crashed]) {
      expect(isTicketOrphaned({ status, reconciled_at: stamped })).toBe(false);
    }
  });

  it('is false without the stamp, and for missing tickets', () => {
    expect(isTicketOrphaned({ status: TicketStatus.Working, reconciled_at: undefined })).toBe(false);
    expect(isTicketOrphaned(null)).toBe(false);
    expect(isTicketOrphaned(undefined)).toBe(false);
  });
});
