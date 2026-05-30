import { useCallback, useEffect, useRef, useState } from 'react';
import './WorktreeCleanupPrompt.css';

export type SessionCreationPhase = 'creating_worktree' | 'starting_session';

interface SessionCreationProgressProps {
  isVisible: boolean;
  label: string;
  path: string;
  phase: SessionCreationPhase;
  error?: string | null;
  onDismiss?: () => void;
}

export function SessionCreationProgress({
  isVisible,
  label,
  path,
  phase,
  error = null,
  onDismiss,
}: SessionCreationProgressProps) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  const compactRef = useRef<HTMLButtonElement>(null);
  const compactTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(false);
  const hasCompactedRef = useRef(false);
  const [isCompact, setIsCompact] = useState(false);
  const hasError = Boolean(error);

  const clearCompactTimer = useCallback(() => {
    if (compactTimerRef.current !== null) {
      window.clearTimeout(compactTimerRef.current);
      compactTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      clearCompactTimer();
    };
  }, [clearCompactTimer]);

  const createMotionGhost = useCallback((source: HTMLElement, rect: DOMRect) => {
    const ghost = source.cloneNode(true) as HTMLElement;
    ghost.removeAttribute('id');
    ghost.querySelectorAll('[id]').forEach((node) => node.removeAttribute('id'));
    ghost.classList.remove('surface-hidden', 'motion-fade', 'motion-target', 'motion-underlay', 'motion-lit');
    ghost.classList.add('motion-ghost');
    ghost.setAttribute('aria-hidden', 'true');
    ghost.style.left = `${rect.left}px`;
    ghost.style.top = `${rect.top}px`;
    ghost.style.width = `${rect.width}px`;
    ghost.style.height = `${rect.height}px`;
    document.body.appendChild(ghost);
    return ghost;
  }, []);

  const finishMotion = useCallback((ghost: HTMLElement, callback: () => void) => {
    let done = false;
    const complete = (event?: TransitionEvent) => {
      if (event && event.propertyName !== 'transform') {
        return;
      }
      if (done) {
        return;
      }
      done = true;
      ghost.removeEventListener('transitionend', complete);
      callback();
      requestAnimationFrame(() => ghost.remove());
    };
    ghost.addEventListener('transitionend', complete);
    window.setTimeout(() => complete(), 360);
  }, []);

  const applyCompact = useCallback((next: boolean, focus = true) => {
    if (!mountedRef.current) {
      return;
    }
    setIsCompact(next);
    if (focus) {
      requestAnimationFrame(() => {
        if (next) {
          compactRef.current?.focus();
        } else {
          dialogRef.current?.focus();
        }
      });
    }
  }, []);

  const setCompactAnimated = useCallback((next: boolean, focus = true) => {
    if (next === isCompact) {
      applyCompact(next, focus);
      return;
    }

    const overlay = overlayRef.current;
    const dialog = dialogRef.current;
    const compact = compactRef.current;
    const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
    if (!overlay || !dialog || !compact || reduceMotion) {
      applyCompact(next, focus);
      return;
    }

    if (next) {
      const from = dialog.getBoundingClientRect();
      const to = compact.getBoundingClientRect();
      if (!from.width || !from.height || !to.width || !to.height) {
        applyCompact(true, focus);
        return;
      }
      const ghost = createMotionGhost(dialog, from);
      overlay.classList.add('motion-fade');
      ghost.getBoundingClientRect();
      ghost.style.transition = 'transform 280ms cubic-bezier(0.2, 0.8, 0.2, 1), opacity 220ms ease';
      ghost.style.transform = `translate(${to.left - from.left}px, ${to.top - from.top}px) scale(${to.width / from.width}, ${to.height / from.height})`;
      ghost.style.opacity = '0.08';
      finishMotion(ghost, () => {
        compact.classList.add('motion-target');
        applyCompact(true, focus);
        overlay.classList.remove('motion-fade');
        requestAnimationFrame(() => compact.classList.remove('motion-target'));
      });
      return;
    }

    const from = compact.getBoundingClientRect();
    const to = dialog.getBoundingClientRect();
    if (!from.width || !from.height || !to.width || !to.height) {
      applyCompact(false, focus);
      return;
    }
    const ghost = createMotionGhost(dialog, to);
    overlay.classList.add('motion-target', 'motion-underlay');
    overlay.classList.remove('surface-hidden');
    overlay.getBoundingClientRect();
    overlay.classList.add('motion-lit');
    compact.classList.add('motion-fade');
    ghost.style.transform = `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width}, ${from.height / to.height})`;
    ghost.style.opacity = '0.16';
    ghost.getBoundingClientRect();
    ghost.style.transition = 'transform 280ms cubic-bezier(0.2, 0.8, 0.2, 1), opacity 220ms ease';
    ghost.style.transform = 'translate(0, 0) scale(1, 1)';
    ghost.style.opacity = '1';
    finishMotion(ghost, () => {
      overlay.classList.remove('motion-underlay', 'motion-lit');
      applyCompact(false, focus);
      compact.classList.remove('motion-fade');
      requestAnimationFrame(() => overlay.classList.remove('motion-target'));
    });
  }, [applyCompact, createMotionGhost, finishMotion, isCompact]);

  useEffect(() => {
    if (!isVisible) {
      clearCompactTimer();
      hasCompactedRef.current = false;
      setIsCompact(false);
      return;
    }
    if (hasError) {
      clearCompactTimer();
      return;
    }
    if (hasCompactedRef.current || isCompact) {
      return;
    }
    hasCompactedRef.current = true;
    compactTimerRef.current = window.setTimeout(() => {
      setCompactAnimated(true, false);
    }, 280);
    return clearCompactTimer;
  }, [clearCompactTimer, hasError, isVisible, setCompactAnimated]);

  useEffect(() => {
    if (!isVisible || isCompact || !hasError) {
      return;
    }
    requestAnimationFrame(() => dialogRef.current?.focus());
  }, [hasError, isCompact, isVisible]);

  if (!isVisible) return null;

  const displayName = label || path.split('/').pop() || 'session';
  const phaseLabel = phase === 'creating_worktree' ? 'Creating worktree' : 'Starting session';
  const detail = phase === 'creating_worktree' ? 'git worktree add' : 'launching agent runtime';

  return (
    <>
      <div
        ref={overlayRef}
        className={`worktree-cleanup-prompt ${isCompact ? 'surface-hidden' : ''}`}
        role="presentation"
      >
        <div
          ref={dialogRef}
          className="cleanup-content"
          role="dialog"
          aria-modal="true"
          aria-labelledby="session-creation-title"
          aria-describedby="session-creation-message"
          tabIndex={-1}
        >
          <div className={`cleanup-status-pin ${hasError ? 'failed' : ''}`} aria-hidden="true" />
          <div className="cleanup-copy">
            <div className="cleanup-title">{hasError ? 'Action needed' : 'Session setup'}</div>
            <div className="cleanup-heading" id="session-creation-title">
              {hasError ? 'Session was not created' : phaseLabel}
            </div>
            <div className="cleanup-message" id="session-creation-message">
              <span className="cleanup-branch">{displayName}</span>
              <span className="cleanup-path">{path}</span>
            </div>
            <div className={`cleanup-operation ${hasError ? 'failed' : ''}`} role={hasError ? 'alert' : 'status'}>
              <span className="cleanup-operation-label">{hasError ? 'Create failed' : phaseLabel}</span>
              <span className="cleanup-operation-detail">{hasError ? error : detail}</span>
              {!hasError && <span className="cleanup-meter" aria-hidden="true"><span /></span>}
            </div>
          </div>
          {hasError && onDismiss && (
            <div className="cleanup-actions">
              <button type="button" className="cleanup-btn keep" onClick={onDismiss}>
                Dismiss
              </button>
            </div>
          )}
        </div>
      </div>

      <button
        ref={compactRef}
        className={`worktree-cleanup-compact ${isCompact ? '' : 'surface-hidden'} ${hasError ? 'failed' : ''}`}
        type="button"
        aria-live="polite"
        onClick={() => setCompactAnimated(false)}
      >
        <span className="compact-pin" aria-hidden="true" />
        <span className="compact-copy">
          <span className="compact-kicker">{hasError ? 'Create failed' : 'Session setup'}</span>
          <span className="compact-title">{hasError ? `${displayName} failed` : phaseLabel}</span>
          <span className="compact-detail">
            <span>{hasError ? 'open to resolve' : displayName}</span>
            {!hasError && <span className="compact-meter" aria-hidden="true"><span /></span>}
          </span>
        </span>
      </button>
    </>
  );
}
