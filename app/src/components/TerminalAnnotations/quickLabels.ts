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
  // everything ("Clarify this").
  tip?: string;
}

// The label row, in the groups it is drawn in. A group is what a divider
// separates, so the grouping lives here as data rather than as an index the
// popup counts to — reordering a label cannot silently move the divider.
//
// Every label here reacts to a *claim in a sentence*, because that is what a
// highlight selects. Labels about the work rather than the words — "needs
// tests", "out of scope", "match existing patterns" — belong on a plan, which
// is why they live in the markdown reader's set and not this one. On a message
// they had nothing to attach to and were never used.
//
// An emoji is an identity, not decoration: it is what a stored annotation
// carries and what `labelByEmoji` resolves the payload from. A retired emoji
// stays retired — reusing one would silently relabel marks already on disk into
// something the user never said.
export const QUICK_LABEL_GROUPS: QuickLabel[][] = [
  [
    // Its tip is the longest here on purpose: "good" alone tells an agent
    // nothing to do, and the point of marking a thing right is that it
    // survives the next revision.
    {
      id: 'exactly-this',
      emoji: '💯',
      text: 'Exactly this',
      tip: 'This is right and it matters. Preserve this decision and the reasoning behind it — do not revisit or trade it away, and apply the same reasoning where it belongs elsewhere.',
    },
  ],
  [
    // The other end of the same axis. It has to travel the way agreement does:
    // a wrong claim is rarely wrong only where it was written.
    {
      id: 'this-is-wrong',
      emoji: '❌',
      text: 'This is wrong',
      tip: 'This is wrong. Correct it before going further, and check what else you built on it — anything downstream of this claim is suspect too.',
    },
    // Doubt with nothing articulate behind it yet. Without this the only way to
    // say it is to argue a case the user has not made, so it arrives as silence
    // or as a rewrite. "Do not defend it" is the load-bearing half of the tip:
    // the reflex it exists to stop is a rebuttal.
    {
      id: 'dont-love-this',
      emoji: '😕',
      text: "I don't love this",
      tip: 'Something here is off. Do not defend it — rework this part. If you cannot see what is wrong with it, say what you think I am reacting to and ask.',
    },
  ],
  [
    { id: 'clarify-this', emoji: '❓', text: 'Clarify this' },
    {
      id: 'verify-this',
      emoji: '🔍',
      text: 'Verify this',
      tip: 'This seems like an assumption. Verify by reading the actual code before proceeding.',
    },
    // Verify is "go and check". This is "you already claimed it — show your
    // work". It catches the failure the others cannot: confident prose written
    // over a guess, which reads exactly like prose written over a measurement.
    {
      id: 'show-the-receipt',
      emoji: '🧾',
      text: 'Show the receipt',
      tip: 'You asserted this as fact. Name what backs it — the file and line, the measurement, the command output — or say plainly that it is an assumption.',
    },
    {
      id: 'give-me-an-example',
      emoji: '🔬',
      text: 'Give me an example',
      tip: 'This is too abstract. Show a before/after, a sample input/output, or a specific scenario.',
    },
    {
      id: 'consider-alternatives',
      emoji: '🔄',
      text: 'Consider alternatives',
      tip: 'Propose 2-3 alternative approaches with trade-offs based on the actual codebase.',
    },
  ],
  [
    // Aimed at the writing. "Be concise" as a standing instruction teaches
    // nothing; a mark on the paragraph that was padding teaches the register.
    {
      id: 'cut-this',
      emoji: '✂️',
      text: 'Cut this',
      tip: 'This is padding. Say what it says in a fraction of the words, or drop it — do not restate it more carefully.',
    },
    // Aimed at the proposal.
    {
      id: 'simplify-this',
      emoji: '🪓',
      text: 'Simplify this',
      tip: 'This is more machinery than the problem needs. Find the version with fewer moving parts, even if that means throwing this away.',
    },
  ],
  [
    // The two halves of getting the asking wrong, marked on the question or the
    // decision itself rather than delivered as a note about behaviour.
    {
      id: 'your-call',
      emoji: '🪙',
      text: 'Your call',
      tip: 'You did not need me for this. Decide it yourself and keep going — ask only when the choice is genuinely mine to make.',
    },
    {
      id: 'ask-me-first',
      emoji: '🙋',
      text: 'Ask me first',
      tip: 'You should have asked before deciding this. Stop and ask rather than picking for me.',
    },
  ],
];

// Every label, in row order. What the payload resolves against; the grouping
// above is only how the row is drawn.
export const QUICK_LABELS: QuickLabel[] = QUICK_LABEL_GROUPS.flat();

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
