import { describe, expect, it } from 'vitest';
import { resolveScenario, resolveScenarios, scenarioCatalog } from './scenarioCatalog.mjs';

describe('scenarioCatalog soakOnly handling', () => {
  it('has the focus-probe entry marked soakOnly', () => {
    const focusProbe = scenarioCatalog.find((scenario) => scenario.id === 'focus-probe');

    expect(focusProbe).toBeDefined();
    expect(focusProbe.soakOnly).toBe(true);
  });

  it('excludes soakOnly entries from the full matrix sweep', () => {
    const scenarios = resolveScenarios([]);

    expect(scenarios.some((scenario) => scenario.soakOnly)).toBe(false);
    expect(scenarios.some((scenario) => scenario.id === 'focus-probe')).toBe(false);
    // The rest of the catalog is untouched.
    expect(scenarios.length).toBe(scenarioCatalog.filter((scenario) => !scenario.soakOnly).length);
  });

  it('rejects explicit matrix selection of a soakOnly scenario', () => {
    expect(() => resolveScenarios(['focus-probe'])).toThrow('Unknown scenario id: focus-probe');
  });

  it('still resolves regular scenarios by explicit matrix selection', () => {
    const scenarios = resolveScenarios(['ghostty-scroll']);

    expect(scenarios).toHaveLength(1);
    expect(scenarios[0].id).toBe('ghostty-scroll');
  });

  it('resolves a soakOnly scenario via direct single-scenario resolution', () => {
    const scenario = resolveScenario('focus-probe');

    expect(scenario.id).toBe('focus-probe');
    expect(scenario.command).toEqual(['pnpm', 'run', 'real-app:focus-probe']);
  });

  it('throws on an unknown id in direct single-scenario resolution', () => {
    expect(() => resolveScenario('does-not-exist')).toThrow('Unknown scenario id: does-not-exist');
  });
});
