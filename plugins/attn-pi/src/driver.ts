import { existsSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { availableModels, type AvailableModels, type ModelQuery } from "../automode/models";
import type { AttnRPCClient } from "./attn-rpc";
import type { RelayConnection, RelayServer } from "./relay";
import type {
  RelayDeliverMessageParams,
  RelayDeliverMessageResult,
  RelayHelloParams,
  RelayHelloState,
  RelayHelloResult,
  RelayReportDenialParams,
  RelayReportInputTakenParams,
  RelayReportPullRequestParams,
  RelayReportStateParams,
  RelayReportStopParams,
} from "./relay-protocol";
import {
  compareVersion,
  evaluatePiVersion,
  parseStableVersion,
  piThinkingLevels,
  type ActivePluginRun,
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

type RunState = {
  token: string;
  sessionID: string;
  runID: string;
  seq: number;
  metadata: PiMetadata;
  connection?: RelayConnection;
  /** Pending "nobody is declaring this session's state" alarm; see markUnbacked. */
  unbacked?: ReturnType<typeof setTimeout>;
};

const deliverMessageTimeoutMs = 10_000;

// A tripwire, not a deadline: a live pi re-dials within a second of the socket
// appearing and the suite's reconnect backoff caps at 30s (suite/core.ts).
const unbackedRunGraceMs = 120_000;

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
  private readonly env: Record<string, string | undefined>;
  private readonly queryModels: ModelQuery;
  private modelQuery?: Promise<AvailableModels>;
  private readonly executable: string;
  private readonly relay: RelayServer;
  private readonly suitePath: string;
  private availability: Availability = { ok: false, message: "pi availability has not been checked" };
  private readonly runsByToken = new Map<string, RunState>();
  private readonly runsBySessionID = new Map<string, RunState>();

  /** The shipped tripwire, shortened by tests that would otherwise wait it out. */
  private readonly unbackedGraceMs: number;

  constructor(options: {
    rpc: AttnRPCClient;
    relay: RelayServer;
    suitePath: string;
    runCommand?: RunCommand;
    env?: Record<string, string | undefined>;
    queryModels?: ModelQuery;
    executable?: string;
    unbackedGraceMs?: number;
  }) {
    this.rpc = options.rpc;
    this.relay = options.relay;
    this.suitePath = options.suitePath;
    this.runCommand = options.runCommand ?? defaultRunCommand;
    this.env = options.env ?? process.env;
    this.queryModels = options.queryModels ?? availableModels;
    this.executable = options.executable?.trim() || process.env.ATTN_PI_EXECUTABLE?.trim() || "pi";
    this.unbackedGraceMs = options.unbackedGraceMs ?? unbackedRunGraceMs;
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
        model_discovery: true,
        effort_pin: true,
        state_reporting: true,
        message_delivery: true,
        auto_mode: true,
        pull_request_reporting: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected pi driver registration");
    // Adopt before listen(): the socket opens only once every inherited token is
    // known, so a suite re-dialing the instant the path appears is never refused.
    this.adoptActiveRuns(result.active_runs ?? []);
    await this.relay.listen();
  }

  models(): Promise<AvailableModels> {
    return this.modelQuery ??= this.queryModels(this.executable, this.env).finally(() => {
      this.modelQuery = undefined;
    });
  }

  async delegationModels(): Promise<{ models: unknown[]; detail: string }> {
    const catalog = await this.models();
    if (catalog.problem) throw new Error(catalog.problem);
    return {
      models: catalog.providers.flatMap(provider => provider.models.map(model => ({
        harness: "pi", provider: provider.provider, id: model.id,
        name: model.name ?? model.id, description: "",
        effort_support: model.effortSupport ?? "unknown", effort_levels: model.effortLevels ?? [],
        access: provider.ready ? "unknown" : "unsupported", detail: provider.detail ?? "",
      }))),
      detail: "Configured Pi models. Account access is checked by the provider when used.",
    };
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
      env: this.envFor(run.token, params.auto_mode, run.sessionID),
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
      env: this.envFor(run.token, params.auto_mode, run.sessionID),
    };
  }

  async sessionClosed(params: SessionClosedParams): Promise<{ ok: true }> {
    const run = this.runsBySessionID.get(params.session_id);
    if (run) {
      this.markBacked(run);
      this.runsBySessionID.delete(params.session_id);
      this.runsByToken.delete(run.token);
    }
    return { ok: true };
  }


  async suiteHello(connection: RelayConnection, rawParams: unknown): Promise<RelayHelloResult> {
    const params = parseRelayHello(rawParams);
    const run = this.requireRunByToken(params.token);
    run.connection = connection;
    this.markBacked(run);
    if (params.dropped_reports !== undefined) {
      console.error(
        `attn-pi: session ${run.sessionID} could not deliver ${params.dropped_reports} state report(s) while the relay was down`,
      );
    }
    connection.onClose(() => {
      if (run.connection !== connection) return;
      run.connection = undefined;
      console.error(
        `attn-pi: relay connection for session ${run.sessionID} closed; nothing declares its state until a suite dials back`,
      );
      this.markUnbacked(run, "the pi suite disconnected");
    });
    run.metadata = { ...run.metadata, pi_session_id: params.pi_session_id, pi_version: params.pi_version };
    await this.reportMetadata(run);
    if (params.pi_state !== undefined) await this.restateAfterUnknown(run, params.pi_state);
    return { ok: true };
  }

  /** Hands attn what pi says it is, to use only while attn says `unknown`: a hello
   * is news about the channel, and declaring it would re-open a settled turn. */
  private async restateAfterUnknown(run: RunState, state: RelayHelloState): Promise<void> {
    try {
      await this.rpc.request("session.report_state", {
        session_id: run.sessionID,
        run_id: run.runID,
        seq: this.nextSeq(run),
        state,
        only_if_unknown: true,
      });
    } catch (error) {
      console.error(`attn-pi: could not restate session ${run.sessionID} as ${state}: ${String(error)}`);
    }
  }

  async suiteReportState(rawParams: unknown): Promise<void> {
    const params = parseRelayReportState(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_state", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      state: params.state,
    });
  }

  async suiteReportStop(rawParams: unknown): Promise<void> {
    const params = parseRelayReportStop(rawParams);
    const run = this.requireRunByToken(params.token);
    const text = params.assistant_text.trim();
    // Reserve the seq BEFORE awaiting classification: a message delivered mid-
    // classification starts a turn whose report must outrank this stop.
    const seq = this.nextSeq(run);
    const verdict = text === "" || params.aborted ? "idle" : await this.classifyStop(run, text);
    await this.rpc.request("session.report_stop", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq,
      verdict,
    });
  }

  async suiteReportDenial(rawParams: unknown): Promise<void> {
    const params = parseRelayReportDenial(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_automode_denial", {
      session_id: run.sessionID,
      run_id: run.runID,
      tool: params.tool,
      action: params.action,
      reason: params.reason,
      rule: params.rule,
      at: params.at,
    });
  }

  async suiteReportInputTaken(rawParams: unknown): Promise<void> {
    const params = parseRelayReportInputTaken(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_input_taken", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      input_id: params.input_id,
    });
  }

  async suiteReportPullRequest(rawParams: unknown): Promise<void> {
    const params = parseRelayReportPullRequest(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_pull_request", {
      session_id: run.sessionID,
      run_id: run.runID,
      url: params.url,
    });
  }

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
      { input_id: params.input_id, text: params.text },
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
    const run: RunState = { token: runID, sessionID, runID, seq: 0, metadata };
    this.runsByToken.set(run.token, run);
    this.runsBySessionID.set(sessionID, run);
    this.markUnbacked(run, "the pi suite has not connected since this run was launched");
    return run;
  }

  /** Rebuilds this driver's run state from the runs attn reports still live. This
   * plugin registers two agents, so pi metadata is the discriminator. */
  private adoptActiveRuns(runs: ActivePluginRun[]): void {
    for (const run of runs) {
      let metadata: PiMetadata;
      try {
        metadata = parsePiMetadata(run.metadata);
      } catch {
        continue;
      }
      const seq = run.seq;
      if (typeof seq !== "number" || !Number.isSafeInteger(seq) || seq < 0) {
        console.error(
          `attn-pi: not adopting run ${run.run_id} for session ${run.session_id}: driver.register carried no report cursor, so this session's state will not move until it is relaunched`,
        );
        continue;
      }
      const state: RunState = {
        token: run.run_id,
        sessionID: run.session_id,
        runID: run.run_id,
        seq,
        metadata,
      };
      this.runsByToken.set(state.token, state);
      this.runsBySessionID.set(state.sessionID, state);
      this.markUnbacked(state, "adopted from attn, waiting for its pi suite to re-dial");
    }
  }

  /** Starts the grace for a run nothing is declaring state for. On expiry the driver
   * says `unknown` rather than leaving a stale declaration standing. */
  private markUnbacked(run: RunState, why: string): void {
    this.markBacked(run);
    const timer = setTimeout(() => {
      run.unbacked = undefined;
      void this.declareUnbacked(run, why);
    }, this.unbackedGraceMs);
    // A pending alarm must not hold up the runtime's exit with its daemon connection.
    timer.unref?.();
    run.unbacked = timer;
  }

  private markBacked(run: RunState): void {
    if (run.unbacked === undefined) return;
    clearTimeout(run.unbacked);
    run.unbacked = undefined;
  }

  private async declareUnbacked(run: RunState, why: string): Promise<void> {
    console.error(
      `attn-pi: no pi suite for session ${run.sessionID} for ${this.unbackedGraceMs}ms (${why}); reporting unknown so attn stops showing a state nobody is refreshing`,
    );
    try {
      await this.rpc.request("session.report_state", {
        session_id: run.sessionID,
        run_id: run.runID,
        seq: this.nextSeq(run),
        state: "unknown",
      });
    } catch (error) {
      console.error(`attn-pi: could not report unknown for session ${run.sessionID}: ${String(error)}`);
    }
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

  // The auto-mode config travels in the environment, not argv: argv is
  // world-readable and prose entries are multi-line.
  private envFor(token: string, autoMode: unknown, sessionID: string): Record<string, string> {
    const env: Record<string, string> = { ATTN_PI_SUITE_SOCKET: this.relay.socketPath, ATTN_PI_TOKEN: token };
    if (autoMode !== undefined && autoMode !== null) {
      env.ATTN_PI_AUTOMODE_CONFIG = JSON.stringify(autoMode);
      const ledger = process.env.ATTN_AUTOMODE_DENIAL_LOG?.trim();
      if (ledger) env.ATTN_PI_AUTOMODE_DENIAL_LOG = ledger;
      env.ATTN_PI_SESSION_ID = sessionID;
    }
    return env;
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
  const dropped = record.dropped_reports;
  const piState = record.pi_state;
  if (piState !== undefined && piState !== "idle" && piState !== "working" && piState !== "pending_approval") {
    throw new Error(`suite.hello has unsupported pi_state ${String(piState)}`);
  }
  return {
    token: token.trim(),
    pi_session_id: piSessionID.trim(),
    pi_version: piVersion.trim(),
    reason,
    // A suite too old to send it says nothing, which is not the same as zero.
    // Only a positive count is reported.
    dropped_reports: typeof dropped === "number" && Number.isFinite(dropped) && dropped > 0 ? dropped : undefined,
    pi_state: piState,
  };
}

function parseRelayReportState(value: unknown): RelayReportStateParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_state params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_state is missing token");
  if (record.state !== "working" && record.state !== "pending_approval") {
    throw new Error(
      `suite.report_state state must be "working" or "pending_approval", got ${JSON.stringify(record.state)}`,
    );
  }
  return { token: token.trim(), state: record.state };
}

