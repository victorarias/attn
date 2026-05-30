// app/src/components/WorktreeCleanupPrompt.tsx
import { useCallback, useEffect, useRef, useState } from 'react';
import type { KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './WorktreeCleanupPrompt.css';

interface WorktreeCleanupPromptProps {
  isVisible: boolean;
  worktreePath: string;
  branchName?: string;
  isDeleting?: boolean;
  deleteError?: string | null;
  deleteForceable?: boolean;
  onKeep: () => void;
  onDelete: () => void;
  onAlwaysKeep: () => void;
}

export function WorktreeCleanupPrompt({
  isVisible,
  worktreePath,
  branchName,
  isDeleting = false,
  deleteError = null,
  deleteForceable = false,
  onKeep,
  onDelete,
  onAlwaysKeep,
}: WorktreeCleanupPromptProps) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  const compactRef = useRef<HTMLButtonElement>(null);
  const keepRef = useRef<HTMLButtonElement>(null);
  const deleteRef = useRef<HTMLButtonElement>(null);
  const alwaysRef = useRef<HTMLButtonElement>(null);
  const mountedRef = useRef(false);
  const compactTimerRef = useRef<number | null>(null);
  const wasDeletingRef = useRef(false);
  const setCompactAnimatedRef = useRef<(next: boolean, focus?: boolean) => void>(() => {});
  const [isCompact, setIsCompact] = useState(false);

  const displayName = branchName || worktreePath.split('/').pop() || 'worktree';
  const hasError = Boolean(deleteError);

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

  const actionButtons = useCallback(() => {
    return [keepRef.current, deleteRef.current, alwaysRef.current].filter((button) => {
      return button && !button.disabled;
    }) as HTMLButtonElement[];
  }, []);

  const focusPrimary = useCallback(() => {
    requestAnimationFrame(() => {
      if (isDeleting) {
        dialogRef.current?.focus();
        return;
      }
      if (hasError) {
        deleteRef.current?.focus();
        return;
      }
      keepRef.current?.focus();
    });
  }, [hasError, isDeleting]);

  const createMotionGhost = useCallback((source: HTMLElement, rect: DOMRect) => {
    const ghost = source.cloneNode(true) as HTMLElement;
    ghost.removeAttribute('id');
    ghost.querySelectorAll('[id]').forEach((node) => node.removeAttribute('id'));
    ghost.classList.remove(
      'surface-hidden',
      'motion-fade',
      'motion-target',
      'motion-underlay',
      'motion-lit',
    );
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
          focusPrimary();
        }
      });
    }
  }, [focusPrimary]);

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
    setCompactAnimatedRef.current = setCompactAnimated;
  }, [setCompactAnimated]);

  const handleEscape = useCallback(() => {
    if (isDeleting) {
      setCompactAnimated(true);
      return;
    }
    onKeep();
  }, [isDeleting, onKeep, setCompactAnimated]);

  useEscapeStack(handleEscape, isVisible && !isCompact);

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      const buttons = actionButtons();

      if (event.key === 'Tab') {
        if (buttons.length === 0) {
          event.preventDefault();
          dialogRef.current?.focus();
          return;
        }
        const active = document.activeElement as HTMLButtonElement | null;
        const currentIndex = Math.max(0, buttons.indexOf(active || buttons[0]));
        const delta = event.shiftKey ? -1 : 1;
        const nextIndex = (currentIndex + delta + buttons.length) % buttons.length;
        buttons[nextIndex]?.focus();
        event.preventDefault();
        return;
      }

      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;

      if (buttons.length === 0) return;
      const active = document.activeElement as HTMLButtonElement | null;
      const currentIndex = Math.max(0, buttons.indexOf(active || buttons[0]));
      const delta = event.key === 'ArrowRight' ? 1 : -1;
      const nextIndex = (currentIndex + delta + buttons.length) % buttons.length;
      buttons[nextIndex]?.focus();
      event.preventDefault();
    },
    [actionButtons]
  );

  useEffect(() => {
    if (!isVisible) return;
    const focusInitial = () => {
      const active = document.activeElement as HTMLElement | null;
      if (
        active
        && (dialogRef.current?.contains(active) || compactRef.current === active)
      ) {
        return;
      }
      focusPrimary();
    };
    focusInitial();
    const raf = requestAnimationFrame(focusInitial);
    const timeoutId = window.setTimeout(focusInitial, 50);
    return () => {
      cancelAnimationFrame(raf);
      window.clearTimeout(timeoutId);
    };
  }, [focusPrimary, isVisible]);

  useEffect(() => {
    if (!isVisible) {
      clearCompactTimer();
      wasDeletingRef.current = false;
      setIsCompact(false);
      return;
    }
    if (!isDeleting) {
      clearCompactTimer();
      wasDeletingRef.current = false;
      return;
    }
    if (wasDeletingRef.current) {
      return;
    }
    wasDeletingRef.current = true;
    compactTimerRef.current = window.setTimeout(() => {
      setCompactAnimatedRef.current(true, true);
    }, 280);
    return clearCompactTimer;
  }, [clearCompactTimer, isDeleting, isVisible]);

  useEffect(() => {
    if (hasError) {
      clearCompactTimer();
    }
  }, [clearCompactTimer, hasError]);

  if (!isVisible) return null;

  const dialogTitle = hasError
    ? deleteForceable ? 'Force delete this worktree?' : 'Worktree was not deleted'
    : isDeleting
      ? 'Deleting worktree'
      : 'Keep this worktree for later?';
  const dialogKicker = hasError ? 'Action needed' : isDeleting ? 'Cleanup running' : 'Session closed';
  const statusCopy = hasError
    ? deleteError
    : isDeleting
      ? 'git worktree remove'
      : 'Local branch workspace';
  const compactKicker = hasError ? 'Delete failed' : 'Cleanup running';
  const compactTitle = hasError ? `${displayName} still exists` : `Deleting ${displayName}`;

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
          aria-labelledby="worktree-cleanup-title"
          aria-describedby="worktree-cleanup-message"
          tabIndex={-1}
          onKeyDown={handleKeyDown}
        >
          <div className={`cleanup-status-pin ${hasError ? 'failed' : ''}`} aria-hidden="true" />
          <div className="cleanup-copy">
            <div className="cleanup-title">{dialogKicker}</div>
            <div className="cleanup-heading" id="worktree-cleanup-title">
              {dialogTitle}
            </div>
            <div className="cleanup-message" id="worktree-cleanup-message">
              <span className="cleanup-branch">{displayName}</span>
              <span className="cleanup-path">{worktreePath}</span>
            </div>
            {(isDeleting || hasError) && (
              <div className={`cleanup-operation ${hasError ? 'failed' : ''}`} role={isDeleting ? 'status' : 'alert'}>
                <span className="cleanup-operation-label">{hasError ? 'Delete failed' : 'Removing worktree'}</span>
                <span className="cleanup-operation-detail">{statusCopy}</span>
                {hasError && deleteForceable && (
                  <span className="cleanup-operation-detail">This removes the local folder and local branch. Remote branches are untouched.</span>
                )}
                {isDeleting && <span className="cleanup-meter" aria-hidden="true"><span /></span>}
              </div>
            )}
          </div>
          <div className="cleanup-actions">
          <button ref={keepRef} type="button" className="cleanup-btn keep" onClick={onKeep} disabled={isDeleting}>
            Keep
          </button>
          <button
            ref={deleteRef}
            type="button"
            className={`cleanup-btn ${hasError ? 'retry' : 'delete'}`}
            onClick={onDelete}
              disabled={isDeleting}
          >
            {hasError ? deleteForceable ? 'Force delete' : 'Retry delete' : 'Delete worktree'}
          </button>
          <button
            ref={alwaysRef}
            type="button"
            className="cleanup-btn always"
            onClick={onAlwaysKeep}
            disabled={isDeleting}
          >
            Always keep
          </button>
        </div>
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
          <span className="compact-kicker">{compactKicker}</span>
          <span className="compact-title">{compactTitle}</span>
          <span className="compact-detail">
            <span>{hasError ? 'open to resolve' : 'git worktree remove'}</span>
            <span className="compact-meter" aria-hidden="true"><span /></span>
          </span>
        </span>
      </button>
    </>
  );
}
