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
  forbiddenFlags?: readonly string[];

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

function commandName(word: string): string {
  const parts = word.split("/");
  return (parts[parts.length - 1] ?? word).toLowerCase();
}
