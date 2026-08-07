/**
 * Quick labels for document annotations.
 *
 * The set is shared with terminal annotations — see
 * `src/annotations/quickLabels.ts`. A label means the same thing whether it
 * lands on a plan or on an agent's message, so there is one set and this module
 * only adapts it to what the reader needs.
 *
 * Annotations reference labels structurally (`quickLabelId` + a snapshotted
 * `quickLabelTip`), never by baking "emoji text" into the comment text. Display
 * resolves the id through `quickLabelById`, which knows retired labels too, so
 * a mark made before a label was withdrawn still renders as what it said.
 */

import { QUICK_LABELS, labelById, type QuickLabel } from '../../../annotations/quickLabels';

export { LABEL_COLOR_MAP, QUICK_LABELS, type QuickLabel } from '../../../annotations/quickLabels';

/**
 * The fixed toolbar 👍 button. It applies the same "this is right" label the
 * picker offers — a one-click agreement that sent a different, instruction-less
 * label than the list's was the same act saying two different things.
 */
export const THUMBS_UP_LABEL: QuickLabel = QUICK_LABELS.find(
  (label) => label.id === 'exactly-this',
)!;

export function quickLabelById(id: string): QuickLabel | undefined {
  return labelById(id);
}
