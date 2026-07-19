import { existsSync } from "node:fs";
import { randomUUID } from "node:crypto";
import type { AttnRPCClient } from "./attn-rpc";
import type { RelayConnection, RelayServer } from "./relay";
import type {
  RelayDeliverMessageParams,
  RelayDeliverMessageResult,
  RelayHelloParams,
  RelayHelloResult,
  RelayReportStateParams,
  RelayReportStopParams,
} from "./relay-protocol";
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

// One run = one live pi process launched by this driver. `token` is the
// bearer credential handed to the pi-side suite via env; `seq` is the
// per-run monotonic cursor for session.report_* calls, owned entirely here.
type RunState = {
  token: string;
  sessionID: string;
  runID: string;
  seq: number;
  metadata: PiMetadata;
  connection?: RelayConnection;
};

const deliverMessageTimeoutMs = 10_000;

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
  private readonly relay: RelayServer;
  private readonly suitePath: string;
  private availability: Availability = { ok: false, message: "pi availability has not been checked" };
  private readonly runsByToken = new Map<string, RunState>();
  private readonly runsBySessionID = new Map<string, RunState>();

  constructor(options: {
    rpc: AttnRPCClient;
    relay: RelayServer;
    suitePath: string;
    runCommand?: RunCommand;
    executable?: string;
  }) {
    this.rpc = options.rpc;
    this.relay = options.relay;
    this.suitePath = options.suitePath;
    this.runCommand = options.runCommand ?? defaultRunCommand;
    this.executable = options.executable?.trim() || process.env.ATTN_PI_EXECUTABLE?.trim() || "pi";
  }

  async initialize(): Promise<void> {
    await this.refreshAvailability();
    if (!this.availability.ok) return;
    await this.relay.listen();
    const result = await this.rpc.request<DriverRegisterResult>("driver.register", {
      agent: "pi",
      capabilities: {
        resume: true,
        initial_prompt: true,
        model_pin: true,
        effort_pin: true,
        state_reporting: true,
        message_delivery: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected pi driver registration");
    // No recovery work: this driver keeps no cross-restart run state. Active
    // runs keep living in their daemon-owned PTYs; resume metadata is
    // persisted daemon-side.
  }

  health(): { ok: boolean; message: string } {
    return this.availability.ok
      ? { ok: true, message: `pi ${this.availability.version} is ready` }
      : { ok: false, message: this.availability.message };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const suitePath = this.requireSuitePath();
    const metadata: PiMetadata = {
      schema: 1,
      pi_session_id: randomUUID(),
      pi_version: availability.version,
      model: cleanOptional(params.model),
      thinking: thinkingFor(params.effort),
    };
    const run = this.createRun(requireText(params.session_id, "session_id"), requireText(params.run_id, "run_id"), metadata);
    await this.reportMetadata(run);
    return {
      argv: this.argvFor(availability.executable, metadata, params.initial_prompt, suitePath),
      cwd: params.cwd,
      env: this.envFor(run.token),
    };
  }

  async resume(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const suitePath = this.requireSuitePath();
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
    const run = this.createRun(requireText(params.session_id, "session_id"), requireText(params.run_id, "run_id"), metadata);
    await this.reportMetadata(run);
    return {
      argv: this.argvFor(availability.executable, metadata, undefined, suitePath),
      cwd: params.cwd,
      env: this.envFor(run.token),
    };
  }

  async sessionClosed(params: SessionClosedParams): Promise<{ ok: true }> {
    const run = this.runsBySessionID.get(params.session_id);
    if (run) {
      this.runsBySessionID.delete(params.session_id);
      this.runsByToken.delete(run.token);
    }
    return { ok: true };
  }

  // Delegate methods invoked by RelayServer when the pi-side suite calls in.

  async suiteHello(connection: RelayConnection, rawParams: unknown): Promise<RelayHelloResult> {
    const params = parseRelayHello(rawParams);
    const run = this.requireRunByToken(params.token);
    run.connection = connection;
    // Keep model/thinking pins; the suite is only authoritative for pi's own
    // native session id and version, which change across resume/fork/new.
    run.metadata = { ...run.metadata, pi_session_id: params.pi_session_id, pi_version: params.pi_version };
    await this.reportMetadata(run);
    return { ok: true };
  }

  async suiteReportState(rawParams: unknown): Promise<void> {
    const params = parseRelayReportState(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_state", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      state: "working",
    });
  }

  async suiteReportStop(rawParams: unknown): Promise<void> {
    const params = parseRelayReportStop(rawParams);
    const run = this.requireRunByToken(params.token);
    const text = params.assistant_text.trim();
    // Reserve this report's slot in the per-run cursor BEFORE awaiting
    // classification: a message delivered mid-classification can start a new
    // turn whose "working" report must outrank this stop. With the seq taken
    // up front, the daemon's strictly-increasing cursor discards the stale
    // verdict instead of letting it overwrite live activity.
    const seq = this.nextSeq(run);
    // Empty text means the agent settled without saying anything: there is
    // nothing to await a response to, so skip the (up to ~30s) classifier call.
    const verdict = text === "" ? "idle" : await this.classifyStop(run, text);
    await this.rpc.request("session.report_stop", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq,
      verdict,
    });
  }

  // Called for the daemon's driver.deliver_message request.
  async deliverMessage(rawParams: unknown): Promise<{ ok: boolean }> {
    const params = parseDeliverMessageParams(rawParams);
    const run = this.runsBySessionID.get(params.session_id);
    if (!run) throw new Error(`no active pi run for session ${params.session_id}`);
    if (run.runID !== params.run_id) {
      throw new Error(`run_id mismatch for session ${params.session_id}: expected ${run.runID}, got ${params.run_id}`);
    }
    if (!run.connection) throw new Error(`no live pi suite connection for session ${params.session_id}`);
    const result = await this.relay.deliverMessage<RelayDeliverMessageParams, RelayDeliverMessageResult>(
      run.connection,
      { text: params.text },
      deliverMessageTimeoutMs,
    );
    return { ok: result.delivered };
  }

  private async classifyStop(run: RunState, assistantText: string): Promise<string> {
    const result = await this.rpc.request<{ verdict: string }>("attn.classify_stop", {
      session_id: run.sessionID,
      run_id: run.runID,
      assistant_text: assistantText,
    });
    return result.verdict;
  }

  private createRun(sessionID: string, runID: string, metadata: PiMetadata): RunState {
    const previous = this.runsBySessionID.get(sessionID);
    if (previous) this.runsByToken.delete(previous.token);
    const run: RunState = { token: randomUUID(), sessionID, runID, seq: 0, metadata };
    this.runsByToken.set(run.token, run);
    this.runsBySessionID.set(sessionID, run);
    return run;
  }

  private requireRunByToken(token: string): RunState {
    const run = this.runsByToken.get(token);
    if (!run) throw new Error("unknown pi suite token");
    return run;
  }

  private nextSeq(run: RunState): number {
    run.seq += 1;
    return run.seq;
  }

  private requireSuitePath(): string {
    if (!existsSync(this.suitePath)) {
      throw new Error(`pi suite entrypoint not found at ${this.suitePath}; this is a build/packaging bug`);
    }
    return this.suitePath;
  }

  private envFor(token: string): Record<string, string> {
    return { ATTN_PI_SUITE_SOCKET: this.relay.socketPath, ATTN_PI_TOKEN: token };
  }

  private argvFor(
    executable: string,
    metadata: PiMetadata,
    initialPrompt: string | undefined,
    suitePath: string,
  ): string[] {
    const argv = [executable, "--session-id", metadata.pi_session_id];
    if (metadata.model) argv.push("--model", metadata.model);
    if (metadata.thinking) argv.push("--thinking", metadata.thinking);
    argv.push("-e", suitePath);
    if (initialPrompt !== undefined && initialPrompt.trim() !== "") argv.push(initialPrompt);
    return argv;
  }

  private async reportMetadata(run: RunState): Promise<void> {
    await this.rpc.request("session.report_metadata", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      metadata: run.metadata,
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

function parseRelayHello(value: unknown): RelayHelloParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.hello params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const piSessionID = record.pi_session_id;
  const piVersion = record.pi_version;
  const reason = record.reason;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.hello is missing token");
  if (typeof piSessionID !== "string" || piSessionID.trim() === "") throw new Error("suite.hello is missing pi_session_id");
  if (typeof piVersion !== "string" || piVersion.trim() === "") throw new Error("suite.hello is missing pi_version");
  if (typeof reason !== "string") throw new Error("suite.hello is missing reason");
  return { token: token.trim(), pi_session_id: piSessionID.trim(), pi_version: piVersion.trim(), reason };
}

function parseRelayReportState(value: unknown): RelayReportStateParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_state params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_state is missing token");
  if (record.state !== "working") {
    throw new Error(`suite.report_state state must be "working", got ${JSON.stringify(record.state)}`);
  }
  return { token: token.trim(), state: "working" };
}

function parseRelayReportStop(value: unknown): RelayReportStopParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_stop params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const assistantText = record.assistant_text;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_stop is missing token");
  if (typeof assistantText !== "string") throw new Error("suite.report_stop is missing assistant_text");
  return { token: token.trim(), assistant_text: assistantText };
}

function parseDeliverMessageParams(value: unknown): { session_id: string; run_id: string; text: string } {
  if (typeof value !== "object" || value === null) throw new Error("driver.deliver_message params must be an object");
  const record = value as Record<string, unknown>;
  const sessionID = record.session_id;
  const runID = record.run_id;
  const text = record.text;
  if (typeof sessionID !== "string" || sessionID.trim() === "") throw new Error("driver.deliver_message is missing session_id");
  if (typeof runID !== "string" || runID.trim() === "") throw new Error("driver.deliver_message is missing run_id");
  if (typeof text !== "string") throw new Error("driver.deliver_message is missing text");
  return { session_id: sessionID.trim(), run_id: runID.trim(), text };
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
