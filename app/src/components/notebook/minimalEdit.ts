// A single-range replacement that turns one string into another by trimming their
// shared prefix and suffix. Used to push an on-disk change into the live editor as a
// SMALL edit rather than a whole-document swap: CodeMirror keeps its scroll position
// and selection anchored across a minimal change, but snaps the viewport back to the
// top when the entire document is replaced. So a reader whose open note is rewritten
// by an agent stays where they were reading instead of being yanked to the top.

export interface MinimalEdit {
  // Offsets into the ORIGINAL string. Replace [from, to) with `insert` to get `next`.
  from: number;
  to: number;
  insert: string;
}

// Smallest [from, to)+insert that rewrites `current` into `next`, found by skipping
// the common leading and trailing runs. Returns null when the strings are identical
// (no edit, so callers can avoid dispatching an empty transaction). The replaced range
// and the inserted slice never overlap the shared prefix, so applying the result —
// current.slice(0, from) + insert + current.slice(to) — reconstructs `next` exactly,
// for appends, prepends, in-place edits, and full replacements alike.
export function computeMinimalEdit(current: string, next: string): MinimalEdit | null {
  if (current === next) return null;

  const maxPrefix = Math.min(current.length, next.length);
  let prefix = 0;
  while (prefix < maxPrefix && current.charCodeAt(prefix) === next.charCodeAt(prefix)) {
    prefix++;
  }

  // Trim the shared suffix, but never back past the shared prefix on either side, so
  // the [from, to) range and the inserted slice stay well-formed and non-overlapping.
  let endCur = current.length;
  let endNext = next.length;
  while (
    endCur > prefix &&
    endNext > prefix &&
    current.charCodeAt(endCur - 1) === next.charCodeAt(endNext - 1)
  ) {
    endCur--;
    endNext--;
  }

  return { from: prefix, to: endCur, insert: next.slice(prefix, endNext) };
}
