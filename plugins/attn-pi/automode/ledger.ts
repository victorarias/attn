// The denial ledger: every blocked call, written to disk at decision time,
// before anything is told about it. attn's relay report is a mirror — a bare
// pi has no relay, and a relay whose socket died drops what it is handed —
// so the file is what makes a denial readable later at all.
//
// One JSON object per line, appended with a single write so a crash cannot
// interleave two records. The reader is `internal/automode/denialledger.go`.
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
 * How large one generation grows before it rotates. Measured 2026-08-18: a fat denial
 * line — a piped-curl command carrying a full classifier reason — is 476
 * bytes, so one generation holds ~8,800 of them against a circuit breaker that
 * stops a session at 20 denials since the user last spoke. Two generations are kept, so nothing
 * is dropped before ~18,000 records. A tripwire for a loop nobody is
 * watching, not a budget.
 */
export const denialLedgerMaxBytes = 4 * 1024 * 1024;

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
   * Keeps the active file and one previous generation. The generation being
   * overwritten is counted first and its count is carried into the new active
   * file's opening marker, plus whatever earlier rotations already dropped:
   * the reader sums those markers, so a clip is always something the person
   * reading the feed is told about rather than a gap they cannot see.
   */
  private rotateIfFull(): void {
    if (sizeOf(this.path) < this.maxBytes) return;
    const previous = `${this.path}.1`;
    const dropped = countRecords(previous) + carriedDropCount(this.path);
    renameSync(this.path, previous);
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

/** Records in a generation, markers excluded — a marker is not a denial. */
function countRecords(path: string): number {
  return readLines(path).filter((line) => !isMarker(line)).length;
}

/** What the markers already in a generation say was dropped before it. */
function carriedDropCount(path: string): number {
  let total = 0;
  for (const line of readLines(path)) {
    const dropped = markerDropCount(line);
    if (dropped !== undefined) total += dropped;
  }
  return total;
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
