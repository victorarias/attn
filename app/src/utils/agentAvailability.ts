import { formatSessionAgentLabel, normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';

export type AgentAvailability = Record<SessionAgent, boolean>;
export type AgentCapabilities = Record<SessionAgent, Record<string, boolean>>;

const BUILTIN_AGENT_ORDER: SessionAgent[] = ['codex', 'claude', 'copilot', 'pi'];

export const AGENT_CAPABILITY_ORDER = [
  'resume',
  'fork',
  'hooks',
  'transcript',
  'transcript_watcher',
  'classifier',
  'state_detector',
] as const;

const AGENT_CAPABILITY_LABELS: Record<string, string> = {
  resume: 'Resume',
  fork: 'Fork',
  hooks: 'Hooks',
  transcript: 'Transcript',
  transcript_watcher: 'Transcript watch',
  classifier: 'Classifier',
  state_detector: 'State detect',
};

function parseAvailability(value?: string): boolean | null {
  if (typeof value !== 'string') return null;
  const normalized = value.trim().toLowerCase();
  if (normalized === 'true') return true;
  if (normalized === 'false') return false;
  return null;
}

function parseAgentFromAvailabilityKey(key: string): SessionAgent | null {
  const normalized = key.trim().toLowerCase();
  if (!normalized.endsWith('_available')) {
    return null;
  }
  const agent = normalized.slice(0, -'_available'.length).trim();
  if (!agent) return null;
  return agent;
}

function parseAgentFromExecutableKey(key: string): SessionAgent | null {
  const normalized = key.trim().toLowerCase();
  if (!normalized.endsWith('_executable')) {
    return null;
  }
  const agent = normalized.slice(0, -'_executable'.length).trim();
  if (!agent || agent === 'editor') return null;
  return agent;
}

function parseAgentCapabilityKey(key: string): { agent: SessionAgent; capability: string } | null {
  const normalized = key.trim().toLowerCase();
  const marker = '_cap_';
  const idx = normalized.indexOf(marker);
  if (idx <= 0) {
    return null;
  }
  const agent = normalized.slice(0, idx).trim();
  const capability = normalized.slice(idx + marker.length).trim();
  if (!agent || !capability) {
    return null;
  }
  return { agent, capability };
}

// Backward compatible with older daemons that do not publish availability keys.
export function getAgentAvailability(settings: Record<string, string>): AgentAvailability {
  const availability: AgentAvailability = {
    codex: true,
    claude: true,
    copilot: true,
    // Pi is new: avoid showing it as available when connected to older daemons
    // that don't publish a pi_available key.
    pi: false,
  };

  for (const [key, value] of Object.entries(settings)) {
    const agent = parseAgentFromAvailabilityKey(key);
    if (!agent) continue;
    const parsed = parseAvailability(value);
    if (parsed == null) continue;
    availability[agent] = parsed;
  }

  return availability;
}

export function orderedAgents(
  availability: AgentAvailability,
  preferred?: SessionAgent,
  fallback: SessionAgent = 'codex',
): SessionAgent[] {
  const all = new Set<SessionAgent>(Object.keys(availability));
  const fallbackAgent = normalizeSessionAgent(fallback, 'codex');
  if (!all.has(fallbackAgent)) all.add(fallbackAgent);
  if (preferred) all.add(normalizeSessionAgent(preferred, fallbackAgent));

  const ordered: SessionAgent[] = [];
  const push = (agent: SessionAgent) => {
    const normalized = normalizeSessionAgent(agent, fallbackAgent);
    if (!all.has(normalized) || ordered.includes(normalized)) return;
    ordered.push(normalized);
  };

  if (preferred) push(preferred);
  for (const agent of BUILTIN_AGENT_ORDER) push(agent);

  const remaining = Array.from(all).filter((agent) => !ordered.includes(agent));
  remaining.sort((a, b) => a.localeCompare(b));
  for (const agent of remaining) ordered.push(agent);

  return ordered;
}

export function getAgentExecutableSettings(settings: Record<string, string>): Record<SessionAgent, string> {
  const executables: Record<SessionAgent, string> = {};
  for (const [key, value] of Object.entries(settings)) {
    const agent = parseAgentFromExecutableKey(key);
    if (!agent) continue;
    executables[agent] = value || '';
  }
  return executables;
}

export function getAgentCapabilities(settings: Record<string, string>): AgentCapabilities {
  const capabilities: AgentCapabilities = {};
  for (const [key, value] of Object.entries(settings)) {
    const parsed = parseAgentCapabilityKey(key);
    if (!parsed) continue;
    const boolValue = parseAvailability(value);
    if (boolValue == null) continue;
    if (!capabilities[parsed.agent]) {
      capabilities[parsed.agent] = {};
    }
    capabilities[parsed.agent][parsed.capability] = boolValue;
  }
  return capabilities;
}

export function agentCapabilityLabel(capability: string): string {
  return AGENT_CAPABILITY_LABELS[capability] || capability.replace(/[_-]+/g, ' ');
}

export function isAgentAvailable(availability: AgentAvailability, agent: SessionAgent): boolean {
  return Boolean(availability[agent]);
}

export function hasAnyAvailableAgents(availability: AgentAvailability): boolean {
  return Object.values(availability).some(Boolean);
}

export function firstAvailableAgent(
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  const preferredOrder = orderedAgents(availability, undefined, fallback);
  return preferredOrder.find((agent) => availability[agent]) ?? normalizeSessionAgent(fallback, 'codex');
}

export function resolvePreferredAgent(
  preferred: SessionAgent | undefined,
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  const normalizedPreferred = preferred ? normalizeSessionAgent(preferred, fallback) : undefined;
  if (normalizedPreferred && availability[normalizedPreferred]) {
    return normalizedPreferred;
  }
  return firstAvailableAgent(availability, fallback);
}

export function agentLabel(agent: SessionAgent): string {
  return formatSessionAgentLabel(agent);
}
