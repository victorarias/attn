import { describe, it, expect, vi } from 'vitest';
import {
  executeTicketResumePlan,
  planTicketResume,
  type TicketResumeInput,
} from './ticketResume';

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

describe('executeTicketResumePlan', () => {
  function handlers() {
    return { focus: vi.fn(), spawn: vi.fn(), error: vi.fn() };
  }

  it('routes a focus plan to the focus handler only (no duplicate spawn)', () => {
    const h = handlers();
    executeTicketResumePlan({ kind: 'focus', sessionId: 'sess-1' }, h);
    expect(h.focus).toHaveBeenCalledWith('sess-1');
    expect(h.spawn).not.toHaveBeenCalled();
    expect(h.error).not.toHaveBeenCalled();
  });

  it('routes a spawn plan to the spawn handler with the full plan', () => {
    const h = handlers();
    const plan = {
      kind: 'spawn',
      sessionId: 'sess-1',
      cwd: '/repo',
      agent: 'codex',
      label: 'Migrate the store',
    } as const;
    executeTicketResumePlan(plan, h);
    expect(h.spawn).toHaveBeenCalledWith(plan);
    expect(h.focus).not.toHaveBeenCalled();
    expect(h.error).not.toHaveBeenCalled();
  });

  it('routes an error plan to the error handler', () => {
    const h = handlers();
    executeTicketResumePlan({ kind: 'error', message: 'no agent' }, h);
    expect(h.error).toHaveBeenCalledWith('no agent');
    expect(h.focus).not.toHaveBeenCalled();
    expect(h.spawn).not.toHaveBeenCalled();
  });
});
