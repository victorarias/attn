// The labels a terminal highlight can carry, and how a set of annotations
// becomes the text typed back into the session.
//
// The set itself is shared with the Markdown reader — see
// `src/annotations/quickLabels.ts`. This module owns only the payload.

export {
  QUICK_LABEL_GROUPS,
  QUICK_LABELS,
  labelByEmoji,
  type QuickLabel,
} from '../../annotations/quickLabels';

import { labelByEmoji } from '../../annotations/quickLabels';

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
//
// The note goes first, before the marks. It is what the user wants done —
// "let's do x and y" — and the marks are the detail that qualifies it. Sending
// it after them would make it read as an afterthought, which is exactly the
// ordering that has people writing "…but also consider what I put below".
export function buildAnnotationPayload(
  annotations: readonly PayloadAnnotation[],
  note = '',
): string {
  if (annotations.length === 0) return '';
  const ordered = [...annotations].sort((a, b) => a.start - b.start);
  const lines: string[] = ['Feedback on your last message.', ''];
  const trimmedNote = note.trim();
  if (trimmedNote) lines.push(trimmedNote, '');
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
