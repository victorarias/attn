import { describe, expect, it } from 'vitest';
import {
  parseWorkspaceContextJanitorConfig,
  serializeWorkspaceContextJanitorConfig,
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
});
