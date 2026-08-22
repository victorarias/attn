// Where a path sits relative to the static rules: the working directory, minus
// the paths that decide what agents and shells may do. Resolution is lexical,
// so a symlink pointing out of the cwd still resolves as in-cwd.
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

/** Protected wherever they appear. `attn-automode*` is covered by protectedSegment. */
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
  | { location: "in-cwd"; resolved: string }
  /** Resolves outside the working directory. */
  | { location: "outside-cwd"; resolved: string }
  /** Names a protected path, whether or not it is inside the working directory. */
  | { location: "protected"; resolved: string; protectedBy: string };

export function locatePath(cwd: string, path: string): PathLocation {
  const resolved = isAbsolute(path) ? resolve(path) : resolve(cwd, path);
  const protectedBy = protectedSegment(resolved);
  if (protectedBy !== undefined) return { location: "protected", resolved, protectedBy };
  return { location: isInside(resolve(cwd), resolved) ? "in-cwd" : "outside-cwd", resolved };
}

export function isInside(cwd: string, resolved: string): boolean {
  if (resolved === cwd) return true;
  const base = cwd.endsWith(sep) ? cwd : cwd + sep;
  return resolved.startsWith(base);
}

// Case-insensitive: `.GIT` and `.git` are one directory on macOS.
function protectedSegment(resolved: string): string | undefined {
  const segments = resolved.split(sep).filter((segment) => segment !== "");
  for (const [index, segment] of segments.entries()) {
    const name = segment.toLowerCase();
    const last = index === segments.length - 1;
    if (protectedDirectories.includes(name)) return segment;
    if (last && protectedFiles.includes(name)) return segment;
    if (last && name.startsWith(".env")) return segment;
    // Auto mode's own config and ledger, wherever they live. A session that can
    // edit its permission system does not have one.
    if (last && name.startsWith("attn-automode")) return segment;
  }
  return undefined;
}
