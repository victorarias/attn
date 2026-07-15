/**
 * Quick labels — the fixed v1 preset set for one-click annotation feedback.
 * Ported from plannotator's DEFAULT_QUICK_LABELS (packages/ui/utils/quickLabels.ts)
 * with the plannotator-specific tip rewritten; no user configuration in v1.
 *
 * Annotations reference labels structurally (`quickLabelId` + a snapshotted
 * `quickLabelTip`), never by baking "emoji text" into the comment text.
 * Display resolves the id against this set; an unknown id renders the raw id
 * (forward compat with future label-set changes).
 */

export interface QuickLabel {
  id: string; // kebab-case identifier e.g. "needs-tests"
  emoji: string;
  text: string;
  color: string; // key into LABEL_COLOR_MAP
  /** Optional instruction injected into the PR6 payload for the agent. */
  tip?: string;
}

/** Inline color values per label (donor rgba values, light/dark text pair). */
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

export const QUICK_LABELS: QuickLabel[] = [
  { id: 'clarify-this', emoji: '❓', text: 'Clarify this', color: 'yellow' },
  {
    id: 'missing-overview',
    emoji: '🗺️',
    text: 'Missing overview',
    color: 'purple',
    tip: 'Provide a narrative overview of what is being built, why it is being built, and how it will be built. Add this before the implementation details.',
  },
  {
    id: 'verify-this',
    emoji: '🔍',
    text: 'Verify this',
    color: 'orange',
    tip: 'This seems like an assumption. Verify by reading the actual code before proceeding.',
  },
  {
    id: 'give-me-an-example',
    emoji: '🔬',
    text: 'Give me an example',
    color: 'cyan',
    tip: 'This is too abstract. Show a before/after, a sample input/output, or a specific scenario so I can see how this actually works.',
  },
  {
    id: 'match-existing-patterns',
    emoji: '🧬',
    text: 'Match existing patterns',
    color: 'teal',
    tip: 'Search the codebase for existing patterns, components, or utilities that already solve this. Reuse what exists rather than introducing a new approach.',
  },
  {
    id: 'consider-alternatives',
    emoji: '🔄',
    text: 'Consider alternatives',
    color: 'pink',
    tip: 'Propose 2-3 alternative approaches with trade-offs based on the actual codebase. Check earlier plan or design documents in this repository for approaches that were already explored or rejected.',
  },
  {
    id: 'ensure-no-regression',
    emoji: '📉',
    text: 'Ensure no regression',
    color: 'amber',
    tip: 'Verify that this change will not break existing behavior. Identify what could regress and how to protect against it.',
  },
  {
    id: 'out-of-scope',
    emoji: '🚫',
    text: 'Out of scope',
    color: 'red',
    tip: 'This is not part of the current task. Remove it and stay focused on what was actually requested.',
  },
  { id: 'needs-tests', emoji: '🧪', text: 'Needs tests', color: 'blue' },
  { id: 'nice-approach', emoji: '👍', text: 'Nice approach', color: 'green' },
];

/** The fixed toolbar 👍 button (separate from the 'nice-approach' list label). */
export const THUMBS_UP_LABEL: QuickLabel = {
  id: 'thumbs-up',
  emoji: '👍',
  text: 'Looks good',
  color: 'green',
};

export function quickLabelById(id: string): QuickLabel | undefined {
  if (id === THUMBS_UP_LABEL.id) {
    return THUMBS_UP_LABEL;
  }
  return QUICK_LABELS.find((label) => label.id === id);
}
