import { createServer } from "node:net";
import { join } from "node:path";
import { type EventSubscription, OpenCodeHTTP, parseModelPin, sessionModel, sessionModelMatches, variantForEffort } from "./opencode-http";
import { RunRegistry } from "./run-registry";
import {
  type DriverSpawnParams,
  type DriverSpawnResult,
  type LaunchConfig,
  type OpenCodeMetadata,
  type OpenCodeModel,
  type Report,
  type RunRecord,
  type SessionClosedParams,
  supportedOpenCodeVersions,
} from "./types";

const startupDeadlineMs = 20_000;
const reconnectDelayMs = 250;
const healthRequestTimeoutMs = 2_000;
const eventReconnectAttempts = 3;

export type AttnReporter = {
  request<T = unknown>(method: string, params?: unknown): Promise<T>;
};

type CommandResult = { exitCode: number; stdout: string; stderr: string };

export type OpenCodeDriverOptions = {
  rpc: AttnReporter;
  registry: RunRegistry;
  executable?: string;
  runCommand?: (argv: string[]) => Promise<CommandResult>;
  allocatePort?: () => Promise<number>;
  http?: (port: number, password: string) => OpenCodeHTTP;
  startupDeadline?: number;
  reconnectDelay?: number;
  healthRequestTimeout?: number;
};

type Availability =
  | { ok: true; executable: string; version: string }
  | { ok: false; message: string };

type LaunchSelection = {
  mode: "interactive" | "pinned";
  model?: OpenCodeModel;
  modelText?: string;
  variant?: string;
  nativeSessionID?: string;
};

type NativeBinding = {
  record: RunRecord;
  nativeID?: string;
  mode: LaunchSelection["mode"];
};

export class OpenCodeDriver {
  private readonly executable: string;
  private readonly runCommand: (argv: string[]) => Promise<CommandResult>;
  private readonly allocatePort: () => Promise<number>;
  private readonly http: (port: number, password: string) => OpenCodeHTTP;
  private readonly startupDeadline: number;
  private readonly reconnectDelay: number;
  private readonly healthRequestTimeout: number;
  private availability: Availability = { ok: false, message: "OpenCode has not been checked" };
  private readonly monitors = new Map<string, AbortController>();
  private readonly reportQueues = new Map<string, Promise<void>>();
  private readonly degraded = new Map<string, string>();

  constructor(private readonly options: OpenCodeDriverOptions) {
    this.executable = options.executable?.trim() || process.env.ATTN_OPENCODE_EXECUTABLE?.trim() || "opencode";
    this.runCommand = options.runCommand ?? runCommand;
    this.allocatePort = options.allocatePort ?? allocateLoopbackPort;
    this.http = options.http ?? ((port, password) => new OpenCodeHTTP({ port, password }));
    this.startupDeadline = options.startupDeadline ?? startupDeadlineMs;
    this.reconnectDelay = options.reconnectDelay ?? reconnectDelayMs;
    this.healthRequestTimeout = options.healthRequestTimeout ?? healthRequestTimeoutMs;
  }

  async initialize(): Promise<void> {
    await this.options.registry.initialize();
    await this.options.registry.pruneDead(async (record) => {
      try {
        const config = await this.options.registry.readLaunchConfig(record);
        const password = await this.options.registry.password(record);
        await this.healthWithin(this.http(config.port, password));
        return true;
      } catch {
        return false;
      }
    });
    await this.refreshAvailability();
    if (this.availability.ok) {
      await this.options.rpc.request("driver.register", {
        agent: "opencode",
        capabilities: {
          resume: true,
          yolo: true,
          initial_prompt: true,
          state_reporting: true,
          model_pin: true,
          effort_pin: true,
        },
      });
    }
  }

  health(): { ok: boolean; message: string } {
    if (!this.availability.ok) return { ok: false, message: this.availability.message };
    if (this.degraded.size > 0) {
      return { ok: false, message: [...this.degraded.values()].join("; ") };
    }
    return { ok: true, message: `OpenCode ${this.availability.version} is ready` };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    return this.launch(params, false);
  }

  async resume(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    return this.launch(params, true);
  }

