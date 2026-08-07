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
const AGREEMENT_LABEL_ID = 'exactly-this';

export const THUMBS_UP_LABEL: QuickLabel = (() => {
  const label = QUICK_LABELS.find((candidate) => candidate.id === AGREEMENT_LABEL_ID);
  if (!label) {
    // Retiring this id without repointing the button would otherwise leave an
    // undefined here and break the toolbar on render, a long way from the edit
    // that caused it.
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
