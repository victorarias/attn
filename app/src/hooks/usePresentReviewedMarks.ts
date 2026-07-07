// app/src/hooks/usePresentReviewedMarks.ts
// Per-(presentation, round) "reviewed" marks for the Present reader, jaunt
// style. Persisted in localStorage so a mark survives a window reload but
// stays scoped to the exact round it was made against — a NEW round id
// starts with a fresh, empty key. That is the intended mid-round-vs-new-round
// semantic: reviewing round 1 doesn't pre-mark anything in round 2, since the
// diff content underneath a path can have changed entirely. Marks are not
// cleared on submit here — submit-time semantics belong to a later slice.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

function storageKey(presentationId: string, roundId: string): string {
  return `attn.present.reviewed.${presentationId}.${roundId}`;
}

function readMarks(key: string): Set<string> {
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return new Set();
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.filter((p): p is string => typeof p === 'string'));
  } catch (err) {
    console.warn('[usePresentReviewedMarks] Failed to read marks:', err);
    return new Set();
  }
}

function writeMarks(key: string, marks: Set<string>): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(Array.from(marks)));
  } catch (err) {
    console.warn('[usePresentReviewedMarks] Failed to persist marks:', err);
  }
}

export interface PresentReviewedMarksControls {
  reviewed: ReadonlySet<string>;
  toggleReviewed(path: string): void;
  /** Idempotent — used by the J-advance auto-mark-on-leave behavior. */
  markReviewed(path: string): void;
}

export function usePresentReviewedMarks(
  presentationId: string | null,
  roundId: string | null,
  filePaths: string[]
): PresentReviewedMarksControls {
  const key = presentationId && roundId ? storageKey(presentationId, roundId) : null;
  const [reviewed, setReviewed] = useState<Set<string>>(new Set());

  // filePaths is a fresh array identity most renders; only its content should
  // drive the pruning effect below.
  const filePathsKey = filePaths.join('\0');
  const filePathsRef = useRef(filePaths);
  filePathsRef.current = filePaths;

  // Load on mount / whenever the (presentation, round) identity changes, then
  // prune any path no longer in the current manifest and write the pruned
  // set back so the persisted key never drifts from the live file list.
  useEffect(() => {
    if (!key) {
      setReviewed(new Set());
      return;
    }
    const loaded = readMarks(key);
    const validPaths = new Set(filePathsRef.current);
    const pruned = new Set(Array.from(loaded).filter((p) => validPaths.has(p)));
    if (pruned.size !== loaded.size) writeMarks(key, pruned);
    setReviewed(pruned);
    // filePathsKey (not filePaths) is the real dependency — see above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, filePathsKey]);

  const toggleReviewed = useCallback(
    (path: string) => {
      if (!key) return;
      setReviewed((current) => {
        const next = new Set(current);
        if (next.has(path)) next.delete(path);
        else next.add(path);
        writeMarks(key, next);
        return next;
      });
    },
    [key]
  );

  const markReviewed = useCallback(
    (path: string) => {
      if (!key) return;
      setReviewed((current) => {
        if (current.has(path)) return current;
        const next = new Set(current);
        next.add(path);
        writeMarks(key, next);
        return next;
      });
    },
    [key]
  );

  return useMemo(() => ({ reviewed, toggleReviewed, markReviewed }), [reviewed, toggleReviewed, markReviewed]);
}
