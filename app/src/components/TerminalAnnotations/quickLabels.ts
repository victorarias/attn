// The labels a highlight can carry, and how a set of annotations becomes the
// text typed back into the session.
//
// A label is a one-click way to say a thing users otherwise retype every turn.
// Its `tip` is the instruction the agent actually receives; the `text` is what
// the user sees. Keeping the two separate is what lets the chip stay short
// ("Verify this") while the agent gets the sentence that makes it actionable.

export interface QuickLabel {
  id: string;
  emoji: string;
  text: string;
  // The instruction sent to the agent. Absent when the label's own name says
  // everything ("Clarify this", "Needs tests").
  tip?: string;
}

export const QUICK_LABELS: QuickLabel[] = [
  { id: 'clarify-this', emoji: '❓', text: 'Clarify this' },
  {
    id: 'verify-this',
    emoji: '🔍',
    text: 'Verify this',
    tip: 'This seems like an assumption. Verify by reading the actual code before proceeding.',
  },
  {
    id: 'give-me-an-example',
    emoji: '🔬',
    text: 'Give me an example',
    tip: 'This is too abstract. Show a before/after, a sample input/output, or a specific scenario.',
  },
  {
    id: 'match-existing-patterns',
    emoji: '🧬',
    text: 'Match existing patterns',
    tip: 'Search the codebase for existing patterns that already solve this. Reuse what exists.',
  },
  {
    id: 'consider-alternatives',
    emoji: '🔄',
    text: 'Consider alternatives',
    tip: 'Propose 2-3 alternative approaches with trade-offs based on the actual codebase.',
  },
  {
    id: 'ensure-no-regression',
    emoji: '📉',
    text: 'Ensure no regression',
    tip: 'Verify this will not break existing behavior. Identify what could regress.',
  },
  {
    id: 'out-of-scope',
    emoji: '🚫',
    text: 'Out of scope',
    tip: 'This is not part of the current task. Remove it and stay focused on what was requested.',
  },
  { id: 'needs-tests', emoji: '🧪', text: 'Needs tests' },
];

export function labelByEmoji(emoji: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.emoji === emoji);
}

export interface PayloadAnnotation {
  quote: string;
  emoji: string;
  comment: string;
  // Offset of the annotation's start in the message, used only to order the
  // payload the way the user reads the message.
  start: number;
}

// Composes the message typed back into the session.
//
// The quote is what makes an annotation legible to the agent: it has no access
// to what the user highlighted, only to what it wrote, so every item leads with
// the exact words it is about. Items are ordered by position in the message
// rather than by when they were made, because the agent reads the payload as a
// pass over its own answer.
export function buildAnnotationPayload(annotations: readonly PayloadAnnotation[]): string {
  if (annotations.length === 0) return '';
  const ordered = [...annotations].sort((a, b) => a.start - b.start);
  const lines: string[] = ['Feedback on your last message. Address each annotation.', ''];
  ordered.forEach((annotation, index) => {
    const label = annotation.emoji ? labelByEmoji(annotation.emoji) : undefined;
    const heading = label
      ? `${label.emoji} ${label.text}`
      : `${annotation.emoji || '💬'} Comment`;
    lines.push(`## ${index + 1}. ${heading}`, '');
    lines.push(`> ${annotation.quote.split('\n').join('\n> ')}`, '');
    if (label?.tip) lines.push(label.tip);
    if (annotation.comment) lines.push(annotation.comment);
    lines.push('');
  });
  return lines.join('\n').trimEnd();
}
