// Fuzzy file finder scoring for the in-tile Notebook finder. A pure, headless-
// testable model of the ActionMenu scorer, widened from substring terms to a
// Cmd+P-style subsequence match over a note's path and title — so "kbidx" finds
// "knowledge/index.md". No React, no daemon: feed it the file index + query.
import type { NotebookEntry } from '../../hooks/useDaemonSocket';

// The last path segment (filename) of a notebook path. Pure string op; the index
// paths are always forward-slashed (daemon-normalized), so no platform handling.
export function finderBasename(path: string): string {
  const slash = path.lastIndexOf('/');
  return slash === -1 ? path : path.slice(slash + 1);
}

// Characters that begin a "word" in a path, so a match right after one is a
// stronger signal (the start of a folder/file name, a word inside a kebab/snake
// name, or after a dot).
const WORD_BOUNDARY = /[/\-_. ]/;

// Score how well `query` subsequence-matches `text` (both already lowercased).
// 0 means `query` is not a subsequence of `text` at all. A higher score rewards
// contiguous runs and matches at word boundaries, so "index" ranks the basename
// over a scattered match deep in a path.
function subsequenceScore(text: string, query: string): number {
  if (query === '') return 1;
  let score = 0;
  let from = 0;
  let prevMatch = -2;
  let run = 0;
  for (let qi = 0; qi < query.length; qi++) {
    const target = query[qi];
    let found = -1;
    for (let i = from; i < text.length; i++) {
      if (text[i] === target) {
        found = i;
        break;
      }
    }
    if (found === -1) return 0; // not a subsequence — disqualify
    score += 1;
    if (found === prevMatch + 1) {
      run += 1;
      score += run * 2; // reward a contiguous run, growing with its length
    } else {
      run = 0;
    }
    if (found === 0 || WORD_BOUNDARY.test(text[found - 1])) {
      score += 3; // match begins a word/segment
    }
    prevMatch = found;
    from = found + 1;
  }
  return score;
}

// Score one notebook entry against a query: the best of its path and its title,
// with the basename weighted a touch higher (finders are filename-first). Returns
// 0 when neither the path nor the title contains the query as a subsequence.
export function scoreNotebookFile(entry: NotebookEntry, query: string): number {
  const q = query.toLowerCase().trim();
  if (q === '') return 1;
  const path = entry.path.toLowerCase();
  const base = finderBasename(path);
  const title = (entry.title ?? '').toLowerCase();
  const pathScore = subsequenceScore(path, q);
  const baseScore = subsequenceScore(base, q);
  const titleScore = subsequenceScore(title, q);
  // The basename match (when it exists) is the most intentional, so give it the
  // strongest weight; fall back to the broader path/title matches otherwise.
  return Math.max(pathScore, baseScore * 1.5, titleScore * 0.9);
}

// Order two entries when their scores tie: most-recently-updated first (the live
// notes you're likely reaching for), then by path for a stable, predictable list.
function tieBreak(a: NotebookEntry, b: NotebookEntry): number {
  const au = a.updated ?? '';
  const bu = b.updated ?? '';
  if (au !== bu) return au < bu ? 1 : -1; // updated desc (ISO strings sort lexically)
  return a.path < b.path ? -1 : a.path > b.path ? 1 : 0;
}

// Rank the file index for `query`: drop non-matches, sort best-first (ties broken
// by recency then path), and cap at `limit` so a huge vault can't flood the list.
// An empty query lists everything (recency-ordered), capped the same way.
export function rankNotebookFiles(
  files: NotebookEntry[],
  query: string,
  limit = 50,
): NotebookEntry[] {
  return files
    .map((entry) => ({ entry, score: scoreNotebookFile(entry, query) }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => (right.score - left.score) || tieBreak(left.entry, right.entry))
    .slice(0, limit)
    .map(({ entry }) => entry);
}
