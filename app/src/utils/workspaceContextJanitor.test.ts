import { describe, expect, it } from 'vitest';
import {
  defaultWorkspaceContextJanitorModel,
  isWorkspaceContextJanitorModelPreset,
  parseWorkspaceContextJanitorConfig,
  serializeWorkspaceContextJanitorConfig,
  workspaceContextJanitorModelPresets,
} from './workspaceContextJanitor';

describe('workspaceContextJanitor', () => {
  it('parses and serializes the atomic agent/model pair', () => {
    expect(parseWorkspaceContextJanitorConfig('{"agent":"Codex","model":" gpt-test "}')).toEqual({
      agent: 'codex',
      model: 'gpt-test',
    });
    expect(serializeWorkspaceContextJanitorConfig({ agent: 'claude', model: ' sonnet ' })).toBe(
      '{"agent":"claude","model":"sonnet"}',
    );
  });

  it('treats empty and incomplete settings as disabled', () => {
    expect(parseWorkspaceContextJanitorConfig('')).toBeNull();
    expect(parseWorkspaceContextJanitorConfig('{"agent":"codex"}')).toBeNull();
    expect(parseWorkspaceContextJanitorConfig('not json')).toBeNull();
  });

  it('provides provider-specific defaults while allowing custom models', () => {
    expect(defaultWorkspaceContextJanitorModel('codex')).toBe('gpt-5.4');
    expect(defaultWorkspaceContextJanitorModel('claude')).toBe('opus');
    expect(workspaceContextJanitorModelPresets('codex').map(({ value }) => value)).toEqual([
      'gpt-5.4',
      'gpt-5.4-mini',
    ]);
    expect(isWorkspaceContextJanitorModelPreset('claude', 'sonnet')).toBe(true);
    expect(isWorkspaceContextJanitorModelPreset('claude', 'claude-custom')).toBe(false);
  });
});
