import { randomUUID } from "node:crypto";
import type { AttnRPCClient } from "./attn-rpc";
import {
  compareVersion,
  evaluatePiVersion,
  parseStableVersion,
  piThinkingLevels,
  type DriverRegisterResult,
  type DriverSpawnParams,
  type DriverSpawnResult,
  type PiMetadata,
  type SessionClosedParams,
} from "./types";

export type CommandResult = { exitCode: number; stdout: string; stderr: string };
export type RunCommand = (argv: string[]) => Promise<CommandResult>;

type Availability =
  | { ok: true; executable: string; version: string }
  | { ok: false; message: string };

const defaultRunCommand: RunCommand = async (argv) => {
  const child = Bun.spawn(argv, { stdout: "pipe", stderr: "pipe" });
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  return { exitCode, stdout, stderr };
};

export class PiDriver {
  private readonly rpc: AttnRPCClient;
  private readonly runCommand: RunCommand;
  private readonly executable: string;
  private availability: Availability = { ok: false, message: "pi availability has not been checked" };

  constructor(options: { rpc: AttnRPCClient; runCommand?: RunCommand; executable?: string }) {
    this.rpc = options.rpc;
    this.runCommand = options.runCommand ?? defaultRunCommand;
    this.executable = options.executable?.trim() || process.env.ATTN_PI_EXECUTABLE?.trim() || "pi";
  }

  async initialize(): Promise<void> {
    await this.refreshAvailability();
    if (!this.availability.ok) return;
    const result = await this.rpc.request<DriverRegisterResult>("driver.register", {
      agent: "pi",
      capabilities: {
        resume: true,
        initial_prompt: true,
        model_pin: true,
        effort_pin: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected pi driver registration");
    // No recovery work: this driver keeps no per-run state. Active runs keep
    // living in their daemon-owned PTYs; resume metadata is persisted daemon-side.
  }

  health(): { ok: boolean; message: string } {
    return this.availability.ok
      ? { ok: true, message: `pi ${this.availability.version} is ready` }
      : { ok: false, message: this.availability.message };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const metadata: PiMetadata = {
      schema: 1,
      pi_session_id: randomUUID(),
      pi_version: availability.version,
      model: cleanOptional(params.model),
      thinking: thinkingFor(params.effort),
    };
    await this.reportMetadata(params, metadata);
    return { argv: this.argvFor(availability.executable, metadata, params.initial_prompt), cwd: params.cwd };
  }

  async resume(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const previous = parsePiMetadata(params.metadata);
    const installed = parseStableVersion(availability.version);
    const recorded = parseStableVersion(previous.pi_version);
    if (compareVersion(installed, recorded) < 0) {
      throw new Error(
        `installed pi ${installed.raw} is older than the ${recorded.raw} this session last ran on; upgrade pi or point ATTN_PI_EXECUTABLE at a matching build`,
      );
    }
    const metadata: PiMetadata = {
      schema: 1,
      pi_session_id: previous.pi_session_id,
      pi_version: availability.version,
      model: cleanOptional(params.model) ?? previous.model,
      thinking: thinkingFor(params.effort) ?? previous.thinking,
    };
    await this.reportMetadata(params, metadata);
    return { argv: this.argvFor(availability.executable, metadata, undefined), cwd: params.cwd };
  }

  async sessionClosed(_params: SessionClosedParams): Promise<{ ok: true }> {
    return { ok: true };
  }

  private argvFor(executable: string, metadata: PiMetadata, initialPrompt: string | undefined): string[] {
    const argv = [executable, "--session-id", metadata.pi_session_id];
    if (metadata.model) argv.push("--model", metadata.model);
    if (metadata.thinking) argv.push("--thinking", metadata.thinking);
    if (initialPrompt !== undefined && initialPrompt.trim() !== "") argv.push(initialPrompt);
    return argv;
  }

  private async reportMetadata(params: DriverSpawnParams, metadata: PiMetadata): Promise<void> {
    await this.rpc.request("session.report_metadata", {
      session_id: requireText(params.session_id, "session_id"),
      run_id: requireText(params.run_id, "run_id"),
      seq: 1,
      metadata,
    });
  }

  private async refreshAvailability(): Promise<void> {
    try {
      const result = await this.runCommand([this.executable, "--version"]);
      if (result.exitCode !== 0) throw new Error(result.stderr.trim() || `exit ${result.exitCode}`);
      const evaluated = evaluatePiVersion(result.stdout.trim());
      if (evaluated.kind === "invalid") throw new Error(`unrecognized pi version: ${evaluated.reason}`);
      if (evaluated.kind === "too_old") {
        throw new Error(`pi ${evaluated.installed.raw} is older than the minimum supported ${evaluated.minimum.raw}`);
      }
      this.availability = { ok: true, executable: this.executable, version: evaluated.installed.raw };
    } catch (error) {
      this.availability = { ok: false, message: `pi executable ${this.executable} is unavailable: ${safeError(error)}` };
    }
  }

  private async requireAvailability(): Promise<Extract<Availability, { ok: true }>> {
    await this.refreshAvailability();
    if (!this.availability.ok) throw new Error(this.availability.message);
    return this.availability;
  }
}

export function parsePiMetadata(value: unknown): PiMetadata {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("pi session metadata must be an object");
  }
  const record = value as Record<string, unknown>;
  if (record.schema !== 1) throw new Error(`unsupported pi session metadata schema ${JSON.stringify(record.schema)}`);
  const sessionID = record.pi_session_id;
  if (typeof sessionID !== "string" || sessionID.trim() === "") throw new Error("pi session metadata is missing pi_session_id");
  const version = record.pi_version;
  if (typeof version !== "string" || version.trim() === "") throw new Error("pi session metadata is missing pi_version");
  return {
    schema: 1,
    pi_session_id: sessionID.trim(),
    pi_version: version.trim(),
    model: cleanOptional(typeof record.model === "string" ? record.model : undefined),
    thinking: cleanOptional(typeof record.thinking === "string" ? record.thinking : undefined),
  };
}

function thinkingFor(effort: string | undefined): string | undefined {
  const cleaned = cleanOptional(effort);
  if (cleaned === undefined) return undefined;
  if (!(piThinkingLevels as readonly string[]).includes(cleaned)) {
    throw new Error(`unsupported pi thinking level ${JSON.stringify(cleaned)}; expected one of ${piThinkingLevels.join(", ")}`);
  }
  return cleaned;
}

function cleanOptional(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

function requireText(value: string, field: string): string {
  const trimmed = value?.trim();
  if (!trimmed) throw new Error(`${field} is required`);
  return trimmed;
}

function safeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
