import { describe, it, expect } from 'vitest';
import { planTicketResume, type TicketResumeInput } from './ticketResume';

function ticket(overrides: Partial<TicketResumeInput> = {}): TicketResumeInput {
  return {
    assignee: 'sess-1',
    cwd: '/repo',
    last_agent_id: 'codex',
    title: 'Migrate the store',
    ...overrides,
  };
}

describe('planTicketResume', () => {
  it('focuses the bound session when it is still tracked locally (no duplicate spawn)', () => {
    // Regression: re-spawning an id already in the local store appends a duplicate
    // row and poisons takeSessionSpawnArgs. The bound session must be focused.
    const plan = planTicketResume(ticket({ assignee: 'sess-1' }), new Set(['sess-1', 'other']));
    expect(plan).toEqual({ kind: 'focus', sessionId: 'sess-1' });
  });

  it('spawns reusing the bound id when the session is gone from the local store', () => {
    const plan = planTicketResume(ticket({ assignee: 'sess-1' }), new Set(['other']));
    expect(plan).toEqual({
      kind: 'spawn',
      sessionId: 'sess-1',
      cwd: '/repo',
      agent: 'codex',
      label: 'Migrate the store',
    });
  });

  it('mints a fresh session (no reused id) when the ticket has no usable bound id', () => {
    for (const assignee of ['', 'you']) {
      const plan = planTicketResume(ticket({ assignee }), new Set());
      expect(plan).toEqual({
        kind: 'spawn',
        sessionId: undefined,
        cwd: '/repo',
        agent: 'codex',
        label: 'Migrate the store',
      });
    }
  });

  it('errors when the ticket has no agent session to resume', () => {
    expect(planTicketResume(ticket({ cwd: '' }), new Set())).toEqual({
      kind: 'error',
      message: 'This ticket has no agent session to resume.',
    });
    expect(planTicketResume(ticket({ last_agent_id: '' }), new Set())).toEqual({
      kind: 'error',
      message: 'This ticket has no agent session to resume.',
    });
  });
});
