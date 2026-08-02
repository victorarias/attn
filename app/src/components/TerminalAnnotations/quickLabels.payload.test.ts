import { describe, expect, it } from 'vitest';
import { QUICK_LABELS, buildAnnotationPayload } from './quickLabels';

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

  it('is empty with nothing to say', () => {
    expect(buildAnnotationPayload([])).toBe('');
  });
});
