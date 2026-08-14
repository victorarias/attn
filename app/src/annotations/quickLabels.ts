// The labels offered by both annotation surfaces: marks on an agent's terminal
// output (`components/TerminalAnnotations`) and marks on a document in the
// Markdown reader (`components/MarkdownReader/annotations`).

export interface QuickLabel {
  id: string;
  emoji: string;
  text: string;
  // Key into LABEL_COLOR_MAP.
  color: string;
  // Sent to the agent under the label.
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

// The label row. A group is what both pickers draw a divider between.
export const QUICK_LABEL_GROUPS: QuickLabel[][] = [
  [
    { id: 'i-agree', emoji: '👍', text: 'I agree', color: 'green' },
    { id: 'exactly-this', emoji: '💯', text: 'Exactly this', color: 'green' },
  ],
  [
    { id: 'this-is-wrong', emoji: '❌', text: 'This is wrong', color: 'red' },
    { id: 'dont-love-this', emoji: '😕', text: "I don't love this", color: 'orange' },
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
    { id: 'cut-this', emoji: '🪓', text: 'Cut this', color: 'amber' },
  ],
  [
    { id: 'your-call', emoji: '🪙', text: 'Your call', color: 'green' },
    { id: 'ask-me-first', emoji: '🙋', text: 'Ask me first', color: 'yellow' },
  ],
];

// Every label, in row order.
export const QUICK_LABELS: QuickLabel[] = QUICK_LABEL_GROUPS.flat();

export function labelByEmoji(emoji: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.emoji === emoji);
}

export function labelById(id: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.id === id);
}
