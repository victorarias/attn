import { describe, expect, it } from 'vitest';
import {
  defaultWorkspaceContextKeeperModel,
  isWorkspaceContextKeeperModelPreset,
  parseWorkspaceContextKeeperConfig,
  serializeWorkspaceContextKeeperConfig,
  workspaceContextKeeperModelPresets,
} from './workspaceContextKeeper';

describe('workspaceContextKeeper', () => {
  it('parses and serializes the atomic agent/model pair', () => {
    expect(parseWorkspaceContextKeeperConfig('{"agent":"Codex","model":" gpt-test "}')).toEqual({
      agent: 'codex',
      model: 'gpt-test',
    });
    expect(serializeWorkspaceContextKeeperConfig({ agent: 'claude', model: ' sonnet ' })).toBe(
      '{"agent":"claude","model":"sonnet"}',
    );
  });

  it('treats empty and incomplete settings as disabled', () => {
    expect(parseWorkspaceContextKeeperConfig('')).toBeNull();
    expect(parseWorkspaceContextKeeperConfig('{"agent":"codex"}')).toBeNull();
    expect(parseWorkspaceContextKeeperConfig('not json')).toBeNull();
  });

  it('provides provider-specific defaults while allowing custom models', () => {
    expect(defaultWorkspaceContextKeeperModel('codex')).toBe('gpt-5.4');
    expect(defaultWorkspaceContextKeeperModel('claude')).toBe('opus');
    expect(workspaceContextKeeperModelPresets('codex').map(({ value }) => value)).toEqual([
      'gpt-5.4',
      'gpt-5.4-mini',
    ]);
    expect(isWorkspaceContextKeeperModelPreset('claude', 'sonnet')).toBe(true);
    expect(isWorkspaceContextKeeperModelPreset('claude', 'claude-custom')).toBe(false);
  });
});
