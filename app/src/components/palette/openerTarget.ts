/**
 * Where the markdown opener is allowed to look, and which session (if any) an
 * opened file may be bound to.
 *
 * The opener's index-and-open route runs entirely against the locally-connected
 * daemon: `fs_index` enumerates this machine's filesystem and `open_markdown`
 * docks a tile whose content this machine reads from disk. A session on a remote
 * endpoint has a `cwd` that names a path on *that* machine, so handing it to
 * either call crosses the ownership boundary — usually failing, but on a path
 * that happens to exist here it would silently show local files under a remote
 * label and then bind one of them to a remote session.
 *
 * So locality must be positively proven, the same rule `localWorkspaceDirectory`
 * applies to workspace directories. With a remote session selected the opener
 * falls back to the local notebook root (recents are daemon-local and always
 * fine) and forfeits the session binding: picking a file opens it in the OS
 * default app instead of docking a tile against the wrong machine.
 */
export interface OpenerSessionLike {
  id: string;
  cwd?: string;
  endpointId?: string;
}

export interface MarkdownOpenerTarget {
  /** Root for fuzzy indexing, or null when there is no local project context. */
  root: string | null;
  /**
   * Session to bind an opened file to. `''` means "let the daemon use its
   * currently selected session"; `null` means no local session owns this open,
   * so it must not go through the daemon at all.
   */
  sessionId: string | null;
}

export function resolveMarkdownOpenerTarget(
  session: OpenerSessionLike | undefined,
  notebookRoot: string | undefined | null,
): MarkdownOpenerTarget {
  const fallbackRoot = notebookRoot || null;
  if (!session) return { root: fallbackRoot, sessionId: '' };
  if (session.endpointId) return { root: fallbackRoot, sessionId: null };
  return { root: session.cwd || fallbackRoot, sessionId: session.id };
}
