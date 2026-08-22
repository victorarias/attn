import { appendFileSync, mkdirSync, readFileSync, renameSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";
import type { AutoModeDenial } from "./index";

export const denialLedgerEnvVar = "ATTN_PI_AUTOMODE_DENIAL_LOG";

export const denialLedgerSessionEnvVar = "ATTN_PI_SESSION_ID";

export const denialLedgerFileName = "attn-automode-denials.jsonl";

export const denialLedgerMaxBytes = 64 * 1024 * 1024;

export type EnvironmentLike = Record<string, string | undefined>;

export function denialLedgerPath(env: EnvironmentLike): string {
  const configured = env[denialLedgerEnvVar]?.trim();
  if (configured && configured !== "") return configured;
  const agentDir = env.PI_CODING_AGENT_DIR?.trim();
  return join(agentDir && agentDir !== "" ? agentDir : join(homedir(), ".pi", "agent"), denialLedgerFileName);
}

export function denialLedgerFor(env: EnvironmentLike): DenialLedger {
  return new DenialLedger(denialLedgerPath(env), env[denialLedgerSessionEnvVar]?.trim() ?? "");
}

export type DenialLedgerRecord = {
  session_id: string;
  tool_call_id: string;
  tool: string;
  action: string;
  reason: string;
  rule: string;
  at: string;

  clearable?: boolean;

  prompt?: DenialLedgerPrompt;
};

export type DenialLedgerPrompt = {
  layer: string;
  system: string;
  user: string;
};

export type DenialLedgerLike = { record(denial: AutoModeDenial): void };

export class DenialLedger implements DenialLedgerLike {
  private ensured = false;

  constructor(
    private readonly path: string,
    private readonly sessionID: string,
    private readonly maxBytes: number = denialLedgerMaxBytes,
  ) {}

  record(denial: AutoModeDenial): void {
    const line: DenialLedgerRecord = {
      session_id: this.sessionID,
      tool_call_id: denial.toolCallId,
      tool: denial.tool,
      action: denial.action,
      reason: denial.reason,
      rule: denial.rule,
      at: denial.at,
      ...(denial.clearable === false ? { clearable: false } : {}),
      ...(denial.prompt ? { prompt: denial.prompt } : {}),
    };
    this.ensureDirectory();
    this.rotateIfFull();
    appendFileSync(this.path, `${JSON.stringify(line)}\n`, { encoding: "utf8", mode: 0o600 });
  }

  private ensureDirectory(): void {
    if (this.ensured) return;
    mkdirSync(dirname(this.path), { recursive: true, mode: 0o700 });
    this.ensured = true;
  }

  private rotateIfFull(): void {
    if (sizeOf(this.path) < this.maxBytes) return;
    const previous = `${this.path}.1`;
    const dropped = countRecords(previous) + carriedDropCount(previous);
    try {
      renameSync(this.path, previous);
    } catch (error) {
      if ((error as NodeJS.ErrnoException)?.code !== "ENOENT") throw error;
      return;
    }
    if (dropped === 0) return;
    const marker = JSON.stringify({ type: "rotated", dropped, at: new Date().toISOString() });
    appendFileSync(this.path, `${marker}\n`, { encoding: "utf8", mode: 0o600 });
  }
}

function sizeOf(path: string): number {
  try {
    return statSync(path).size;
  } catch {
    return 0;
  }
}

function readLines(path: string): string[] {
  let contents: string;
  try {
    contents = readFileSync(path, "utf8");
  } catch {
    return [];
  }
  return contents.split("\n").filter((line) => line.trim() !== "");
}

function carriedDropCount(path: string): number {
  let total = 0;
  for (const line of readLines(path)) total += markerDropCount(line) ?? 0;
  return total;
}

function countRecords(path: string): number {
  return readLines(path).filter((line) => !isMarker(line)).length;
}

function isMarker(line: string): boolean {
  return markerDropCount(line) !== undefined;
}

function markerDropCount(line: string): number | undefined {
  try {
    const parsed = JSON.parse(line) as { type?: unknown; dropped?: unknown };
    if (parsed?.type !== "rotated") return undefined;
    return typeof parsed.dropped === "number" && Number.isFinite(parsed.dropped) ? parsed.dropped : 0;
  } catch {
    return undefined;
  }
}
