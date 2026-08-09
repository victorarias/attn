// The shared label set (`src/annotations/quickLabels.ts`), adapted to what the
// Markdown reader needs. A document mark stores `quickLabelId` and the tip it
// was made with, never the label's text baked into the comment.

import { QUICK_LABELS, labelById, type QuickLabel } from '../../../annotations/quickLabels';

export { LABEL_COLOR_MAP, QUICK_LABELS, type QuickLabel } from '../../../annotations/quickLabels';

// What the toolbar's fixed 👍 button applies.
const AGREEMENT_LABEL_ID = 'i-agree';

export const THUMBS_UP_LABEL: QuickLabel = (() => {
  const label = QUICK_LABELS.find((candidate) => candidate.id === AGREEMENT_LABEL_ID);
  if (!label) {
    throw new Error(
      `The one-click agreement button points at quick label "${AGREEMENT_LABEL_ID}", which the shared set no longer offers. `
      + `Point it at one of: ${QUICK_LABELS.map((candidate) => candidate.id).join(', ')}.`,
    );
  }
  return label;
})();

export function quickLabelById(id: string): QuickLabel | undefined {
  return labelById(id);
}
