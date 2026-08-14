// The shared label set (`src/annotations/quickLabels.ts`), adapted to what the
// Markdown reader needs. A document mark stores `quickLabelId` and the tip it
// was made with, never the label's text baked into the comment.

import {
  QUICK_LABEL_GROUPS,
  QUICK_LABELS,
  labelById,
  type QuickLabel,
} from '../../../annotations/quickLabels';

export { LABEL_COLOR_MAP, QUICK_LABELS, type QuickLabel } from '../../../annotations/quickLabels';

const PROMOTED_LABEL_IDS = ['i-agree', 'this-is-wrong', 'clarify-this'] as const;

function requireQuickLabel(id: string): QuickLabel {
  const label = labelById(id);
  if (!label) {
    throw new Error(
      `A promoted toolbar button points at quick label "${id}", which the shared set no longer offers. `
      + `Point it at one of: ${QUICK_LABELS.map((candidate) => candidate.id).join(', ')}.`,
    );
  }
  return label;
}

export const PROMOTED_LABELS: readonly QuickLabel[] = PROMOTED_LABEL_IDS.map(requireQuickLabel);

const promotedLabelIds = new Set<string>(PROMOTED_LABEL_IDS);

export const QUICK_LABEL_PICKER_GROUPS: readonly (readonly QuickLabel[])[] = QUICK_LABEL_GROUPS
  .map((group) => group.filter((label) => !promotedLabelIds.has(label.id)))
  .filter((group) => group.length > 0);

export const QUICK_LABEL_PICKER_LABELS: readonly QuickLabel[] = QUICK_LABEL_PICKER_GROUPS.flat();

export function quickLabelById(id: string): QuickLabel | undefined {
  return labelById(id);
}
