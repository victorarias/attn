// Where a session's auto mode config comes from. Two callers, two answers:
// attn hands it over in ATTN_PI_AUTOMODE_CONFIG at spawn, and bare pi reads
// the same JSON from a file under pi's config directory.
//
// A config that cannot be read never turns auto mode off. The shipped defaults
// take over and the session is told so — the alternative is a session that
// silently lost its permission system because of a typo.
import { join } from "node:path";
import { homedir } from "node:os";
import {
  defaultAutoModeConfig,
  loadAutoModeConfig,
  type AutoModeConfig,
  type RawAutoModeConfig,
} from "./config";

/** attn's channel. Environment, not argv: prose entries are multi-line. */
export const autoModeConfigEnvVar = "ATTN_PI_AUTOMODE_CONFIG";

/** Bare pi's channel, under `PI_CODING_AGENT_DIR` or `~/.pi/agent`. */
export const autoModeConfigFileName = "attn-automode.json";

export type AutoModeSource = {
  config: AutoModeConfig;
  /** What went wrong reading it, for the session to say out loud. Absent when nothing did. */
  problem?: string;
};

export type EnvironmentLike = Record<string, string | undefined>;

export function autoModeConfigFilePath(env: EnvironmentLike): string {
  const configured = env.PI_CODING_AGENT_DIR?.trim();
  return join(configured && configured !== "" ? configured : join(homedir(), ".pi", "agent"), autoModeConfigFileName);
}

/**
 * The config an attn session was launched with. Undefined means attn sent
 * none — auto mode is not built at all and the session runs as bare pi does.
 */
export function attnAutoModeSource(env: EnvironmentLike): AutoModeSource | undefined {
  const raw = env[autoModeConfigEnvVar]?.trim();
  if (!raw) return undefined;
  return parseSource(raw, `attn sent an auto mode config this session could not read`);
}

/**
 * Bare pi's config: the same environment variable when one is set (so
 * `pi -e automode.js` inside attn behaves like the suite), otherwise the file.
 *
 * With neither, auto mode is off. Bare pi has no attn to have decided
 * otherwise, so running it is something the user asks for — with `--auto`, or
 * by writing the file.
 */
export function standaloneAutoModeSource(
  env: EnvironmentLike,
  readFile: (path: string) => string | undefined,
): AutoModeSource {
  const fromAttn = attnAutoModeSource(env);
  if (fromAttn) return fromAttn;

  const path = autoModeConfigFilePath(env);
  let contents: string | undefined;
  try {
    contents = readFile(path);
  } catch (error) {
    return { config: offByDefault(defaultAutoModeConfig), problem: `${path} could not be read: ${message(error)}` };
  }
  if (contents === undefined) return { config: offByDefault(defaultAutoModeConfig) };

  const source = parseSource(contents, `${path} could not be read`);
  // A file that does not say whether auto mode starts on does not turn it on:
  // writing one is how a bare pi user configures auto mode, not how they
  // enable it.
  return source.statedEnabledDefault ? source : { ...source, config: offByDefault(source.config) };
}

type ParsedSource = AutoModeSource & { statedEnabledDefault: boolean };

function parseSource(raw: string, whatWentWrong: string): ParsedSource {
  try {
    const parsed = JSON.parse(raw) as RawAutoModeConfig;
    return { config: loadAutoModeConfig(parsed), statedEnabledDefault: typeof parsed?.enabled_default === "boolean" };
  } catch (error) {
    // Counted as having stated it: a file too broken to read was still written
    // by someone who wanted auto mode, and the safe reading of "I cannot tell"
    // is on rather than off.
    return {
      config: defaultAutoModeConfig,
      statedEnabledDefault: true,
      problem: `${whatWentWrong}: ${message(error)}. Auto mode is running on its shipped defaults.`,
    };
  }
}

function offByDefault(config: AutoModeConfig): AutoModeConfig {
  return { ...config, enabledDefault: false };
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