  async sessionClosed(params: SessionClosedParams): Promise<{ ok: true }> {
    const controller = this.monitors.get(params.run_id);
    controller?.abort();
    this.monitors.delete(params.run_id);
    this.reportQueues.delete(params.run_id);
    this.degraded.delete(params.run_id);
    await this.options.registry.cleanup(params.run_id);
    return { ok: true };
  }

  private async launch(params: DriverSpawnParams, resume: boolean): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const selection = resume ? this.resumeSelection(params, availability.version) : this.freshSelection(params);
    const port = await this.allocatePort();
    let record: RunRecord | undefined;
    try {
      record = await this.options.registry.create({
        schema: 1,
        attn_session_id: requireText(params.session_id, "session_id"),
        run_id: requireText(params.run_id, "run_id"),
        next_seq: 1,
        port,
        opencode_session_id: selection.nativeSessionID,
        opencode_version: availability.version,
        pinned: selection.mode === "pinned",
        model: selection.modelText,
        variant: selection.model?.variant ?? selection.variant,
        resume,
        created_at: new Date().toISOString(),
      }, params.initial_prompt ?? "");
      const launchConfig: LaunchConfig = {
        schema: 1,
        run_id: record.run_id,
        executable: availability.executable,
        cwd: params.cwd,
        password_ref: record.password_ref,
        port,
        yolo: params.yolo === true,
        resume_session_id: selection.nativeSessionID,
      };
      await this.options.registry.writeLaunchConfig(record, launchConfig);
      const controller = new AbortController();
      this.monitors.set(record.run_id, controller);
      void this.monitor(record, selection, controller.signal);
      return {
        argv: [process.execPath, "run", join(import.meta.dir, "launcher.ts"), record.launch_config_ref],
        cwd: params.cwd,
      };
    } catch (error) {
      if (record) await this.options.registry.cleanup(record.run_id);
      throw error;
    }
  }

  private freshSelection(params: DriverSpawnParams): LaunchSelection {
    const modelText = params.model?.trim() ?? "";
    const effort = params.effort?.trim() ?? "";
    const stagedPrompt = params.initial_prompt?.trim() ?? "";
    if (!modelText && !effort && !stagedPrompt) return { mode: "interactive" };
    if (!modelText) throw new Error("OpenCode staged prompts require a delegated model pin");
    if (!effort) throw new Error("OpenCode pinned launches require an explicit effort pin");
    return {
      mode: "pinned",
      modelText,
      model: { ...parseModelPin(modelText), variant: variantForEffort(effort) },
    };
  }

  private resumeSelection(params: DriverSpawnParams, version: string): LaunchSelection {
    if (params.metadata === undefined || params.metadata === null || params.metadata === "") {
      // An interactive TUI has no native identity until its first normal user
      // prompt. A recovery attempt in that window must relaunch a fresh TUI,
      // not advertise a resume target that the plugin cannot name.
      return this.freshSelection(params);
    }
    const metadata = parseMetadata(params.metadata);
    const pinned = metadata.pinned ?? Boolean(metadata.model && metadata.variant);
    if (metadata.opencode_version !== version) {
      throw new Error(`OpenCode resume requires version ${metadata.opencode_version}, found ${version}`);
    }
    if (params.model && (!pinned || !metadata.model || params.model !== metadata.model)) {
      throw new Error("OpenCode resume model pin does not match the persisted native session");
    }
    if (params.effort && (!pinned || !metadata.variant || variantForEffort(params.effort) !== metadata.variant)) {
      throw new Error("OpenCode resume effort pin does not match the persisted native session");
    }
    const model = pinned && metadata.model && metadata.variant
      ? { ...parseModelPin(metadata.model), variant: metadata.variant }
      : undefined;
    return {
      mode: pinned ? "pinned" : "interactive",
      model,
      modelText: metadata.model,
      variant: metadata.variant,
      nativeSessionID: metadata.opencode_session_id,
    };
  }

  private async monitor(initialRecord: RunRecord, selection: LaunchSelection, signal: AbortSignal): Promise<void> {
    const binding: NativeBinding = {
      record: initialRecord,
      nativeID: initialRecord.opencode_session_id,
      mode: selection.mode,
    };
    const eventController = new AbortController();
    const abortEventsForSession = () => eventController.abort(signal.reason);
    if (signal.aborted) {
      abortEventsForSession();
    } else {
      signal.addEventListener("abort", abortEventsForSession, { once: true });
    }
    let subscription: EventSubscription | undefined;
    try {
      const client = await this.waitForHealthyServer(binding.record, signal);
      if (signal.aborted) return;

      subscription = client.subscribe((event) => this.handleEvent(client, binding, event, eventController.signal), eventController.signal);
      const setup = this.requestDeadline(signal);
      try {
        await this.awaitSubscriptionReady(subscription, setup.signal);

        if (binding.nativeID) {
          const nativeSession = await client.getSession(binding.nativeID, setup.signal);
          if (selection.model && !sessionModelMatches(nativeSession, selection.model)) {
            throw new Error("persisted OpenCode session model or variant no longer matches the delegated pins");
          }
          await this.reconcileStatus(binding.record, client, binding.nativeID, setup.signal);
          await client.selectSession(binding.nativeID, setup.signal);
        } else if (selection.mode === "pinned") {
          const model = selection.model!;
          const nativeID = await client.createSession(model, setup.signal);
          binding.nativeID = nativeID;
          binding.record = await this.options.registry.update(binding.record.run_id, { opencode_session_id: nativeID });
          await this.enqueueReport(binding.record, {
            kind: "metadata",
            metadata: {
              schema: 1,
              opencode_session_id: nativeID,
              opencode_version: binding.record.opencode_version,
              pinned: true,
              model: binding.record.model,
              variant: binding.record.variant,
            },
          });
          // A just-submitted prompt can be absent from /session/status until its
          // first explicit lifecycle event. Reconciliation therefore never infers
          // idle from absence.
          await this.reconcileStatus(binding.record, client, nativeID, setup.signal);
          await client.selectSession(nativeID, setup.signal);
          const prompt = await this.options.registry.prompt(binding.record);
          if (prompt !== "") await client.promptAsync(nativeID, prompt, model, setup.signal);
        }
      } finally {
        setup.dispose();
      }

      let establishedStreamFailures = 0;
      for (;;) {
        const activeSubscription = subscription;
        if (!activeSubscription) return;
        try {
          await activeSubscription.done;
          establishedStreamFailures = 0;
        } catch (error) {
          // OpenCode can close an otherwise healthy idle SSE stream. Reconnect
          // before degrading the run. Repeated established-stream failures are
          // terminal even when each replacement reached SSE readiness.
          if (signal.aborted) return;
          establishedStreamFailures += 1;
          if (establishedStreamFailures >= eventReconnectAttempts) throw error;
        }
        if (signal.aborted) return;
        let reconnectError: unknown;
        for (let attempt = 0; attempt < eventReconnectAttempts; attempt += 1) {
          await sleep(this.reconnectDelay, signal);
          if (signal.aborted) return;
          subscription = client.subscribe((event) => this.handleEvent(client, binding, event, eventController.signal), eventController.signal);
          const setup = this.requestDeadline(signal);
          try {
            await this.awaitSubscriptionReady(subscription, setup.signal);
            await this.reconcileStatus(binding.record, client, binding.nativeID, setup.signal);
            reconnectError = undefined;
            break;
          } catch (error) {
            subscription.abort();
            if (signal.aborted) return;
            reconnectError = error;
          } finally {
            setup.dispose();
          }
        }
        if (reconnectError) throw reconnectError;
      }
    } catch (error) {
      if (signal.aborted) return;
      // Setup failures are terminal for this run. Stop the event stream before
      // reporting degraded state so a late SSE event cannot overwrite it.
      eventController.abort(error);
      subscription?.abort();
      const message = `OpenCode run ${initialRecord.attn_session_id} is degraded: ${safeError(error)}`;
      this.degraded.set(initialRecord.run_id, message);
      try {
        await this.enqueueReport(binding.record, { kind: "state", state: "unknown" });
      } catch {
        // The run's failure is already visible through plugin health. Do not let
        // a disconnected daemon turn error handling into an unhandled rejection.
      }
    } finally {
      signal.removeEventListener("abort", abortEventsForSession);
      eventController.abort();
    }
  }

  private async waitForHealthyServer(record: RunRecord, signal: AbortSignal): Promise<OpenCodeHTTP> {
    const deadline = Date.now() + this.startupDeadline;
    let lastError: unknown = new Error("OpenCode server did not start");
    while (Date.now() < deadline && !signal.aborted) {
      try {
        const config = await this.options.registry.readLaunchConfig(record);
        if (config.port !== record.port) record = await this.options.registry.update(record.run_id, { port: config.port });
        const password = await this.options.registry.password(record);
        const client = this.http(record.port, password);
        const health = await this.healthWithin(client, signal, deadline - Date.now());
        if (health.version !== record.opencode_version || !supportedOpenCodeVersions.has(health.version)) {
          throw new Error(`OpenCode server version ${health.version} is not the verified ${record.opencode_version}`);
        }
        return client;
      } catch (error) {
        lastError = error;
        await sleep(this.reconnectDelay, signal);
      }
    }
    throw lastError;
  }

  private async reconcileStatus(record: RunRecord, client: OpenCodeHTTP, nativeID: string | undefined, signal?: AbortSignal): Promise<void> {
    if (!nativeID) return;
    const status = await client.statusFor(nativeID, signal);
    if (status === "busy" || status === "retry") {
      await this.enqueueReport(record, { kind: "state", state: "working" });
    } else if (status === "idle") {
      await this.enqueueReport(record, { kind: "stop", verdict: "idle" });
    }
  }

  private async handleEvent(client: OpenCodeHTTP, binding: NativeBinding, event: { type: string; sessionID?: string; status?: string }, parentSignal: AbortSignal): Promise<void> {
    if (parentSignal.aborted) return;
    if (!binding.nativeID && binding.mode === "interactive" && event.type === "session.created" && event.sessionID) {
      const request = this.requestDeadline(parentSignal);
      let nativeSession: unknown;
      try {
        nativeSession = await client.getSession(event.sessionID, request.signal);
      } finally {
        request.dispose();
      }
      if (parentSignal.aborted) return;
      const selectedModel = sessionModel(nativeSession);
      binding.nativeID = event.sessionID;
      binding.record = await this.options.registry.update(binding.record.run_id, {
        opencode_session_id: event.sessionID,
        pinned: false,
        model: selectedModel ? `${selectedModel.providerID}/${selectedModel.id}` : undefined,
        variant: selectedModel?.variant,
      });
      await this.enqueueReport(binding.record, {
        kind: "metadata",
        metadata: {
          schema: 1,
          opencode_session_id: event.sessionID,
          opencode_version: binding.record.opencode_version,
          pinned: false,
          ...(selectedModel ? { model: `${selectedModel.providerID}/${selectedModel.id}`, variant: selectedModel.variant } : {}),
        },
      });
      const statusRequest = this.requestDeadline(parentSignal);
      try {
        await this.reconcileStatus(binding.record, client, binding.nativeID, statusRequest.signal);
      } finally {
        statusRequest.dispose();
      }
    }
    if (parentSignal.aborted || !binding.nativeID || event.sessionID !== binding.nativeID) return;
    const record = binding.record;
    if (event.type === "session.status") {
      if (event.status === "busy" || event.status === "retry") {
        await this.enqueueReport(record, { kind: "state", state: "working" });
      } else if (event.status === "idle") {
        await this.enqueueReport(record, { kind: "stop", verdict: "idle" });
      }
    } else if (event.type === "session.idle") {
      await this.enqueueReport(record, { kind: "stop", verdict: "idle" });
    } else if (event.type === "session.error") {
      await this.enqueueReport(record, { kind: "state", state: "unknown" });
    }
  }

  private enqueueReport(record: RunRecord, report: Report): Promise<void> {
    const previous = this.reportQueues.get(record.run_id) ?? Promise.resolve();
    const next = previous.then(() => this.sendReport(record, report));
    this.reportQueues.set(record.run_id, next.catch(() => undefined));
    return next;
  }

  private async healthWithin(client: OpenCodeHTTP, parentSignal?: AbortSignal, limit = this.healthRequestTimeout): Promise<{ version: string }> {
    const request = this.requestDeadline(parentSignal, limit);
    try {
      return await client.health(request.signal);
    } finally {
      request.dispose();
    }
  }

  private requestDeadline(parentSignal?: AbortSignal, limit = this.healthRequestTimeout): { signal: AbortSignal; dispose: () => void } {
    if (limit <= 0) throw new Error("OpenCode server request deadline elapsed");
    const controller = new AbortController();
    const abortForParent = () => controller.abort(parentSignal?.reason);
    if (parentSignal?.aborted) {
      abortForParent();
    } else {
      parentSignal?.addEventListener("abort", abortForParent, { once: true });
    }
    const timer = setTimeout(() => controller.abort(), Math.min(limit, this.healthRequestTimeout));
    return {
      signal: controller.signal,
      dispose: () => {
        clearTimeout(timer);
        parentSignal?.removeEventListener("abort", abortForParent);
      },
    };
  }

  private async awaitSubscriptionReady(subscription: { ready: Promise<void>; abort: () => void }, signal: AbortSignal): Promise<void> {
    if (signal.aborted) {
      subscription.abort();
      throw new Error("OpenCode event subscription deadline elapsed");
    }
    await new Promise<void>((resolve, reject) => {
      const abort = () => {
        subscription.abort();
        reject(new Error("OpenCode event subscription deadline elapsed"));
      };
      signal.addEventListener("abort", abort, { once: true });
      subscription.ready.then(
        () => {
          signal.removeEventListener("abort", abort);
          resolve();
        },
        (error) => {
          signal.removeEventListener("abort", abort);
          reject(error);
        },
      );
    });
  }

  private async sendReport(record: RunRecord, report: Report): Promise<void> {
    const seq = await this.options.registry.reserveSequence(record.run_id);
    const base = { session_id: record.attn_session_id, run_id: record.run_id, seq };
    switch (report.kind) {
      case "metadata":
        await this.options.rpc.request("session.report_metadata", { ...base, metadata: report.metadata });
        return;
      case "state":
        await this.options.rpc.request("session.report_state", { ...base, state: report.state });
        return;
      case "stop":
        await this.options.rpc.request("session.report_stop", { ...base, verdict: report.verdict });
        return;
    }
  }

  private async refreshAvailability(): Promise<void> {
    try {
      const result = await this.runCommand([this.executable, "--version"]);
      const version = result.stdout.trim().replace(/^v/, "");
      if (result.exitCode !== 0) throw new Error(result.stderr.trim() || `exit ${result.exitCode}`);
      if (!supportedOpenCodeVersions.has(version)) {
        throw new Error(`version ${version || "(empty)"} is unsupported; verified versions: ${[...supportedOpenCodeVersions].join(", ")}`);
      }
      this.availability = { ok: true, executable: this.executable, version };
    } catch (error) {
      this.availability = { ok: false, message: `OpenCode executable ${this.executable} is unavailable: ${safeError(error)}` };
    }
  }

  private async requireAvailability(): Promise<Extract<Availability, { ok: true }>> {
    await this.refreshAvailability();
    if (!this.availability.ok) throw new Error(this.availability.message);
    return this.availability;
  }
}

