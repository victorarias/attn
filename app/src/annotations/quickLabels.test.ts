import { describe, expect, it } from 'vitest';
import {
  LABEL_COLOR_MAP,
  QUICK_LABELS,
  QUICK_LABEL_GROUPS,
  RETIRED_LABELS,
  labelById,
  labelByEmoji,
} from './quickLabels';

describe('the shared label set', () => {
  it('is one set, drawn from by both annotation surfaces', async () => {
    // Terminal marks and document marks resolve the same labels. Two sets is
    // what this replaced, and an import that quietly forks one back is the way
    // it would come back.
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
    // The terminal row is emoji-only, so a label added there is easy to ship
    // with a color the map has never heard of — and the Markdown chip then
    // renders unstyled, in the other surface, where nobody was looking.
    for (const label of [...QUICK_LABELS, ...RETIRED_LABELS]) {
      expect(LABEL_COLOR_MAP[label.color], label.id).toBeDefined();
    }
  });
});

// Retiring a label leaves its id on documents and its emoji on terminal marks
// already saved. Reusing either would relabel them into something the user
// never said, so a retired identity is retired for good — RETIRED_LABELS is the
// list, and it only ever grows.
describe('retired labels', () => {
  it('never come back on a different label', () => {
    const liveIds = new Set(QUICK_LABELS.map((label) => label.id));
    const liveEmoji = new Set(QUICK_LABELS.map((label) => label.emoji));

    expect(RETIRED_LABELS.filter((label) => liveIds.has(label.id))).toEqual([]);
    expect(RETIRED_LABELS.filter((label) => liveEmoji.has(label.emoji))).toEqual([]);
  });

  it('covers everything the two sets used to offer', () => {
    const known = new Set([...QUICK_LABELS, ...RETIRED_LABELS].map((label) => label.id));
    const shipped = [
      'clarify-this',
      'missing-overview',
      'verify-this',
      'give-me-an-example',
      'match-existing-patterns',
      'consider-alternatives',
      'ensure-no-regression',
      'out-of-scope',
      'needs-tests',
      'nice-approach',
      'thumbs-up',
    ];

    expect(shipped.filter((id) => !known.has(id))).toEqual([]);
  });

  it('still resolve by id, so an old mark renders as what it said', () => {
    // A withdrawn label that stops resolving does not disappear from a
    // document — it renders as the raw id, which is the user's own feedback
    // turned into debug output.
    expect(labelById('needs-tests')?.text).toBe('Needs tests');
    expect(labelById('exactly-this')?.text).toBe('Exactly this');
    expect(labelById('never-existed')).toBeUndefined();
  });

  it('still resolve by emoji, which is all a terminal mark stores', () => {
    // A terminal mark persists nothing but the emoji, and drafts outlive the
    // upgrade that retired a label. Without this, a mark saved as 🧪 comes back
    // headed "🧪 Comment" with its instruction dropped — the user's own label
    // turned into an unlabelled one.
    expect(labelByEmoji('🧪')?.text).toBe('Needs tests');
    expect(labelByEmoji('🚫')?.text).toBe('Out of scope');
    expect(labelByEmoji('💯')?.id).toBe('exactly-this');
    expect(labelByEmoji('🦄')).toBeUndefined();
  });

  it('answer the one ambiguous emoji with the label the picker offered', () => {
    // 'nice-approach' and 'thumbs-up' both wore 👍. It was never a terminal
    // emoji, so nothing on disk depends on the answer — but the lookup must
    // still give one rather than depend on array order by accident.
    expect(labelByEmoji('👍')?.id).toBe('nice-approach');
  });
});
