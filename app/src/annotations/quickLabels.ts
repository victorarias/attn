// The one label set. Both annotation surfaces draw from it: marks on an
// agent's terminal output (`components/TerminalAnnotations`) and marks on a
// document in the Markdown reader (`components/MarkdownReader/annotations`).
//
// They were two sets, and the difference was an accident of where each came
// from rather than a claim that a plan deserves different vocabulary than a
// message. One set means a label learned in one surface means the same thing in
// the other, and a new label lands in both at once.
//
// A label is a one-click way to say a thing users otherwise retype every turn.
// Its `tip` is the instruction the agent actually receives; the `text` is what
// the user sees. Keeping the two separate is what lets the chip stay short
// ("Verify this") while the agent gets the sentence that makes it actionable.

export interface QuickLabel {
  id: string;
  emoji: string;
  text: string;
  // Key into LABEL_COLOR_MAP. Only the Markdown reader draws colored chips; the
  // terminal row is emoji-only. It is required anyway so a label added for one
  // surface is never half-dressed in the other.
  color: string;
  // The instruction sent to the agent. Absent when the label's own name says
  // everything ("Clarify this").
  tip?: string;
}

/** Inline color values per label (light/dark text pair). */
export const LABEL_COLOR_MAP: Record<string, { bg: string; text: string; darkText: string }> = {
  blue: { bg: 'rgba(59,130,246,0.15)', text: '#2563eb', darkText: '#60a5fa' },
  red: { bg: 'rgba(239,68,68,0.15)', text: '#dc2626', darkText: '#f87171' },
  orange: { bg: 'rgba(249,115,22,0.15)', text: '#ea580c', darkText: '#fb923c' },
  yellow: { bg: 'rgba(234,179,8,0.15)', text: '#ca8a04', darkText: '#facc15' },
  purple: { bg: 'rgba(147,51,234,0.15)', text: '#9333ea', darkText: '#a78bfa' },
  teal: { bg: 'rgba(20,184,166,0.15)', text: '#0d9488', darkText: '#2dd4bf' },
  pink: { bg: 'rgba(236,72,153,0.15)', text: '#db2777', darkText: '#f472b6' },
  green: { bg: 'rgba(34,197,94,0.15)', text: '#16a34a', darkText: '#4ade80' },
  cyan: { bg: 'rgba(8,145,178,0.15)', text: '#0891b2', darkText: '#22d3ee' },
  amber: { bg: 'rgba(180,83,9,0.15)', text: '#b45309', darkText: '#fbbf24' },
};

// The label row, in the groups it is drawn in. A group is what a divider
// separates, so the grouping lives here as data rather than as an index the
// popup counts to — reordering a label cannot silently move the divider. The
// Markdown reader draws the same labels as one vertical list and ignores the
// grouping.
//
// Every label here reacts to a *claim in a span of text*, because that is what
// a highlight selects — in a message or in a plan alike. Labels about the work
// rather than the words ("needs tests", "out of scope") had nothing to attach
// to and were never used; they are in RETIRED_LABELS.
//
// An id and an emoji are both identities: a terminal mark stores the emoji and
// resolves its payload from it, a document mark stores the id. Neither is ever
// reused for a different label — that would silently relabel marks already on
// disk into something the user never said. Retire, never recycle.
export const QUICK_LABEL_GROUPS: QuickLabel[][] = [
  [
    // Its tip is the longest here on purpose: "good" alone tells an agent
    // nothing to do, and the point of marking a thing right is that it
    // survives the next revision.
    {
      id: 'exactly-this',
      emoji: '💯',
      text: 'Exactly this',
      color: 'green',
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
      color: 'red',
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
      color: 'orange',
      tip: 'Something here is off. Do not defend it — rework this part. If you cannot see what is wrong with it, say what you think I am reacting to and ask.',
    },
  ],
  [
    { id: 'clarify-this', emoji: '❓', text: 'Clarify this', color: 'yellow' },
    {
      id: 'verify-this',
      emoji: '🔍',
      text: 'Verify this',
      color: 'blue',
      tip: 'This seems like an assumption. Verify by reading the actual code before proceeding.',
    },
    // Verify is "go and check". This is "you already claimed it — show your
    // work". It catches the failure the others cannot: confident prose written
    // over a guess, which reads exactly like prose written over a measurement.
    {
      id: 'show-the-receipt',
      emoji: '🧾',
      text: 'Show the receipt',
      color: 'teal',
      tip: 'You asserted this as fact. Name what backs it — the file and line, the measurement, the command output — or say plainly that it is an assumption.',
    },
    {
      id: 'give-me-an-example',
      emoji: '🔬',
      text: 'Give me an example',
      color: 'cyan',
      tip: 'This is too abstract. Show a before/after, a sample input/output, or a specific scenario.',
    },
    {
      id: 'consider-alternatives',
      emoji: '🔄',
      text: 'Consider alternatives',
      color: 'pink',
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
      color: 'amber',
      tip: 'This is padding. Say what it says in a fraction of the words, or drop it — do not restate it more carefully.',
    },
    // Aimed at the proposal.
    {
      id: 'simplify-this',
      emoji: '🪓',
      text: 'Simplify this',
      color: 'purple',
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
      color: 'green',
      tip: 'You did not need me for this. Decide it yourself and keep going — ask only when the choice is genuinely mine to make.',
    },
    {
      id: 'ask-me-first',
      emoji: '🙋',
      text: 'Ask me first',
      color: 'yellow',
      tip: 'You should have asked before deciding this. Stop and ask rather than picking for me.',
    },
  ],
];

// Every label, in row order. What a payload resolves against; the grouping
// above is only how the terminal row is drawn.
export const QUICK_LABELS: QuickLabel[] = QUICK_LABEL_GROUPS.flat();

// Labels no longer offered, kept so marks that already carry them still render
// as what the user said rather than as a raw id. Nothing adds to this except
// retirement, and nothing is ever removed from it.
//
// The first five were the Markdown reader's own: they are about the work rather
// than the words, and a highlight has nothing to attach them to. 'nice-approach'
// and 'thumbs-up' are what 💯 Exactly this replaced — same act, and now one
// label with an instruction behind it instead of two without.
export const RETIRED_LABELS: QuickLabel[] = [
  { id: 'missing-overview', emoji: '🗺️', text: 'Missing overview', color: 'purple' },
  { id: 'match-existing-patterns', emoji: '🧬', text: 'Match existing patterns', color: 'teal' },
  { id: 'ensure-no-regression', emoji: '📉', text: 'Ensure no regression', color: 'amber' },
  { id: 'out-of-scope', emoji: '🚫', text: 'Out of scope', color: 'red' },
  { id: 'needs-tests', emoji: '🧪', text: 'Needs tests', color: 'blue' },
  { id: 'nice-approach', emoji: '👍', text: 'Nice approach', color: 'green' },
  { id: 'thumbs-up', emoji: '👍', text: 'Looks good', color: 'green' },
];

/** Resolves a live label by the emoji a terminal mark carries. */
export function labelByEmoji(emoji: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.emoji === emoji);
}

/**
 * Resolves a label by the id a document mark carries, retired ones included —
 * this is a display lookup, and an old mark deserves its name back.
 */
export function labelById(id: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.id === id)
    ?? RETIRED_LABELS.find((label) => label.id === id);
}
