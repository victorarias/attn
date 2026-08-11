// The grid-reading helpers the annotation scenario steers by. They decide
// which row is dragged over; a bad choice does not fail loudly, it produces a
// drag that resolves to nothing or to the wrong message, so the scenario would
// report a product regression that is really a parsing bug here.

import { describe, expect, it } from 'vitest';
import { proseRow, wordSpan } from './scenario-terminal-annotations.mjs';

const PROSE = '• A retry wrapper protects idempotent operations from duplicate network effects.';
const SHORT_PROSE = '  Yes, exactly.';
const REQUIRED = ['retry', 'wrapper', 'idempotent', 'duplicate', 'network', 'effects'];

describe('proseRow', () => {
  it('finds Codex assistant prose by its bullet and requested words', () => {
    const found = proseRow([
      '› explain retry wrappers',
      '',
      PROSE,
      '',
    ], REQUIRED);
    expect(found?.text).toBe(PROSE);
    expect(found?.row).toBe(2);
  });

  it('skips prompt and tool rows even when they repeat requested words', () => {
    const found = proseRow([
      '› A retry wrapper protects idempotent operations from duplicate network effects.',
      '└ Ran retry wrapper diagnostic for idempotent duplicate network effects',
    ], REQUIRED);
    expect(found).toBeNull();
  });

  it('takes the row matching the most requested words', () => {
    const found = proseRow([
      '  A retry wrapper handles idempotent requests in ordinary prose.',
      PROSE,
    ], REQUIRED);
    expect(found?.text).toBe(PROSE);
  });

  it('ignores a row too short to drag a whole-word span across', () => {
    expect(proseRow([SHORT_PROSE], REQUIRED)).toBeNull();
  });

  it('ignores terminal chrome', () => {
    const found = proseRow([
      '────────────────────────────────────────────────────────────',
      '│ retry wrapper idempotent duplicate network effects status │',
    ], REQUIRED);
    expect(found).toBeNull();
  });

  it('takes the marked row of a wrapped assistant message', () => {
    const found = proseRow([
      '• A retry wrapper protects idempotent operations from',
      '  duplicate network effects while dependencies are failing safely.',
      '',
      '› ',
    ], REQUIRED);
    expect(found?.row).toBe(0);
  });

  it('returns null before the agent has said anything', () => {
    expect(proseRow(['› ', '──────────────'], REQUIRED)).toBeNull();
  });
});

describe('wordSpan', () => {
  it('spans whole words away from both edges of the row', () => {
    const span = wordSpan(PROSE);
    const quoted = PROSE.slice(span.startCol, span.endCol);
    expect(quoted).toBe('retry wrapper protects idempotent');
    // Starting inside the indent would drag over the terminal's own gutter,
    // and ending mid-word would anchor half a token.
    expect(span.startCol).toBeGreaterThanOrEqual(PROSE.length - PROSE.trimStart().length);
    expect(PROSE[span.endCol]).not.toMatch(/[A-Za-z]/);
  });

  it('refuses a row with too few words to span safely', () => {
    expect(wordSpan(SHORT_PROSE)).toBeNull();
  });
});