async function runCommand(argv: string[]): Promise<CommandResult> {
  const child = Bun.spawn(argv, { stdout: "pipe", stderr: "pipe" });
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  return { stdout, stderr, exitCode };
}

async function allocateLoopbackPort(): Promise<number> {
  return new Promise<number>((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen({ host: "127.0.0.1", port: 0 }, () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close(() => reject(new Error("failed to reserve loopback port")));
        return;
      }
      server.close((error) => error ? reject(error) : resolve(address.port));
    });
  });
}

function parseMetadata(value: unknown): OpenCodeMetadata {
  const metadata = typeof value === "string" ? JSON.parse(value) : value;
  if (!metadata || typeof metadata !== "object") throw new Error("OpenCode resume requires persisted metadata");
  const result = metadata as Partial<OpenCodeMetadata>;
  if (result.schema !== 1 || !result.opencode_session_id || !result.opencode_version ||
    (result.pinned !== undefined && typeof result.pinned !== "boolean") ||
    (result.model !== undefined && typeof result.model !== "string") ||
    (result.variant !== undefined && typeof result.variant !== "string")) {
    throw new Error("OpenCode resume metadata is incomplete");
  }
  return result as OpenCodeMetadata;
}

function requireText(value: string | undefined, label: string): string {
  const normalized = value?.trim() ?? "";
  if (!normalized) throw new Error(`${label} is required`);
  return normalized;
}

function safeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function sleep(ms: number, signal: AbortSignal): Promise<void> {
  await new Promise<void>((resolve) => {
    const timeout = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(timeout);
      resolve();
    }, { once: true });
  });
}
