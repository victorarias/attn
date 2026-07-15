export const pluginAPIVersion = 2;

export type StableVersion = {
  raw: string;
  major: number;
  minor: number;
  patch: number;
};

export type VersionCompatibility =
  | { kind: "supported"; installed: StableVersion; minimum: StableVersion }
  | { kind: "too_old"; installed: StableVersion; minimum: StableVersion }
  | { kind: "invalid"; raw: string; reason: string };

export const minimumOpenCodeVersion = parseStableVersion("1.17.16");

export function parseStableVersion(value: string): StableVersion {
  const candidate = value.trim();
  const match = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(candidate);
  if (!match) {
    throw new Error(`expected a stable MAJOR.MINOR.PATCH release, got ${candidate || "(empty)"}`);
  }
  const [major, minor, patch] = match.slice(1).map(Number);
  if (![major, minor, patch].every(Number.isSafeInteger)) {
    throw new Error(`version components must be safe integers, got ${candidate}`);
  }
  return { raw: `${major}.${minor}.${patch}`, major, minor, patch };
}

export function compareVersion(a: StableVersion, b: StableVersion): -1 | 0 | 1 {
  for (const key of ["major", "minor", "patch"] as const) {
    if (a[key] < b[key]) return -1;
    if (a[key] > b[key]) return 1;
  }
  return 0;
}

export function evaluateOpenCodeVersion(value: string): VersionCompatibility {
  let installed: StableVersion;
  try {
    installed = parseStableVersion(value);
  } catch (error) {
    return {
      kind: "invalid",
      raw: value,
      reason: error instanceof Error ? error.message : String(error),
    };
  }
  return compareVersion(installed, minimumOpenCodeVersion) < 0
    ? { kind: "too_old", installed, minimum: minimumOpenCodeVersion }
    : { kind: "supported", installed, minimum: minimumOpenCodeVersion };
}

export type DriverCapabilities = Record<string, boolean>;

export type DriverSpawnParams = {
  session_id: string;
  run_id: string;
  cwd: string;
  label?: string;
  yolo?: boolean;
  model?: string;
  effort?: string;
  initial_prompt?: string;
  metadata?: unknown;
};

export type DriverSpawnResult = {
  argv: string[];
  env?: Record<string, string>;
  cwd?: string;
};

export type SessionClosedParams = {
  session_id: string;
  run_id: string;
  reason: string;
  exit_code?: number;
  signal?: string;
};

export type OpenCodeModel = {
  providerID: string;
  id: string;
  variant: string;
};

export type OpenCodeMetadata = {
  schema: 1;
  opencode_session_id: string;
  opencode_version: string;
  // Absent only for metadata produced before interactive-default launches.
  // Those older records were all delegated, so resume treats absence as true.
  pinned?: boolean;
  model?: string;
  variant?: string;
};

export type RunRecord = {
  schema: 1;
  attn_session_id: string;
  run_id: string;
  next_seq: number;
  port: number;
  password_ref: string;
  prompt_ref?: string;
  launch_config_ref: string;
  opencode_session_id?: string;
  opencode_version: string;
  pinned: boolean;
  model?: string;
  variant?: string;
  resume: boolean;
  created_at: string;
};

export type LaunchConfig = {
  schema: 1;
  run_id: string;
  executable: string;
  cwd: string;
  password_ref: string;
  port: number;
  yolo: boolean;
  resume_session_id?: string;
};

export type ReportState = "working" | "waiting_input" | "pending_approval" | "idle" | "unknown";

export type Report =
  | { kind: "metadata"; metadata: OpenCodeMetadata }
  | { kind: "state"; state: ReportState }
  | { kind: "stop"; verdict: "idle" | "waiting_input" | "unknown" };
