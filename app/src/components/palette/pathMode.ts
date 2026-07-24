/**
 * Path mode: the opener's second mode, reached by typing a path rather than a
 * name. `/`, `~`, `./`, and `../` are the only triggers, so an ordinary fuzzy
 * query like `docs/plan` — which also contains a slash — stays fuzzy.
 *
 * The daemon does the parsing (see parseBrowseInput): it expands `~`, lists the
 * directory before the last slash, and filters that listing by whatever was
 * typed after it. The one thing it cannot resolve is a relative path, which
 * would land on the daemon's own working directory, so `./` and `../` are
 * expanded here against the opener's root before the query is sent.
 */

/** True when the query names a path rather than a file to fuzzy-match. */
export function isPathQuery(query: string): boolean {
  const trimmed = query.trimStart();
  return (
    trimmed.startsWith('/') ||
    trimmed.startsWith('~') ||
    trimmed === '.' ||
    trimmed.startsWith('./') ||
    trimmed.startsWith('../')
  );
}

/**
 * Converts a path query into the input `browse_directory` expects, or null when
 * the query is not a path (or is relative with no root to resolve against, in
 * which case there is nothing honest to list).
 */
export function toBrowseInput(query: string, root: string | null): string | null {
  const trimmed = query.trimStart();
  if (!isPathQuery(trimmed)) return null;
  if (trimmed.startsWith('/') || trimmed.startsWith('~')) return trimmed;
  if (!root) return null;
  return normalizePath(`${root}/${trimmed}`);
}

/**
 * Resolves `.` and `..` segments textually. Symlinks are not followed — the
 * daemon resolves the final path anyway; this only needs to produce something
 * it can list.
 */
function normalizePath(path: string): string {
  const trailingSlash = path.endsWith('/');
  const absolute = path.startsWith('/');
  const resolved: string[] = [];
  for (const segment of path.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (resolved.length > 0 && resolved[resolved.length - 1] !== '..') {
        resolved.pop();
        continue;
      }
      if (absolute) continue; // can't go above /
    }
    resolved.push(segment);
  }
  const joined = (absolute ? '/' : '') + resolved.join('/');
  if (joined === '/' || joined === '') return absolute ? '/' : '.';
  return trailingSlash ? `${joined}/` : joined;
}

/**
 * The query that descends into a directory the user just picked: its display
 * path plus a trailing slash, which the daemon reads as "list this directory"
 * rather than "filter its parent by this name".
 */
export function descendQuery(displayPath: string): string {
  return displayPath.endsWith('/') ? displayPath : `${displayPath}/`;
}
