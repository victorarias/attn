import type { SessionAgent } from '../types/sessionAgent';

export interface AgentAvailability {
  codex: boolean;
  claude: boolean;
  copilot: boolean;
}

const AGENT_ORDER: SessionAgent[] = ['codex', 'claude', 'copilot'];

function parseAvailability(value?: string): boolean | null {
  if (typeof value !== 'string') return null;
  const normalized = value.trim().toLowerCase();
  if (normalized === 'true') return true;
  if (normalized === 'false') return false;
  return null;
}

// Backward compatible with older daemons that do not publish availability keys.
export function getAgentAvailability(settings: Record<string, string>): AgentAvailability {
  const codex = parseAvailability(settings.codex_available);
  const claude = parseAvailability(settings.claude_available);
  const copilot = parseAvailability(settings.copilot_available);

  return {
    codex: codex ?? true,
    claude: claude ?? true,
    copilot: copilot ?? true,
  };
}

export function isAgentAvailable(availability: AgentAvailability, agent: SessionAgent): boolean {
  return availability[agent];
}

export function hasAnyAvailableAgents(availability: AgentAvailability): boolean {
  return availability.codex || availability.claude || availability.copilot;
}

export function firstAvailableAgent(
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  return AGENT_ORDER.find((agent) => availability[agent]) ?? fallback;
}

export function resolvePreferredAgent(
  preferred: SessionAgent | undefined,
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  if (preferred && availability[preferred]) {
    return preferred;
  }
  return firstAvailableAgent(availability, fallback);
}
