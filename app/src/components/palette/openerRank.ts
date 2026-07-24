import { scoreFile } from './rank';

// One row in the markdown opener. `label` is what the user sees and what the
// fuzzy scorer matches — root-relative for files inside the fuzzy root, the
// absolute path for anything else (a recent from another project). `absPath` is
// what actually gets opened. `recentAt` is set only for remembered files, and
// carries the last-open timestamp so ties break toward the fresher document.
export interface OpenerFile {
  absPath: string;
  label: string;
  recentAt?: string;
}

// How much a recently-opened file's score is multiplied by. Enough to win ties
// and near-ties against an equally good match you've never opened, not enough
// to keep a weak match above a clearly better one.
export const RECENT_BONUS = 1.4;

// Join a root and a root-relative slash path into an absolute path.
function joinRoot(root: string, rel: string): string {
  return root.endsWith('/') ? `${root}${rel}` : `${root}/${rel}`;
}

// Build the opener's candidate list: remembered files first (their order is the
// daemon's frecency ranking, which is what an empty query shows), then the
// fuzzy index, with any index entry that is already a recent dropped so a file
// never appears twice. Recents inside the fuzzy root are labeled relative to it
// so they read the same as their index counterparts.
export function mergeOpenerFiles(
  recents: { path: string; lastAt: string }[],
  root: string | null,
  indexFiles: string[],
): OpenerFile[] {
  const prefix = root ? (root.endsWith('/') ? root : `${root}/`) : null;
  const merged: OpenerFile[] = recents.map((recent) => ({
    absPath: recent.path,
    label: prefix && recent.path.startsWith(prefix) ? recent.path.slice(prefix.length) : recent.path,
    recentAt: recent.lastAt,
  }));
  const seen = new Set(merged.map((file) => file.absPath));
  if (!root) return merged;
  for (const rel of indexFiles) {
    const absPath = joinRoot(root, rel);
    if (seen.has(absPath)) continue;
    seen.add(absPath);
    merged.push({ absPath, label: rel });
  }
  return merged;
}

// Rank the opener list for a query. An empty query is the recents list — the
// index is deliberately not listed, since "everything in the repo" is noise
// before you have typed anything. Once there is a query, recents and index
// entries rank together in one list (no pinned section, no mode change
// mid-typing), with recents carrying RECENT_BONUS.
export function rankOpenerFiles(files: OpenerFile[], query: string, limit = 50): OpenerFile[] {
  if (query.trim() === '') {
    return files.filter((file) => file.recentAt).slice(0, limit);
  }
  return files
    .map((file) => ({
      file,
      score: scoreFile({ path: file.label, updated: file.recentAt }, query) * (file.recentAt ? RECENT_BONUS : 1),
    }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => {
      if (right.score !== left.score) return right.score - left.score;
      // Ties: most recently opened first, then alphabetically for stability.
      const leftAt = left.file.recentAt ?? '';
      const rightAt = right.file.recentAt ?? '';
      if (leftAt !== rightAt) return leftAt < rightAt ? 1 : -1;
      return left.file.label < right.file.label ? -1 : left.file.label > right.file.label ? 1 : 0;
    })
    .slice(0, limit)
    .map(({ file }) => file);
}
