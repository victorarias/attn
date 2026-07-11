export const pluginAPIVersion = 2;

export const supportedOpenCodeVersions = new Set(["1.17.16", "1.17.18"]);

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
