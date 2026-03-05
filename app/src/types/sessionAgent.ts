export type SessionAgent = string;

export function normalizeSessionAgent(
  agent: string | null | undefined,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  const normalized = agent?.trim().toLowerCase();
  if (!normalized) {
    return fallback;
  }
  return normalized;
}

export function formatSessionAgentLabel(agent: SessionAgent): string {
  const normalized = normalizeSessionAgent(agent, 'codex');
  switch (normalized) {
    case 'claude':
      return 'Claude';
    case 'codex':
      return 'Codex';
    case 'copilot':
      return 'Copilot';
    case 'pi':
      return 'Pi';
    default:
      return normalized
        .split(/[-_\s]+/)
        .filter(Boolean)
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join(' ');
  }
}