function parseRelayReportStop(value: unknown): RelayReportStopParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_stop params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const assistantText = record.assistant_text;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_stop is missing token");
  if (typeof assistantText !== "string") throw new Error("suite.report_stop is missing assistant_text");
  const aborted = record.aborted;
  if (aborted !== undefined && typeof aborted !== "boolean") throw new Error("suite.report_stop aborted must be a boolean");
  return { token: token.trim(), assistant_text: assistantText, aborted: aborted === true };
}

function parseRelayReportDenial(value: unknown): RelayReportDenialParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_denial params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_denial is missing token");
  const action = record.action;
  if (typeof action !== "string" || action.trim() === "") throw new Error("suite.report_denial is missing action");
  return {
    token: token.trim(),
    tool: textField(record.tool),
    action: action.trim(),
    reason: textField(record.reason),
    rule: textField(record.rule),
    at: textField(record.at),
  };
}

function parseRelayReportPullRequest(value: unknown): RelayReportPullRequestParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_pull_request params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const url = record.url;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_pull_request is missing token");
  if (typeof url !== "string" || url.trim() === "") throw new Error("suite.report_pull_request is missing url");
  return { token: token.trim(), url: url.trim() };
}

function textField(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function parseDeliverMessageParams(value: unknown): { session_id: string; run_id: string; input_id: string; text: string } {
  if (typeof value !== "object" || value === null) throw new Error("driver.deliver_message params must be an object");
  const record = value as Record<string, unknown>;
  const sessionID = record.session_id;
  const runID = record.run_id;
  const inputID = record.input_id;
  const text = record.text;
  if (typeof sessionID !== "string" || sessionID.trim() === "") throw new Error("driver.deliver_message is missing session_id");
  if (typeof runID !== "string" || runID.trim() === "") throw new Error("driver.deliver_message is missing run_id");
  if (typeof inputID !== "string" || inputID.trim() === "") throw new Error("driver.deliver_message is missing input_id");
  if (typeof text !== "string") throw new Error("driver.deliver_message is missing text");
  return { session_id: sessionID.trim(), run_id: runID.trim(), input_id: inputID.trim(), text };
}

function parseRelayReportInputTaken(value: unknown): RelayReportInputTakenParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_input_taken params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const inputID = record.input_id;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_input_taken is missing token");
  if (typeof inputID !== "string" || inputID.trim() === "") throw new Error("suite.report_input_taken is missing input_id");
  return { token: token.trim(), input_id: inputID.trim() };
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
