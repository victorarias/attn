import { describe, expect, it } from 'vitest';
import { evaluateVisibleContentGates, formatGateReport } from './scenarioAssertions.mjs';
import { formatResultTable, selectFailedScenarios } from './matrixDigest.mjs';

function visibleContent({
  lines = [],
  nonEmptyLineCount = 0,
  denseLineCount = 0,
  charCount = 0,
  maxLineLength = 0,
} = {}) {
  return {
    lines,
    summary: { nonEmptyLineCount, denseLineCount, charCount, maxLineLength },
  };
}

describe('evaluateVisibleContentGates', () => {
  it('skips the contains gate entirely when no needle was requested', () => {
    const gates = evaluateVisibleContentGates(visibleContent(), { contains: null });
    expect(gates.some((gate) => gate.gate === 'contains')).toBe(false);
  });

  it('marks contains ok when the needle is present, failed when absent', () => {
    const content = visibleContent({ lines: ['hello world'] });
    const found = evaluateVisibleContentGates(content, { contains: 'hello' });
    const containsGate = found.find((gate) => gate.gate === 'contains');
    expect(containsGate.ok).toBe(true);
    expect(containsGate.actual).toBe(true);

    const missing = evaluateVisibleContentGates(content, { contains: 'goodbye' });
    const missingGate = missing.find((gate) => gate.gate === 'contains');
    expect(missingGate.ok).toBe(false);
    expect(missingGate.actual).toBe(false);
  });

  it('truncates a long contains needle to 40 chars in the required field', () => {
    const needle = 'x'.repeat(60);
    const gates = evaluateVisibleContentGates(visibleContent({ lines: [needle] }), { contains: needle });
    const containsGate = gates.find((gate) => gate.gate === 'contains');
    expect(containsGate.required).toBe('x'.repeat(40));
  });

  it('computes nonEmptyLines/denseLines/charCount/maxLineLength boundaries with actual/required in the right direction', () => {
    // Exactly at the minimum: ok. One below: fails. This pins actual >= required,
    // not required >= actual (a swap would flip both cases).
    const atMinimum = evaluateVisibleContentGates(
      visibleContent({ nonEmptyLineCount: 8, denseLineCount: 3, charCount: 20, maxLineLength: 20 }),
      { minNonEmptyLines: 8, minDenseLines: 3, minCharCount: 20, minMaxLineLength: 20 },
    );
    for (const gate of atMinimum) {
      expect(gate.ok).toBe(true);
    }

    const belowMinimum = evaluateVisibleContentGates(
      visibleContent({ nonEmptyLineCount: 7, denseLineCount: 2, charCount: 19, maxLineLength: 19 }),
      { minNonEmptyLines: 8, minDenseLines: 3, minCharCount: 20, minMaxLineLength: 20 },
    );
    for (const gate of belowMinimum) {
      expect(gate.ok).toBe(false);
    }

    const nonEmptyGate = belowMinimum.find((gate) => gate.gate === 'nonEmptyLines');
    expect(nonEmptyGate.actual).toBe(7);
    expect(nonEmptyGate.required).toBe(8);
  });
});

describe('formatGateReport', () => {
  it('renders contains as bare OK/FAIL and other gates as actual/required OK/FAIL', () => {
    const gates = [
      { gate: 'contains', actual: true, required: 'needle', ok: true },
      { gate: 'nonEmptyLines', actual: 19, required: 8, ok: true },
      { gate: 'denseLines', actual: 0, required: 3, ok: false },
    ];
    expect(formatGateReport(gates)).toBe('contains OK | nonEmptyLines 19/8 OK | denseLines 0/3 FAIL');
  });

  it('never reports a failing gate as OK or a passing gate as FAIL', () => {
    // A regression that swapped the ok flag (or hardcoded 'OK') would slip past
    // a report string it isn't compared against — assert the substring lands
    // next to the gate it belongs to instead.
    const gates = [
      { gate: 'charCount', actual: 5, required: 20, ok: false },
      { gate: 'maxLineLength', actual: 25, required: 20, ok: true },
    ];
    const report = formatGateReport(gates);
    expect(report).toContain('charCount 5/20 FAIL');
    expect(report).toContain('maxLineLength 25/20 OK');
    expect(report).not.toContain('charCount 5/20 OK');
    expect(report).not.toContain('maxLineLength 25/20 FAIL');
  });
});

describe('selectFailedScenarios', () => {
  it('selects only scenarios with a non-zero exit code', () => {
    const lastMatrix = {
      results: [
        { id: 'tr401', code: 0 },
        { id: 'tr402', code: 1 },
        { id: 'tr502', code: 124 },
      ],
    };
    expect(selectFailedScenarios(lastMatrix)).toEqual(['tr402', 'tr502']);
  });

  it('returns an empty array when everything passed or results are missing', () => {
    expect(selectFailedScenarios({ results: [{ id: 'tr401', code: 0 }] })).toEqual([]);
    expect(selectFailedScenarios({})).toEqual([]);
    expect(selectFailedScenarios(null)).toEqual([]);
  });
});

describe('formatResultTable', () => {
  it('labels a zero exit code PASS and any non-zero code FAIL', () => {
    const table = formatResultTable([
      { id: 'tr401', code: 0, durationMs: 1500 },
      { id: 'tr402', code: 1, durationMs: 2500 },
    ]);
    const lines = table.split('\n');
    expect(lines[0]).toMatch(/^PASS\s+tr401\s+1\.5s$/);
    expect(lines[1]).toMatch(/^FAIL\s+tr402\s+2\.5s$/);
  });
});
