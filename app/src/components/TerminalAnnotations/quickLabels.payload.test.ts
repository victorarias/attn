import { describe, expect, it } from 'vitest';
import { QUICK_LABELS, QUICK_LABEL_GROUPS, buildAnnotationPayload } from './quickLabels';

describe('QUICK_LABEL_GROUPS', () => {
  it('keeps agreement in its own group, ahead of everything that asks for a change', () => {
    // A reviewer who cannot cheaply say "this part is right" files only
    // objections. Ninth in a run of corrections, the one positive mark reads as
    // one more complaint — the group is what a divider draws between.
    expect(QUICK_LABEL_GROUPS[0].map((label) => label.id)).toEqual(['exactly-this']);
    expect(QUICK_LABEL_GROUPS).toHaveLength(2);
    expect(QUICK_LABELS[0].emoji).toBe('💯');
  });

  it('flattens to the whole row, so a grouped label is still resolvable', () => {
    expect(QUICK_LABELS).toEqual(QUICK_LABEL_GROUPS.flat());
    expect(new Set(QUICK_LABELS.map((label) => label.emoji)).size).toBe(QUICK_LABELS.length);
  });
});

describe('buildAnnotationPayload', () => {
  it('leads each item with the words it is about', () => {
    // The agent cannot see the highlight. The quote is the only thing that
    // tells it which part of its own answer the feedback lands on.
    const payload = buildAnnotationPayload([
      { start: 40, emoji: '🧪', comment: '', quote: 'ship it without tests' },
    ]);

    expect(payload).toContain('> ship it without tests');
    expect(payload).toContain('🧪 Needs tests');
  });

  it('sends the label instruction, not the label name', () => {
    // "Verify this" is a chip. What the agent has to act on is the sentence
    // behind it.
    const verify = QUICK_LABELS.find((label) => label.id === 'verify-this')!;

    const payload = buildAnnotationPayload([
      { start: 0, emoji: verify.emoji, comment: '', quote: 'the parser already handles this' },
    ]);

    expect(payload).toContain(verify.tip!);
  });

  it('orders items by position in the message, not by when they were made', () => {
    const payload = buildAnnotationPayload([
      { start: 900, emoji: '❓', comment: '', quote: 'the last claim' },
      { start: 10, emoji: '❓', comment: '', quote: 'the first claim' },
    ]);

    expect(payload.indexOf('the first claim')).toBeLessThan(payload.indexOf('the last claim'));
    expect(payload).toMatch(/## 1\..*\n\n> the first claim/);
  });

  it('carries a bare comment with no label', () => {
    const payload = buildAnnotationPayload([
      { start: 0, emoji: '', comment: 'this contradicts the paragraph above', quote: 'always safe' },
    ]);

    expect(payload).toContain('💬 Comment');
    expect(payload).toContain('this contradicts the paragraph above');
  });

  it('tells the agent what to do with agreement, not just that it got one', () => {
    // "💯" alone is a pat on the head. What makes marking a thing right worth
    // the click is that the agent is told to keep it through the next revision.
    const payload = buildAnnotationPayload([
      { start: 0, emoji: '💯', comment: '', quote: 'the retry only fires on idempotent verbs' },
    ]);

    expect(payload).toContain('💯 Exactly this');
    expect(payload).toContain('Preserve this decision');
  });

  it('says what the payload is without instructing the agent how to behave', () => {
    // The preamble names the thing so the agent knows these are marks on its
    // own last message. Telling it to "address each annotation" on top of that
    // is an instruction the annotations already are.
    const payload = buildAnnotationPayload([
      { start: 0, emoji: '❓', comment: '', quote: 'always safe' },
    ]);

    expect(payload.split('\n')[0]).toBe('Feedback on your last message.');
  });

  it('is empty with nothing to say', () => {
    expect(buildAnnotationPayload([])).toBe('');
  });

  describe('with a note', () => {
    const MARKS = [
      { start: 40, emoji: '🧪', comment: '', quote: 'the retry wrapper' },
      { start: 4, emoji: '❓', comment: 'which parser?', quote: 'the parser' },
    ];

    it('puts the note ahead of the marks it qualifies', () => {
      // The note is the instruction and the marks are where it lands. Sent
      // after them it reads as an afterthought, which is the ordering that has
      // people typing "…and also consider the feedback below".
      const payload = buildAnnotationPayload(MARKS, 'Split this into two PRs.');
      const lines = payload.split('\n').filter((line) => line !== '');

      expect(lines[0]).toBe('Feedback on your last message.');
      expect(lines[1]).toBe('Split this into two PRs.');
      expect(lines[2]).toMatch(/^## 1\./);
    });

    it('leaves a whitespace-only note out entirely', () => {
      expect(buildAnnotationPayload(MARKS, '   \n  ')).toBe(buildAnnotationPayload(MARKS));
    });

    it('keeps the marks in reading order under the note', () => {
      const payload = buildAnnotationPayload(MARKS, 'Split this into two PRs.');

      expect(payload.indexOf('the parser')).toBeLessThan(payload.indexOf('the retry wrapper'));
    });
  });
});
