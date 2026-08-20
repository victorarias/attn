// An artifact is an object, not a chip. The kind is named in words in a fixed
// left gutter so the column is scannable and there is no icon vocabulary to
// learn; alignment does the work a box would have done.
//
// A seed's artifacts, as objects rather than chips. Three facts decide a row:
// what kind of thing it is, which part of it a person recognizes, and whether
// following it stays inside attn or leaves it. Everything else is noise.
import { useEffect, useState } from 'react';
import type { SeedArtifactReference } from '../types/generated';
import { artifactKey } from './seedArtifacts';
import './SeedArtifactRows.css';

export interface SeedArtifactRowsProps {
  artifacts: readonly SeedArtifactReference[];
  onOpenMarkdownArtifact?: (path: string) => void;
  /** Answers whether a path is really there. Absent leaves rows unchecked. */
  checkArtifactPath?: (path: string) => Promise<boolean>;
}

interface Presentation {
  /** The kind gutter: what sort of object this is, in two or three words. */
  kind: string;
  /** The part a person recognizes — a filename, a PR number, a host. */
  primary: string;
  /** Where it lives. Dropped when it would only repeat the primary. */
  secondary: string;
  /** Following this leaves attn. */
  external: boolean;
  /** The path a markdown artifact opens as a tile. */
  path: string;
  href: string;
}

// A GitHub pull request and a random link are not the same object, and a reader
// scanning a seed's artifacts should never have to read a URL to tell them
// apart. The recognition is the view's, not the daemon's: the wire kind stays
// `url`, and a URL that does not match reads as a plain link.
const PR_URL = /^https?:\/\/(?:www\.)?github\.com\/([^/]+)\/([^/]+)\/(pull|issues)\/(\d+)/;

function present(artifact: SeedArtifactReference): Presentation {
  const base: Presentation = { kind: 'artifact', primary: '', secondary: '', external: false, path: '', href: '' };
  const path = artifact.path ?? '';
  const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  const name = path.slice(path.lastIndexOf('/') + 1);

  if (artifact.kind === 'markdown_file' || artifact.kind === 'repository') {
    return {
      ...base,
      kind: artifact.kind === 'repository' ? 'repo file' : 'markdown',
      primary: name || path,
      secondary: [artifact.repository, dir].filter(Boolean).join(' / '),
      path,
    };
  }
  if (artifact.kind === 'notebook') {
    return { ...base, kind: 'notebook', primary: artifact.notebook_document_id ?? '' };
  }
  const url = artifact.url ?? '';
  const pr = PR_URL.exec(url);
  if (pr) {
    const [, owner, repo, sort, number] = pr;
    return {
      ...base,
      kind: sort === 'pull' ? 'pull request' : 'issue',
      primary: `#${number}`,
      secondary: `${owner}/${repo}`,
      external: true,
      href: url,
    };
  }
  if (url) {
    let host = url;
    let rest = '';
    try {
      const parsed = new URL(url);
      host = parsed.host.replace(/^www\./, '');
      rest = `${parsed.pathname}${parsed.search}`.replace(/\/$/, '');
    } catch {
      // Not parseable: the whole string is the only identity there is.
    }
    return { ...base, kind: 'link', primary: host, secondary: rest === '/' ? '' : rest, external: true, href: url };
  }
  return { ...base, kind: artifact.kind, primary: artifact.repository ?? artifact.kind };
}

/**
 * Which artifact paths are missing. Only absolute paths are checked: a
 * repo-relative path resolves against a worktree this surface does not know, so
 * asking about it would answer "missing" about a file that is right there.
 * Making every artifact answerable is a daemon-side projection, not a view.
 */
function useMissingPaths(
  artifacts: readonly SeedArtifactReference[],
  check?: (path: string) => Promise<boolean>,
): Set<string> {
  const [missing, setMissing] = useState<Set<string>>(new Set());
  const absolute: string[] = [];
  for (const artifact of artifacts) {
    const path = artifact.path ?? '';
    if (path.startsWith('/')) absolute.push(path);
  }
  const paths = absolute.join('\n');

  useEffect(() => {
    if (!check || !paths) {
      setMissing(new Set());
      return;
    }
    let ignore = false;
    const wanted = paths.split('\n');
    Promise.all(
      wanted.map((path) => check(path).then((exists) => (exists ? '' : path)).catch(() => '')),
    ).then((results) => {
      if (ignore) return;
      setMissing(new Set(results.filter(Boolean)));
    });
    return () => {
      ignore = true;
    };
  }, [paths, check]);

  return missing;
}

export function SeedArtifactRows({ artifacts, onOpenMarkdownArtifact, checkArtifactPath }: SeedArtifactRowsProps) {
  const missing = useMissingPaths(artifacts, checkArtifactPath);
  if (artifacts.length === 0) return null;

  return (
    <ul className="seed-artifacts">
      {artifacts.map((artifact) => {
        const view = present(artifact);
        const gone = Boolean(view.path) && missing.has(view.path);
        const body = (
          <>
            <span className="seed-artifact__kind">{view.kind}</span>
            <span className="seed-artifact__primary">{view.primary}</span>
            {view.secondary && <span className="seed-artifact__secondary">{view.secondary}</span>}
            {gone ? (
              <span className="seed-artifact__gone">not on disk</span>
            ) : (
              view.external && <span className="seed-artifact__leaves" aria-label="opens outside attn">↗</span>
            )}
          </>
        );

        if (gone) {
          // Nothing to open, so nothing offers to. The path stays whole and
          // selectable: the next move is finding where the file went.
          return (
            <li key={artifactKey(artifact)} className="seed-artifact is-gone" title={view.path}>
              {body}
            </li>
          );
        }
        if (view.path && onOpenMarkdownArtifact) {
          return (
            <li key={artifactKey(artifact)} className="seed-artifact">
              <button type="button" onClick={() => onOpenMarkdownArtifact(view.path)} title={view.path}>
                {body}
              </button>
            </li>
          );
        }
        if (view.href) {
          return (
            <li key={artifactKey(artifact)} className="seed-artifact">
              <a href={view.href} title={view.href}>{body}</a>
            </li>
          );
        }
        return <li key={artifactKey(artifact)} className="seed-artifact">{body}</li>;
      })}
    </ul>
  );
}
