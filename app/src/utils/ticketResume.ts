// Pure decision for resuming a ticket's agent session, kept out of App.tsx so the
// branching is unit-testable. A ticket's bound session id (`assignee`) is reused as
// the resume session id — that is what lets the daemon resolve the prior
// conversation's resume id (precise resume) and keeps the ticket bound to the same
// session. But if that id is STILL tracked in the frontend's local session store,
// spawning a new local session with it would append a duplicate row (createSession
// does not dedupe) and poison takeSessionSpawnArgs, which grabs the first match.
// So a still-tracked session is focused, not re-spawned; the app's own
// attach/resume-recovery revives a dead runtime on mount.

export interface TicketResumeInput {
  assignee: string;
  cwd: string;
  last_agent_id: string;
  title: string;
}

export type TicketResumePlan =
  | { kind: 'focus'; sessionId: string }
  | { kind: 'spawn'; sessionId: string | undefined; cwd: string; agent: string; label: string }
  | { kind: 'error'; message: string };

export function planTicketResume(
  ticket: TicketResumeInput,
  existingSessionIds: ReadonlySet<string>,
): TicketResumePlan {
  const cwd = ticket.cwd?.trim();
  const agent = ticket.last_agent_id?.trim();
  if (!cwd || !agent) {
    return { kind: 'error', message: 'This ticket has no agent session to resume.' };
  }
  const assignee = ticket.assignee?.trim();
  // The bound session is still tracked locally → focus it instead of spawning a
  // duplicate id.
  if (assignee && existingSessionIds.has(assignee)) {
    return { kind: 'focus', sessionId: assignee };
  }
  // Reuse the bound id so the daemon can resolve the prior resume id; mint a fresh
  // session only when there is no usable bound id (unassigned, or the human "you").
  const sessionId = assignee && assignee !== 'you' ? assignee : undefined;
  return { kind: 'spawn', sessionId, cwd, agent, label: ticket.title };
}
