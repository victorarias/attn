export type SessionAgent = 'codex' | 'claude' | 'copilot';

export function normalizeSessionAgent(
  agent: string | null | undefined,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  if (agent === 'claude' || agent === 'codex' || agent === 'copilot') {
    return agent;
  }
  return fallback;
}
