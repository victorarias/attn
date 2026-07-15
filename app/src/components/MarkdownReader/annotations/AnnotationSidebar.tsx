/**
 * AnnotationSidebar — the right rail inside the markdown tile listing every
 * annotation in document order.
 *
 * Contract (spec E18–E22):
 * - cards sorted by document position (anchor.startLine, then anchor.start);
 *   globals last, by createdAt — lines are re-baselined fresh so sorting on
 *   them is exact;
 * - card click focuses: highlight glows + scrolls to center (engine skips
 *   the scroll for just-created ids); orphan cards never scroll (nothing is
 *   painted);
 * - orphan cards show an "⚠ moved" badge, the quote, and `~line N (moved)`
 *   with the last-known startLine; still deletable;
 * - hover-reveal delete per card (delete is the undo — donor parity);
 * - header: count pill (collapse toggle), "+ Global comment", "Clear all"
 *   with an inline two-step confirm wired to the tombstoning clear.
 */

import { useEffect, useRef, useState } from 'react';
import type { AnnotationOrphanReason } from './useAnnotations';
import { LABEL_COLOR_MAP, quickLabelById } from './quickLabels';
import type { Annotation } from './types';

export interface AnnotationSidebarProps {
  annotations: Annotation[];
  orphans: Map<string, AnnotationOrphanReason>;
  selectedId: string | null;
  onCardClick: (id: string) => void;
  onDelete: (id: string) => void;
  onClearAll: () => void;
  /** Opens the global-comment popover anchored to the clicked button. */
  onGlobalComment: (anchorEl: HTMLElement) => void;
  /** Collapses the sidebar (count-pill toggle). */
  onToggle: () => void;
}

/** Document-position sort: startLine, then start; globals last by createdAt. */
export function sortAnnotations(annotations: Annotation[]): Annotation[] {
  return [...annotations].sort((a, b) => {
    if (!a.anchor && !b.anchor) {
      return a.createdAt - b.createdAt;
    }
    if (!a.anchor) {
      return 1;
    }
    if (!b.anchor) {
      return -1;
    }
    if (a.anchor.startLine !== b.anchor.startLine) {
      return a.anchor.startLine - b.anchor.startLine;
    }
    return a.anchor.start - b.anchor.start;
  });
}

function CardBadge({ annotation }: { annotation: Annotation }) {
  if (annotation.quickLabelId) {
    const label = quickLabelById(annotation.quickLabelId);
    if (!label) {
      // Unknown id (forward compat): render the raw id.
      return <span className="md-card-badge md-card-badge--comment">{annotation.quickLabelId}</span>;
    }
    const color = LABEL_COLOR_MAP[label.color];
    return (
      <span
        className="md-card-badge md-ql-chip"
        style={
          color
            ? ({
                background: color.bg,
                '--md-ql-text': color.text,
                '--md-ql-text-dark': color.darkText,
              } as React.CSSProperties)
            : undefined
        }
      >
        {label.emoji} {label.text}
      </span>
    );
  }
  return (
    <span className={`md-card-badge md-card-badge--${annotation.type}`}>{annotation.type}</span>
  );
}

export function AnnotationSidebar({
  annotations,
  orphans,
  selectedId,
  onCardClick,
  onDelete,
  onClearAll,
  onGlobalComment,
  onToggle,
}: AnnotationSidebarProps) {
  const sorted = sortAnnotations(annotations);
  const [confirmingClear, setConfirmingClear] = useState(false);
  const confirmTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // The inline confirm re-arms after a beat if not confirmed.
  useEffect(() => {
    return () => {
      if (confirmTimerRef.current) {
        clearTimeout(confirmTimerRef.current);
      }
    };
  }, []);

  const handleClearClick = () => {
    if (confirmingClear) {
      if (confirmTimerRef.current) {
        clearTimeout(confirmTimerRef.current);
        confirmTimerRef.current = null;
      }
      setConfirmingClear(false);
      onClearAll();
      return;
    }
    setConfirmingClear(true);
    confirmTimerRef.current = setTimeout(() => {
      confirmTimerRef.current = null;
      setConfirmingClear(false);
    }, 3000);
  };

  return (
    <aside className="md-annotations-sidebar">
      <div className="md-sidebar-header">
        <span className="md-sidebar-title">Annotations</span>
        <button
          type="button"
          className="md-sidebar-count"
          title="Collapse annotations sidebar"
          onClick={onToggle}
        >
          {annotations.length}
        </button>
        <span className="md-sidebar-spacer" />
        <button
          type="button"
          className="md-sidebar-action"
          title="Add a document-wide comment"
          onClick={(e) => onGlobalComment(e.currentTarget)}
        >
          + Global comment
        </button>
        {annotations.length > 0 && (
          <button
            type="button"
            className={`md-sidebar-action md-sidebar-clear ${confirmingClear ? 'md-sidebar-clear--confirming' : ''}`.trim()}
            onClick={handleClearClick}
          >
            {confirmingClear ? 'Confirm?' : 'Clear all'}
          </button>
        )}
      </div>
      <div className="md-sidebar-list">
        {sorted.length === 0 ? (
          <div className="md-sidebar-empty">
            <p>No annotations yet.</p>
            <p>Select text in the document to comment, redline, or quick-label it.</p>
          </div>
        ) : (
          sorted.map((annotation) => {
            const orphaned = orphans.has(annotation.id);
            return (
              <div
                key={annotation.id}
                className={[
                  'md-annotation-card',
                  selectedId === annotation.id ? 'md-annotation-card--selected' : '',
                  orphaned ? 'md-annotation-card--orphan' : '',
                ]
                  .filter(Boolean)
                  .join(' ')}
                onClick={() => onCardClick(annotation.id)}
              >
                <div className="md-card-top">
                  <CardBadge annotation={annotation} />
                  {orphaned && <span className="md-card-orphan-badge">⚠ moved</span>}
                  <button
                    type="button"
                    className="md-card-delete"
                    title="Remove annotation"
                    onClick={(e) => {
                      e.stopPropagation();
                      onDelete(annotation.id);
                    }}
                  >
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
                {annotation.anchor && (
                  <div className="md-card-quote">"{annotation.anchor.exact}"</div>
                )}
                {orphaned && annotation.anchor && (
                  <div className="md-card-orphan-line">~line {annotation.anchor.startLine} (moved)</div>
                )}
                {annotation.text && <div className="md-card-text">{annotation.text}</div>}
              </div>
            );
          })
        )}
      </div>
    </aside>
  );
}
