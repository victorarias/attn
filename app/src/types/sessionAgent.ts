export type SessionAgent = 'codex' | 'claude';

export function normalizeSessionAgent(
  agent: string | null | undefined,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  if (agent === 'claude' || agent === 'codex') {
    return agent;
  }
  return fallback;
}
