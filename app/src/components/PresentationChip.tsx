import './PresentationChip.css';
import type { Presentation } from '../types/generated';

// The pane-header indicator for a presentation triggered by this session:
// an inline chip in .workspace-pane-header (see NudgeIndicator's
// HeaderNudgeIndicator for the sibling idiom this mirrors), rendered whenever
// the session has an open, unsubmitted presentation. The whole chip opens the
// presentation window.
export function HeaderPresentationChip({
  presentation,
  onOpen,
}: {
  presentation: Presentation;
  onOpen: (presentationId: string) => void;
}) {
  return (
    <button
      type="button"
      className="presentation-chip"
      // Stop the pane header's pointerdown drag from starting on this button. In a
      // split the header is a leaf-drag handle (beginLeafDrag), so without this a
      // sloppy click that drifts >=4px would relocate the pane instead of opening
      // the presentation — exactly as the sibling nudge/rename buttons guard
      // themselves.
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onOpen(presentation.id);
      }}
      title={presentation.title}
    >
      <span className="presentation-chip-dot" aria-hidden="true" />
      <span className="presentation-chip-label">▶ review</span>
    </button>
  );
}
