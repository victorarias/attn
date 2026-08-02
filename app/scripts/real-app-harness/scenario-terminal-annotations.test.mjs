// The grid-reading helpers the annotation scenario steers by. They decide
// which row is dragged over; a bad choice does not fail loudly, it produces a
// drag that resolves to nothing or to the wrong message, so the scenario would
// report a product regression that is really a parsing bug here.

import { describe, expect, it } from 'vitest';
import { proseRow, wordSpan } from './scenario-terminal-annotations.mjs';

const PROSE = '  A retry wrapper should only retry operations that are idempotent, since';
const SHORT_PROSE = '  Yes, exactly.';

describe('proseRow', () => {
  it('finds the agent\'s prose continuation rows', () => {
    const found = proseRow([
      '❯ what about retries?',
      '',
      '⏺ Here is the answer you asked for.',
      PROSE,
      '',
    ]);
    expect(found?.text).toBe(PROSE);
    expect(found?.row).toBe(3);
  });

  it('skips a tool call, which renders in the same marker but is not prose', () => {
    // "⏺ Bash(...)" and its ⎿ output are not the agent talking, and an
    // annotation on them would address text no transcript message contains.
    const found = proseRow([
      '⏺ Bash(git status --short --branch --porcelain=v2 && echo done)',
      '  git status --short --branch and then some more words here',
      '  ⎿  On branch main and nothing else to report about it at all',
    ]);
    expect(found).toBeNull();
  });

  it('takes the widest prose row, which is the least likely to be a wrap fragment', () => {
    const found = proseRow(['⏺ Answer.', SHORT_PROSE, PROSE]);
    expect(found?.text).toBe(PROSE);
  });

  it('ignores a row too short to drag a whole-word span across', () => {
    expect(proseRow(['⏺ Answer.', SHORT_PROSE])).toBeNull();
  });

  it('ends the block at the first non-indented line', () => {
    // Without this the status footer and the prompt divider read as prose.
    const found = proseRow([
      '⏺ Answer.',
      '✻ Crunched for 13s',
      '  ctx 4% · 5h 8% and some other status words down here',
    ]);
    expect(found).toBeNull();
  });

  it('takes continuations whose marker scrolled off the alternate screen', () => {
    // An answer taller than the grid leaves no "⏺ " on screen at all. Those rows
    // are the whole point of the scenario's second half, so refusing them made
    // the run depend on how much the agent chose to say.
    const found = proseRow([
      PROSE,
      '  and the wrapper itself has no way at all to know which one of those it just made.',
      '',
      '❯ ',
    ]);
    expect(found?.row).toBe(1);
  });

  it('returns null before the agent has said anything', () => {
    expect(proseRow(['❯ ', '──────────────'])).toBeNull();
  });
});

describe('wordSpan', () => {
  it('spans whole words away from both edges of the row', () => {
    const span = wordSpan(PROSE);
    const quoted = PROSE.slice(span.startCol, span.endCol);
    expect(quoted).toBe('retry wrapper should only');
    // Starting inside the indent would drag over the terminal's own gutter,
    // and ending mid-word would anchor half a token.
    expect(span.startCol).toBeGreaterThanOrEqual(PROSE.length - PROSE.trimStart().length);
    expect(PROSE[span.endCol]).not.toMatch(/[A-Za-z]/);
  });

  it('refuses a row with too few words to span safely', () => {
    expect(wordSpan(SHORT_PROSE)).toBeNull();
  });
});
