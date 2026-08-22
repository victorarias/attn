import { isAbsolute, resolve, sep } from "node:path";

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

  | { location: "in-cwd"; resolved: string }

  | { location: "outside-cwd"; resolved: string }

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

function protectedSegment(resolved: string): string | undefined {
  const segments = resolved.split(sep).filter((segment) => segment !== "");
  for (const [index, segment] of segments.entries()) {
    const name = segment.toLowerCase();
    const last = index === segments.length - 1;
    if (protectedDirectories.includes(name)) return segment;
    if (last && protectedFiles.includes(name)) return segment;
    if (last && name.startsWith(".env")) return segment;

    if (last && name.startsWith("attn-automode")) return segment;
  }
  return undefined;
}
