export const pluginAPIVersion = 5;

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

export const minimumPiVersion = parseStableVersion("0.80.10");

export function evaluatePiVersion(value: string): VersionCompatibility {
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
  return compareVersion(installed, minimumPiVersion) < 0
    ? { kind: "too_old", installed, minimum: minimumPiVersion }
    : { kind: "supported", installed, minimum: minimumPiVersion };
}

export type DriverCapabilities = Record<string, boolean>;

export type ActivePluginRun = {
  session_id: string;
  run_id: string;
  metadata?: unknown;
};

export type DriverRegisterResult = {
  ok: boolean;
  active_runs?: ActivePluginRun[];
};

export type DriverSpawnParams = {
  session_id: string;
  run_id: string;
  cwd: string;
  label?: string;
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

// Resume token: persisted daemon-side via session.report_metadata and handed
// back verbatim on driver.resume.
export type PiMetadata = {
  schema: 1;
  pi_session_id: string;
  pi_version: string;
  model?: string;
  thinking?: string;
};

export const piThinkingLevels = ["off", "minimal", "low", "medium", "high", "xhigh", "max"] as const;
