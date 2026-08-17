// The static read-only bash set: the boring commands that carry normal work
// and cannot change anything. Everything this file declines goes to the
// classifier, so declining is cheap and being wrong in the other direction
// is not — every entry here runs unjudged.
//
// Two rules keep the set honest. A command that can be talked into writing
// (`sort -o`, `git branch -D`) carries the flags or subcommands that make it
// read-only, not just its name. And a command that can run another command
// (`env`, `xargs`, `find -exec`, `sh -c`) is not in the set at all: naming
// the wrapper would let anything ride in behind it.

/** Reaching the network is never read-only, whatever the command looks like. */
export const networkCommands: readonly string[] = [
  "curl",
  "wget",
  "http",
  "https",
  "httpie",
  "xh",
  "aria2c",
  "nc",
  "ncat",
  "netcat",
  "socat",
  "telnet",
  "ftp",
  "ssh",
  "scp",
  "sftp",
  "rsync",
];

type ReadOnlyCommand = {
  /** Flags that turn this command into a writer. */
  forbiddenFlags?: readonly string[];
  /** When present, only these subcommands are read-only. */
  subcommands?: readonly string[];
};

export const readOnlyCommands: Readonly<Record<string, ReadOnlyCommand>> = {
  ls: {},
  cat: {},
  head: {},
  tail: {},
  wc: {},
  pwd: {},
  echo: {},
  date: {},
  file: {},
  stat: {},
  du: {},
  df: {},
  which: {},
  whoami: {},
  hostname: {},
  uname: {},
  basename: {},
  dirname: {},
  realpath: {},
  readlink: {},
  printenv: {},
  tree: {},
  jq: {},
  cut: {},
  tr: {},
  uniq: {},
  diff: {},
  grep: {},
  egrep: {},
  fgrep: {},
  rg: {},
  sort: { forbiddenFlags: ["-o", "--output"] },
  git: { subcommands: ["status", "log", "diff", "show", "blame", "rev-parse", "ls-files", "describe", "shortlog"] },
};

/**
 * Characters that let one command line become several, redirect output, or
 * expand into text this matcher never sees. Any of them and the command is
 * not read-only, whatever the words around them say.
 */
const controlCharacters = [";", "&", ">", "<", "`", "$", "(", ")", "{", "}", "\n", "\r", "\\"];

export type BashClassification =
  | { kind: "read-only" }
  | { kind: "network"; command: string }
  | { kind: "unjudged"; reason: string };

export function classifyBashCommand(command: string): BashClassification {
  const trimmed = command.trim();
  if (trimmed === "") return { kind: "unjudged", reason: "empty command" };

  const network = networkCommandIn(trimmed);
  if (network !== undefined) return { kind: "network", command: network };

  for (const character of controlCharacters) {
    if (trimmed.includes(character)) {
      return { kind: "unjudged", reason: `command contains ${JSON.stringify(character)}` };
    }
  }
  if (trimmed.includes("||")) return { kind: "unjudged", reason: 'command contains "||"' };

  for (const segment of trimmed.split("|")) {
    const failure = readOnlySegmentFailure(segment.trim());
    if (failure !== undefined) return { kind: "unjudged", reason: failure };
  }
  return { kind: "read-only" };
}

/**
 * Scans the whole command line for a network command name rather than only
 * the leading word, so a network call cannot ride in as a later element of
 * something that otherwise parses as read-only.
 */
function networkCommandIn(command: string): string | undefined {
  for (const word of command.split(/[^A-Za-z0-9_.-]+/)) {
    if (networkCommands.includes(word.toLowerCase())) return word.toLowerCase();
  }
  return undefined;
}

function readOnlySegmentFailure(segment: string): string | undefined {
  const words = segment.split(/\s+/).filter((word) => word !== "");
  const name = words[0];
  if (name === undefined) return "empty command";
  if (name.includes("=")) return `environment assignment ${JSON.stringify(name)} prefixes the command`;

  const rule = readOnlyCommands[commandName(name)];
  if (rule === undefined) return `${commandName(name)} is not in the read-only set`;

  const args = words.slice(1);
  for (const flag of rule.forbiddenFlags ?? []) {
    if (args.some((arg) => arg === flag || arg.startsWith(`${flag}=`))) {
      return `${commandName(name)} ${flag} writes`;
    }
  }
  if (rule.subcommands !== undefined) {
    const subcommand = args[0];
    if (subcommand === undefined) return `${commandName(name)} without a subcommand`;
    if (!rule.subcommands.includes(subcommand)) return `${commandName(name)} ${subcommand} is not read-only`;
  }
  return undefined;
}

/** `/usr/bin/git` and `git` are the same command. */
function commandName(word: string): string {
  const parts = word.split("/");
  return (parts[parts.length - 1] ?? word).toLowerCase();
}
