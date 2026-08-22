// attn hands the config over in ATTN_PI_AUTOMODE_CONFIG at spawn; bare pi reads
// the same JSON from a file. A config that cannot be read never turns auto mode
// off: the shipped defaults take over and the session says so.
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

/** Undefined means attn sent none, so auto mode is not built at all. */
export function attnAutoModeSource(env: EnvironmentLike): AutoModeSource | undefined {
  const raw = env[autoModeConfigEnvVar]?.trim();
  if (!raw) return undefined;
  return parseSource(raw, `attn sent an auto mode config this session could not read`);
}

/** The environment variable when set, otherwise the file. With neither, off. */
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
  // Writing a file configures auto mode; it does not enable it.
  return source.statedEnabledDefault ? source : { ...source, config: offByDefault(source.config) };
}

type ParsedSource = AutoModeSource & { statedEnabledDefault: boolean };

function parseSource(raw: string, whatWentWrong: string): ParsedSource {
  try {
    const parsed = JSON.parse(raw) as RawAutoModeConfig;
    return { config: loadAutoModeConfig(parsed), statedEnabledDefault: typeof parsed?.enabled_default === "boolean" };
  } catch (error) {
    // A file too broken to read was still written by someone who wanted this.
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
