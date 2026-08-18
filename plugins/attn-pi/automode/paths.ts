// Where a file path sits relative to auto mode's safety envelope. The
// envelope is the session's working directory minus the paths that decide
// what agents and shells are allowed to do — editing those from inside a
// session is how a session widens its own leash.
//
// Resolution is lexical (node:path), so a symlink inside the working
// directory that points outside it resolves as in-envelope.
import { isAbsolute, resolve, sep } from "node:path";

/** Directory names that are protected wherever they appear in a path. */
export const protectedDirectories: readonly string[] = [
  ".git",
  ".pi",
  ".attn",
  ".claude",
  ".codex",
  ".ssh",
  ".gnupg",
  ".aws",
];

/**
 * File names that are protected wherever they appear. Last-segment names
 * beginning `attn-automode` are protected by the same rule (see
 * protectedSegment): auto mode's config and its denial ledger.
 */
export const protectedFiles: readonly string[] = [
  ".bashrc",
  ".bash_profile",
  ".zshrc",
  ".zshenv",
  ".zprofile",
  ".profile",
  "config.fish",
  ".netrc",
  ".npmrc",
  ".mcp.json",
  ".claude.json",
];

export type PathLocation =
  /** Inside the working directory and outside every protected path. */
  | { location: "in-envelope"; resolved: string }
  /** Resolves outside the working directory. */
  | { location: "outside-cwd"; resolved: string }
  /** Names a protected path, whether or not it is inside the working directory. */
  | { location: "protected"; resolved: string; protectedBy: string };

export function locatePath(cwd: string, path: string): PathLocation {
  const resolved = isAbsolute(path) ? resolve(path) : resolve(cwd, path);
  const protectedBy = protectedSegment(resolved);
  if (protectedBy !== undefined) return { location: "protected", resolved, protectedBy };
  return { location: isInside(resolve(cwd), resolved) ? "in-envelope" : "outside-cwd", resolved };
}

export function isInside(cwd: string, resolved: string): boolean {
  if (resolved === cwd) return true;
  const base = cwd.endsWith(sep) ? cwd : cwd + sep;
  return resolved.startsWith(base);
}

// Compared case-insensitively: on a case-insensitive filesystem `.GIT` and
// `.git` are the same directory.
function protectedSegment(resolved: string): string | undefined {
  const segments = resolved.split(sep).filter((segment) => segment !== "");
  for (const [index, segment] of segments.entries()) {
    const name = segment.toLowerCase();
    const last = index === segments.length - 1;
    if (protectedDirectories.includes(name)) return segment;
    if (last && protectedFiles.includes(name)) return segment;
    if (last && name.startsWith(".env")) return segment;
    // Auto mode's own files — its config and its denial ledger, wherever the
    // pi agent dir or attn's data dir puts them. A session that can edit its
    // permission system does not have one, and a session that can edit the
    // record of what it was refused leaves no record.
    if (last && name.startsWith("attn-automode")) return segment;
  }
  return undefined;
}
