import { describe, expect, it } from 'vitest';
import {
  LABEL_COLOR_MAP,
  QUICK_LABELS,
  QUICK_LABEL_GROUPS,
  labelById,
  labelByEmoji,
} from './quickLabels';

describe('the shared label set', () => {
  it('is one set, drawn from by both annotation surfaces', async () => {
    const terminal = await import('../components/TerminalAnnotations/quickLabels');
    const markdown = await import('../components/MarkdownReader/annotations/quickLabels');

    expect(terminal.QUICK_LABELS).toBe(QUICK_LABELS);
    expect(markdown.QUICK_LABELS).toBe(QUICK_LABELS);
  });

  it('flattens the groups in row order, with no repeated identity', () => {
    expect(QUICK_LABELS).toEqual(QUICK_LABEL_GROUPS.flat());
    expect(new Set(QUICK_LABELS.map((label) => label.emoji)).size).toBe(QUICK_LABELS.length);
    expect(new Set(QUICK_LABELS.map((label) => label.id)).size).toBe(QUICK_LABELS.length);
  });

  it('gives every label a color the reader can actually draw', () => {
    for (const label of QUICK_LABELS) {
      expect(LABEL_COLOR_MAP[label.color], label.id).toBeDefined();
    }
  });
});

describe('label lookups', () => {
  it('resolve a mark back to the label it was made with', () => {
    expect(labelByEmoji('💯')?.id).toBe('exactly-this');
    expect(labelByEmoji('👍')?.id).toBe('i-agree');
    expect(labelById('verify-this')?.text).toBe('Verify this');
    expect(labelByEmoji('🦄')).toBeUndefined();
    expect(labelById('never-existed')).toBeUndefined();
  });
});
