// Every blocked call, written to disk at decision time, before anything is told
// about it: the relay report can be lost, this cannot. One JSON object per
// line, one write each. The reader is `internal/automode/denialledger.go`.
import { appendFileSync, mkdirSync, readFileSync, renameSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";
import type { AutoModeDenial } from "./index";

/** attn's channel, injected by the pi driver at spawn from the daemon's data dir. */
export const denialLedgerEnvVar = "ATTN_PI_AUTOMODE_DENIAL_LOG";

/**
 * The attn session the denials belong to, injected by the pi driver beside
 * the path. Empty for a bare pi: it has no attn session to name.
 */
export const denialLedgerSessionEnvVar = "ATTN_PI_SESSION_ID";

/** Bare pi's file, beside the config file it already reads. */
export const denialLedgerFileName = "attn-automode-denials.jsonl";

/**
 * One generation's size tripwire. The classifier prompt sizes the line:
 * measured 2026-08-22, the largest the transcript budgets can produce is
 * 24,569 bytes, against 476 with no prompt. This holds ~8,000 records at the
 * ~8 KB a real denial runs, and two generations are kept.
 */
export const denialLedgerMaxBytes = 64 * 1024 * 1024;

export type EnvironmentLike = Record<string, string | undefined>;

export function denialLedgerPath(env: EnvironmentLike): string {
  const configured = env[denialLedgerEnvVar]?.trim();
  if (configured && configured !== "") return configured;
  const agentDir = env.PI_CODING_AGENT_DIR?.trim();
  return join(agentDir && agentDir !== "" ? agentDir : join(homedir(), ".pi", "agent"), denialLedgerFileName);
}

/** The ledger this process writes to. Nothing touches disk until a denial. */
export function denialLedgerFor(env: EnvironmentLike): DenialLedger {
  return new DenialLedger(denialLedgerPath(env), env[denialLedgerSessionEnvVar]?.trim() ?? "");
}

/** One line of the file. `session_id` is empty for a pi running outside attn. */
export type DenialLedgerRecord = {
  session_id: string;
  tool_call_id: string;
  tool: string;
  action: string;
  reason: string;
  rule: string;
  at: string;
  /** Written only when false: the user's approval could not have lifted this. */
  clearable?: boolean;
  /** What the deciding layer was sent. Absent when no classifier ran. */
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

  /**
   * Keeps the active file and one previous generation. The drop count comes
   * from the generation being destroyed, never from the active file, whose own
   * marker survives the rename — counting it here would double every earlier
   * rotation.
   */
  private rotateIfFull(): void {
    if (sizeOf(this.path) < this.maxBytes) return;
    const previous = `${this.path}.1`;
    const dropped = countRecords(previous) + carriedDropCount(previous);
    try {
      renameSync(this.path, previous);
    } catch (error) {
      // Another session in this profile rotated first. Its rotation is ours.
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

/** What the markers in a generation stand for: records dropped before it. */
function carriedDropCount(path: string): number {
  let total = 0;
  for (const line of readLines(path)) total += markerDropCount(line) ?? 0;
  return total;
}

/** Records in a generation, markers excluded — a marker is not a denial. */
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
