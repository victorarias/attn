// app/src/components/PresentRoot/DriveBar.tsx
// Fixed bottom strip for the Present reader: review-progress on the left,
// keyboard hints in the middle, and the "Submit review" action on the right.
// Jaunt-style "drive bar" — always visible, independent of scroll position.
import './DriveBar.css';

export interface DriveBarProps {
  reviewedCount: number;
  totalCount: number;
  draftCount: number;
  submitting: boolean;
  onSubmit: () => void;
}

export function DriveBar({ reviewedCount, totalCount, draftCount, submitting, onSubmit }: DriveBarProps) {
  const pct = totalCount > 0 ? Math.round((reviewedCount / totalCount) * 100) : 0;

  return (
    <div className="present-drive-bar" data-testid="present-drive-bar">
      <div className="present-drive-bar-progress">
        <div className="present-drive-bar-progress-track">
          <div className="present-drive-bar-progress-fill" style={{ width: `${pct}%` }} />
        </div>
        <span className="present-drive-bar-progress-label">
          {reviewedCount} of {totalCount} reviewed
        </span>
      </div>

      <div className="present-drive-bar-hints">
        <span>
          <kbd>J</kbd>/<kbd>K</kbd> next/prev
        </span>
        <span>
          <kbd>R</kbd> reviewed
        </span>
      </div>

      <button
        type="button"
        className="present-drive-bar-submit"
        onClick={onSubmit}
        disabled={submitting}
      >
        {draftCount > 0 ? `Submit review (${draftCount})` : 'Submit review'}
        <kbd>S</kbd>
      </button>
    </div>
  );
}

export default DriveBar;
