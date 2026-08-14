// The shared label set (`src/annotations/quickLabels.ts`), adapted to what the
// Markdown reader needs. A document mark stores `quickLabelId` and the tip it
// was made with, never the label's text baked into the comment.

import { labelById, type QuickLabel } from '../../../annotations/quickLabels';

export {
  LABEL_COLOR_MAP,
  PROMOTED_LABELS,
  QUICK_LABEL_PICKER_GROUPS,
  QUICK_LABEL_PICKER_LABELS,
  QUICK_LABELS,
  type QuickLabel,
} from '../../../annotations/quickLabels';

export function quickLabelById(id: string): QuickLabel | undefined {
  return labelById(id);
}
