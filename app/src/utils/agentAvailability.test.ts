import { describe, expect, it } from 'vitest';
import {
  firstAvailableAgent,
  getAgentAvailability,
  hasAnyAvailableAgents,
  isAgentAvailable,
  resolvePreferredAgent,
} from './agentAvailability';

describe('agentAvailability', () => {
  it('defaults to available when daemon keys are missing', () => {
    const availability = getAgentAvailability({});
    expect(availability).toEqual({ codex: true, claude: true, copilot: true });
  });

  it('parses daemon availability flags', () => {
    const availability = getAgentAvailability({
      codex_available: 'true',
      claude_available: 'false',
      copilot_available: 'false',
    });
    expect(availability).toEqual({ codex: true, claude: false, copilot: false });
  });

  it('resolves preferred agent to first available fallback', () => {
    const availability = { codex: false, claude: false, copilot: true };
    expect(resolvePreferredAgent('claude', availability, 'codex')).toBe('copilot');
    expect(firstAvailableAgent(availability, 'codex')).toBe('copilot');
  });

  it('reports per-agent availability and any-available status', () => {
    const availability = { codex: false, claude: false, copilot: false };
    expect(isAgentAvailable(availability, 'codex')).toBe(false);
    expect(hasAnyAvailableAgents(availability)).toBe(false);
  });
});
