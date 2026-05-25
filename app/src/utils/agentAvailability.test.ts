import { describe, expect, it } from 'vitest';
import {
  agentCapabilityLabel,
  firstAvailableAgent,
  getAgentAvailability,
  getAgentCapabilities,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  isAgentAvailable,
  orderedAgents,
  resolvePreferredAgent,
} from './agentAvailability';

describe('agentAvailability', () => {
  it('defaults in-tree built-ins without pre-advertising plugin agents', () => {
    const availability = getAgentAvailability({});
    expect(availability).toEqual({ codex: true, claude: true, copilot: true });
  });

  it('parses dynamic daemon availability flags', () => {
    const availability = getAgentAvailability({
      codex_available: 'true',
      claude_available: 'false',
      copilot_available: 'false',
      pi_available: 'true',
      'gemini-cli_available': 'true',
    });
    expect(availability).toEqual({
      codex: true,
      claude: false,
      copilot: false,
      pi: true,
      'gemini-cli': true,
    });
  });

  it('orders preferred, built-ins, then extras', () => {
    const availability = {
      codex: true,
      claude: true,
      copilot: true,
      pi: true,
      'gemini-cli': true,
      mistral: true,
    };
    const ordered = orderedAgents(availability, 'pi', 'codex');
    expect(ordered.slice(0, 4)).toEqual(['pi', 'codex', 'claude', 'copilot']);
    expect(ordered).toContain('gemini-cli');
    expect(ordered).toContain('mistral');
  });

  it('resolves preferred agent to first available fallback', () => {
    const availability = { codex: false, claude: false, copilot: true, pi: false };
    expect(resolvePreferredAgent('claude', availability, 'codex')).toBe('copilot');
    expect(firstAvailableAgent(availability, 'codex')).toBe('copilot');
  });

  it('reports per-agent availability and any-available status', () => {
    const availability = { codex: false, claude: false, copilot: false, pi: false };
    expect(isAgentAvailable(availability, 'codex')).toBe(false);
    expect(hasAnyAvailableAgents(availability)).toBe(false);
  });

  it('extracts only attn-owned executable settings', () => {
    const executables = getAgentExecutableSettings({
      codex_executable: '/usr/local/bin/codex',
      snipe_executable: '/opt/snipe',
      editor_executable: 'code',
    });
    expect(executables).toEqual({
      codex: '/usr/local/bin/codex',
    });
  });

  it('extracts per-agent capability flags', () => {
    const caps = getAgentCapabilities({
      codex_cap_transcript: 'true',
      codex_cap_classifier: 'false',
      pi_cap_transcript: 'false',
      pi_cap_resume: 'false',
    });
    expect(caps).toEqual({
      codex: {
        transcript: true,
        classifier: false,
      },
      pi: {
        transcript: false,
        resume: false,
      },
    });
    expect(agentCapabilityLabel('transcript_watcher')).toBe('Transcript watch');
    expect(agentCapabilityLabel('custom_cap')).toBe('custom cap');
  });
});
