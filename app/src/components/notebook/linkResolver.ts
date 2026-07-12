// Single resolver for markdown link hrefs inside the notebook editor and its broken-
// link checker, replacing the two resolvers that used to disagree (one required a
// leading '/' + '.md' for in-notebook links, the other accepted bare root-relative
// paths — neither resolved against the linking note's own directory). The daemon
// always interprets fs paths root-relative (fsdoc cleanRel), so directory-relative
// resolution has to happen here, before a path ever reaches the daemon.

export type ResolvedLink =
  | { kind: 'note'; path: string; anchor?: string } // path root-relative, no leading slash, normalized
  | { kind: 'fragment'; anchor: string } // same-note anchor, '#' stripped, URI-decoded
  | { kind: 'external'; href: string };

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function decodeAnchor(raw: string): string {
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

// Split '#anchor' (kept, decoded) and '?query' (dropped) off a path, in that order —
// an anchor always follows any query in a URL, so a literal '?' inside the anchor
// text (rare, but possible after decoding) is preserved rather than mis-split.
function stripAnchorAndQuery(href: string): { path: string; anchor?: string } {
  const hashIdx = href.indexOf('#');
  const path = hashIdx === -1 ? href : href.slice(0, hashIdx);
  const anchor = hashIdx === -1 ? undefined : decodeAnchor(href.slice(hashIdx + 1));
  const queryIdx = path.indexOf('?');
  return { path: queryIdx === -1 ? path : path.slice(0, queryIdx), anchor };
}

// Join and normalize '.'/'..' segments against a base. A '..' that would climb above
// the notebook root clamps there instead of escaping it or throwing — matching the
// daemon's cleanRel, since the resolved path is always handed to the daemon next.
function normalizeJoin(baseDir: string, path: string): string {
  const parts: string[] = [];
  for (const segment of `${baseDir}/${path}`.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') parts.pop();
    else parts.push(segment);
  }
  return parts.join('/');
}

// Directory of a root-relative note path; '' for a root-level path.
export function noteDir(notePath: string): string {
  const idx = notePath.lastIndexOf('/');
  return idx === -1 ? '' : notePath.slice(0, idx);
}

// GitHub-style anchor slug of a heading's text: lowercase, punctuation stripped
// (keep letters/digits/space/hyphen), spaces → '-'.
export function headingSlug(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9 -]/g, '')
    .trim()
    .replace(/\s+/g, '-');
}

// baseDir: the current note's directory, root-relative, '' at the root
// (e.g. 'knowledge/areas' for note 'knowledge/areas/foo.md').
export function resolveNotebookLink(href: string, baseDir: string): ResolvedLink {
  const trimmed = href.trim();
  if (!trimmed) return { kind: 'external', href: '' };
  if (trimmed.startsWith('#')) {
    return { kind: 'fragment', anchor: decodeAnchor(trimmed.slice(1)) };
  }
  if (trimmed.startsWith('//') || SCHEME.test(trimmed)) {
    return { kind: 'external', href: trimmed };
  }

  const { path, anchor } = stripAnchorAndQuery(trimmed);
  const resolved = path.startsWith('/')
    ? normalizeJoin('', path.replace(/^\/+/, ''))
    : normalizeJoin(baseDir, path);

  if (!resolved) return { kind: 'external', href: '' };
  return { kind: 'note', path: resolved, anchor };
}
