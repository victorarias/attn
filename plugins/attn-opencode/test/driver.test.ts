import { afterEach, describe, expect, test } from "bun:test";
import { randomUUID } from "node:crypto";
import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { OpenCodeDriver } from "../src/driver";
import attnGuidancePlugin from "../src/guidance-plugin";
import { launch, opencodeConfigForLaunch } from "../src/launcher";
import { type EventSubscription, type ServerEvent, OpenCodeHTTP } from "../src/opencode-http";
import { RunRegistry, writePrivate } from "../src/run-registry";
import type { ClassifierInput, DriverSpawnParams, StopClassifier, StopVerdict } from "../src/types";
import { FakeOpenCode, eventually } from "./fake-opencode";

const servers: FakeOpenCode[] = [];
afterEach(() => {
  for (const server of servers.splice(0)) server.close();
});

class RecordingRPC {
  readonly calls: Array<{ method: string; params: unknown }> = [];
  constructor(readonly activeRuns: Array<{ session_id: string; run_id: string; metadata?: unknown }> = []) {}

  async request<T = unknown>(method: string, params?: unknown): Promise<T> {
    this.calls.push({ method, params });
    return { ok: true, active_runs: method === "driver.register" ? this.activeRuns : undefined } as T;
  }
}

class ControlledEventClient extends OpenCodeHTTP {
  readonly subscriptions: Array<{ reject: (error: Error) => void; resolve: () => void }> = [];

  override subscribe(_onEvent: (event: ServerEvent) => Promise<void> | void, parentSignal?: AbortSignal): EventSubscription {
    let resolve!: () => void;
    let reject!: (error: Error) => void;
    const done = new Promise<void>((nextResolve, nextReject) => {
      resolve = nextResolve;
      reject = nextReject;
    });
    const abort = () => resolve();
    if (parentSignal?.aborted) {
      abort();
    } else {
      parentSignal?.addEventListener("abort", abort, { once: true });
    }
    this.subscriptions.push({ reject, resolve });
    return { ready: Promise.resolve(), done, abort };
  }

  failLatest(): void {
    this.subscriptions.at(-1)?.reject(new Error("event stream dropped"));
  }
}

function server(password: string, version?: string): FakeOpenCode {
  const result = new FakeOpenCode(password, version);
  servers.push(result);
  return result;
}

function params(runID = "run-one"): DriverSpawnParams {
  return {
    session_id: `attn-${runID}`,
    run_id: runID,
    cwd: "/tmp",
    model: "spotify-glm/zai-org/GLM-5.2-FP8",
    effort: "max",
    initial_prompt: "Say GLM_SPIKE_OK",
  };
}

function assistantMessage(id: string, text: string, completed = Date.now(), variant = "max"): unknown {
  return {
    info: {
      id,
      role: "assistant",
      time: { completed },
      model: { providerID: "spotify-glm", modelID: "zai-org/GLM-5.2-FP8", variant },
    },
    parts: [{ type: "text", text }],
  };
}

class BlockingClassifier implements StopClassifier {
  readonly inputs: ClassifierInput[] = [];
  aborted = false;
  private resolve?: (verdict: StopVerdict) => void;

  classify(input: ClassifierInput): Promise<StopVerdict> {
    this.inputs.push(input);
    return new Promise((resolve) => {
      this.resolve = resolve;
      input.signal.addEventListener("abort", () => {
        this.aborted = true;
        resolve("unknown");
      }, { once: true });
    });
  }

  finish(verdict: StopVerdict): void {
    this.resolve?.(verdict);
  }
}

function driverFor(rpc: RecordingRPC, registry: RunRegistry, target: FakeOpenCode): OpenCodeDriver {
  return new OpenCodeDriver({
    rpc,
    registry,
    runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
    allocatePort: async () => target.port,
    http: (port, password) => new OpenCodeHTTP({ port, password }),
    startupDeadline: 300,
    reconnectDelay: 5,
  });
}

describe("OpenCode server-backed driver", () => {
  test("accepts the minimum and newer stable OpenCode versions", async () => {
    for (const [version, normalized] of [
      ["1.17.16", "1.17.16"],
      ["v1.17.16", "1.17.16"],
      ["1.17.18", "1.17.18"],
      ["1.18.0", "1.18.0"],
      ["2.0.0", "2.0.0"],
    ]) {
      const rpc = new RecordingRPC();
      const registry = new RunRegistry(join(await tempRoot(), "runtime"));
      const driver = new OpenCodeDriver({
        rpc,
        registry,
        runCommand: async () => ({ exitCode: 0, stdout: `${version}\n`, stderr: "" }),
      });
      await driver.initialize();
      expect(driver.health()).toEqual({ ok: true, message: `OpenCode ${normalized} is ready` });
      expect(rpc.calls[0]?.method).toBe("driver.register");
      expect((rpc.calls[0]?.params as { capabilities: { classifier: boolean } }).capabilities.classifier).toBe(true);
    }
  });

  test("rejects old, malformed, and prerelease OpenCode versions before registering", async () => {
    for (const [version, message] of [
      ["1.17.15", "requires OpenCode >= 1.17.16"],
      ["1.17", "expected a stable MAJOR.MINOR.PATCH release"],
      ["1.18.0-beta.1", "expected a stable MAJOR.MINOR.PATCH release"],
    ]) {
      const rpc = new RecordingRPC();
      const registry = new RunRegistry(join(await tempRoot(), "runtime"));
      const driver = new OpenCodeDriver({
        rpc,
        registry,
        runCommand: async () => ({ exitCode: 0, stdout: `${version}\n`, stderr: "" }),
      });
      await driver.initialize();
      expect(driver.health()).toEqual(expect.objectContaining({ ok: false }));
      expect(driver.health().message).toContain(message);
      expect(rpc.calls).toHaveLength(0);
    }
  });

  test("uses per-run Basic authentication on every HTTP endpoint", async () => {
    const target = server("correct-password");
    await expect(new OpenCodeHTTP({ port: target.port, password: "wrong-password" }).health()).rejects.toThrow("HTTP 401");
    const client = new OpenCodeHTTP({ port: target.port, password: "correct-password" });
    await expect(client.health()).resolves.toEqual({ version: "1.17.18" });
    target.pendingQuestions.set("question-auth", "native-auth");
    await expect(client.pendingAttentionFor("native-auth")).resolves.toBe("question");
    expect(target.requests).toHaveLength(4);
    expect(target.requests.slice(1).every((request) => request.authorization?.startsWith("Basic "))).toBe(true);
  });

  test("creates a pinned native session after authenticated SSE subscription and does not infer idle from a missing status", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    expect(rpc.calls[0]?.method).toBe("driver.register");

    const launch = await driver.spawn(params());
    expect(launch.argv.slice(0, 3)).toEqual([process.execPath, "run", expect.stringContaining("launcher.ts")]);
    await eventually(
      () => target.prompts.length === 1,
      () => `staged prompt submission; health=${JSON.stringify(driver.health())}; requests=${JSON.stringify(target.requests)}; calls=${JSON.stringify(rpc.calls)}`,
    );
    expect(target.requests.some((request) => request.path === "/event")).toBe(true);
    expect(target.requests.filter((request) => request.path === "/session/status")).toHaveLength(1);
    expect(rpc.calls.map((call) => call.method)).toEqual(["driver.register", "session.report_metadata"]);

    const created = target.sessions.get("native-1") as { model: { providerID: string; id: string; variant: string } };
    expect(created.model).toEqual({ providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "max" });
    expect(target.prompts[0]?.body).toEqual({
      parts: [{ type: "text", text: "Say GLM_SPIKE_OK" }],
      model: { providerID: "spotify-glm", modelID: "zai-org/GLM-5.2-FP8" },
      variant: "max",
    });

    target.emit("session.status", { sessionID: "native-1", status: { type: "busy" } });
    target.emit("session.status", { sessionID: "native-1", status: { type: "retry" } });
    target.emit("session.status", { sessionID: "native-1", status: { type: "idle" } });
    await eventually(() => rpc.calls.length === 5, "ordered lifecycle reports");
    expect(rpc.calls.slice(1).map((call) => call.method)).toEqual([
      "session.report_metadata",
      "session.report_state",
      "session.report_state",
      "session.report_stop",
    ]);
    const sequences = rpc.calls.slice(1).map((call) => (call.params as { seq: number }).seq);
    expect(sequences).toEqual([1, 2, 3, 4]);
  });

  test("uses the standalone executable as its launcher without requiring Bun", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      standaloneLauncher: true,
      guidancePluginRef: "file:///Applications/attn.app/Contents/Resources/plugins/attn-opencode/guidance-plugin.js",
    });
    await driver.initialize();

    const launch = await driver.spawn(params("run-standalone-launcher"));
    expect(launch.argv).toEqual([
      process.execPath,
      "--attn-opencode-launcher",
      expect.stringContaining("run-standalone-launcher.json"),
    ]);
    expect(launch.argv).not.toContain("run");
    expect(launch.env).toEqual({
      ATTN_OPENCODE_GUIDANCE_PLUGIN_REF: "file:///Applications/attn.app/Contents/Resources/plugins/attn-opencode/guidance-plugin.js",
    });
  });

  test("maps native question and permission events for only the linked session", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-attention-events"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");

    const reportsBeforeOtherSession = rpc.calls.length;
    target.askQuestion("native-other", "question-other");
    await Bun.sleep(20);
    expect(rpc.calls).toHaveLength(reportsBeforeOtherSession);

    target.askQuestion("native-1", "question-1", "question.v2.asked");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "waiting_input",
      "question waiting-input report",
    );
    target.replyQuestion("native-1", "question-1", "question.v2.replied");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "working",
      "question reply working report",
    );
    target.askPermission("native-1", "permission-1", "permission.v2.asked");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "pending_approval",
      "permission pending-approval report",
    );
    target.replyPermission("native-1", "permission-1", "permission.v2.replied");
    await eventually(
      () => rpc.calls.filter((call) => (call.params as { state?: string }).state === "working").length === 2,
      "permission reply working report",
    );

    const attentionReports = rpc.calls.slice(reportsBeforeOtherSession);
    expect(attentionReports.map((call) => (call.params as { state?: string }).state)).toEqual([
      "waiting_input",
      "working",
      "pending_approval",
      "working",
    ]);
    expect(attentionReports.map((call) => (call.params as { seq: number }).seq)).toEqual([2, 3, 4, 5]);
  });

  test("keeps reporting attention when another native request remains after a resolution", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-overlapping-attention"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");

    target.askPermission("native-1", "permission-overlap");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "pending_approval",
      "initial permission report",
    );
    target.askQuestion("native-1", "question-overlap");
    await eventually(
      () => rpc.calls.filter((call) => (call.params as { state?: string }).state === "pending_approval").length === 2,
      "permission remains authoritative after overlapping question",
    );

    target.replyPermission("native-1", "permission-overlap");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "waiting_input",
      "remaining question after permission reply",
    );

    target.askPermission("native-1", "permission-overlap-2");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "pending_approval",
      "second overlapping permission report",
    );
    target.replyQuestion("native-1", "question-overlap");
    await eventually(
      () => rpc.calls.filter((call) => (call.params as { state?: string }).state === "pending_approval").length === 4,
      "remaining permission after question reply",
    );

    target.replyPermission("native-1", "permission-overlap-2");
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "working",
      "working after all native attention resolves",
    );
  });

  test("checks pending native attention before reporting an explicit idle event", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-idle-attention"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");

    target.pendingQuestions.set("question-idle", "native-1");
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "waiting_input",
      "idle with pending question",
    );
    expect(rpc.calls.some((call) => call.method === "session.report_stop")).toBe(false);
    expect(target.classifierPrompts).toHaveLength(0);

    target.pendingQuestions.clear();
    target.pendingPermissions.set("permission-idle", "native-1");
    target.emit("session.status", { sessionID: "native-1", status: { type: "idle" } });
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "pending_approval",
      "idle with pending permission",
    );
    expect(rpc.calls.some((call) => call.method === "session.report_stop")).toBe(false);
    expect(target.classifierPrompts).toHaveLength(0);

    target.pendingPermissions.clear();
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => rpc.calls.at(-1)?.method === "session.report_stop", "idle without pending attention");
    expect((rpc.calls.at(-1)?.params as { verdict: string }).verdict).toBe("idle");
    expect(target.classifierPrompts).toHaveLength(0);
  });

  test("classifies prose-only questions and completed answers in deleted hidden sessions", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-prose-verdicts"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");

    target.messages.set("native-1", [assistantMessage("message-question", "Should I continue?")]);
    target.classifierReplies.push('{"verdict":"WAITING"}');
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(
      () => (rpc.calls.at(-1)?.params as { verdict?: string }).verdict === "waiting_input",
      "prose question verdict",
    );

    target.messages.set("native-1", [assistantMessage("message-done", "The task is complete.", Date.now() + 1)]);
    target.classifierReplies.push('{"verdict":"DONE"}');
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(
      () => rpc.calls.filter((call) => call.method === "session.report_stop").length === 2,
      "completed prose verdict",
    );

    expect(rpc.calls.filter((call) => call.method === "session.report_stop").map((call) =>
      (call.params as { verdict: string }).verdict)).toEqual(["waiting_input", "idle"]);
    expect(target.classifierPrompts).toHaveLength(2);
    expect(target.deletedSessions).toEqual(["native-2", "native-3"]);
    expect(target.requests.filter((request) => request.path === "/tui/select-session").map((request) => request.body)).toEqual([
      { sessionID: "native-1" },
    ]);
  });

  test("reuses a cached message verdict while reserving a fresh report sequence", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-prose-cache"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");
    target.messages.set("native-1", [assistantMessage("same-message", "Which option should I use?")]);
    target.classifierReplies.push('{"verdict":"WAITING"}');

    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => target.classifierPrompts.length === 1 && rpc.calls.at(-1)?.method === "session.report_stop", "initial classification");
    target.classifierReplies.push('{"verdict":"DONE"}');
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => rpc.calls.filter((call) => call.method === "session.report_stop").length === 2, "cached classification");

    const reports = rpc.calls.filter((call) => call.method === "session.report_stop");
    expect(reports.map((call) => (call.params as { verdict: string }).verdict)).toEqual(["waiting_input", "waiting_input"]);
    expect(reports.map((call) => (call.params as { seq: number }).seq)).toEqual([2, 3]);
    expect(target.classifierPrompts).toHaveLength(1);
    expect((await registry.get("run-prose-cache"))?.last_classification).toEqual(expect.objectContaining({
      messageID: "same-message",
      verdict: "waiting_input",
    }));
  });

  test("reports unknown for malformed classifier output", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-prose-malformed"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");
    target.messages.set("native-1", [assistantMessage("message-malformed", "Can you choose?")]);
    target.classifierReplies.push("WAITING");
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => rpc.calls.at(-1)?.method === "session.report_stop", "unknown prose verdict");
    expect((rpc.calls.at(-1)?.params as { verdict: string }).verdict).toBe("unknown");
  });

  test("reports unknown when native-attention reconciliation fails", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-idle-reconcile-failure"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");
    target.failPendingLists = true;
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => rpc.calls.at(-1)?.method === "session.report_stop", "unknown reconciliation verdict");
    expect((rpc.calls.at(-1)?.params as { verdict: string }).verdict).toBe("unknown");
    expect(target.classifierPrompts).toHaveLength(0);
  });

  for (const newerEvent of ["busy", "permission"] as const) {
    test(`lets a newer ${newerEvent} event cancel and overtake reserved classification`, async () => {
      const rpc = new RecordingRPC();
      const target = server("*");
      const registry = new RunRegistry(join(await tempRoot(), "runtime"));
      const classifier = new BlockingClassifier();
      const driver = new OpenCodeDriver({
        rpc,
        registry,
        runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
        allocatePort: async () => target.port,
        http: (port, password) => new OpenCodeHTTP({ port, password }),
        stopClassifier: () => classifier,
        startupDeadline: 300,
        reconnectDelay: 5,
      });
      await driver.initialize();
      await driver.spawn(params(`run-cancel-${newerEvent}`));
      await eventually(() => target.prompts.length === 1, "native prompt submission");
      target.messages.set("native-1", [assistantMessage("message-blocked", "What next?")]);
      target.emit("session.idle", { sessionID: "native-1" });
      await eventually(() => classifier.inputs.length === 1, "classifier started");

      if (newerEvent === "busy") {
        target.emit("session.status", { sessionID: "native-1", status: { type: "busy" } });
      } else {
        target.askPermission("native-1", "newer-permission");
      }
      const expectedState = newerEvent === "busy" ? "working" : "pending_approval";
      await eventually(() => (rpc.calls.at(-1)?.params as { state?: string }).state === expectedState, "newer state report");
      expect(classifier.aborted).toBe(true);
      classifier.finish("waiting_input");
      await Bun.sleep(20);
      expect(rpc.calls.filter((call) => call.method === "session.report_stop")).toHaveLength(0);
      expect((rpc.calls.at(-1)?.params as { seq: number }).seq).toBe(3);
    });
  }

  test("does not cancel classification for benign linked-session updates after idle", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const classifier = new BlockingClassifier();
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      stopClassifier: () => classifier,
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await driver.spawn(params("run-benign-session-update"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");
    target.messages.set("native-1", [assistantMessage("message-benign", "Should I continue?")]);
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => classifier.inputs.length === 1, "classifier started");
    target.emit("session.updated", { sessionID: "native-1", title: "updated" });
    await Bun.sleep(20);
    expect(classifier.aborted).toBe(false);
    classifier.finish("waiting_input");
    await eventually(() => (rpc.calls.at(-1)?.params as { verdict?: string }).verdict === "waiting_input", "completed classification");
  });

  test("reconciles a missed pending question after the SSE stream reconnects", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-question-reconnect"));
    await eventually(() => target.prompts.length === 1 && target.eventSubscriberCount === 1, "initial native run");

    target.pendingQuestions.set("question-missed", "native-1");
    target.closeEvents();
    await eventually(
      () => target.requests.filter((request) => request.path === "/event").length >= 2 &&
        (rpc.calls.at(-1)?.params as { state?: string }).state === "waiting_input",
      "reconnected pending-question reconciliation",
    );
    expect(driver.health()).toEqual({ ok: true, message: "OpenCode 1.17.18 is ready" });
  });

  test("applies events buffered during reconnect after the older pending-request snapshot", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-question-reconnect-order"));
    await eventually(() => target.prompts.length === 1 && target.eventSubscriberCount === 1, "initial native run");

    target.pendingQuestions.set("question-race", "native-1");
    let releaseSnapshot!: () => void;
    target.pendingListBarrier = new Promise<void>((resolve) => {
      releaseSnapshot = resolve;
    });
    target.closeEvents();
    await eventually(
      () => target.requests.filter((request) => request.path === "/question").length >= 2 &&
        target.requests.filter((request) => request.path === "/permission").length >= 2 &&
        target.eventSubscriberCount === 1,
      "reconnect snapshot in flight",
    );

    target.replyQuestion("native-1", "question-race");
    releaseSnapshot();
    await eventually(
      () => (rpc.calls.at(-1)?.params as { state?: string }).state === "working",
      "buffered question reply after stale snapshot",
    );
    const states = rpc.calls
      .filter((call) => call.method === "session.report_state")
      .map((call) => (call.params as { state: string }).state);
    expect(states.slice(-2)).toEqual(["waiting_input", "working"]);
  });

  test("lets an unpinned interactive launch use OpenCode defaults and records its first native session", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();

    await driver.spawn({
      session_id: "attn-interactive",
      run_id: "run-interactive",
      cwd: "/tmp",
    });
    await eventually(() => target.requests.some((request) => request.path === "/event"), "interactive SSE subscription");
    expect(target.requests.some((request) => request.path === "/session" && request.method === "POST")).toBe(false);
    expect(target.prompts).toHaveLength(0);

    target.sessions.set("native-default", {
      id: "native-default",
      model: { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "max" },
    });
    target.emit("session.created", { sessionID: "native-default" });
    await eventually(
      () => rpc.calls.some((call) => call.method === "session.report_metadata"),
      "interactive native session metadata",
    );

    const record = await registry.get("run-interactive");
    expect(record).toEqual(expect.objectContaining({
      opencode_session_id: "native-default",
      pinned: false,
      model: "spotify-glm/zai-org/GLM-5.2-FP8",
      variant: "max",
    }));
    expect(rpc.calls.find((call) => call.method === "session.report_metadata")?.params).toEqual(expect.objectContaining({
      metadata: {
        schema: 1,
        opencode_session_id: "native-default",
        opencode_version: "1.17.18",
        pinned: false,
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "max",
      },
    }));

    target.emit("session.status", { sessionID: "native-default", status: { type: "busy" } });
    target.emit("session.status", { sessionID: "native-default", status: { type: "idle" } });
    await eventually(() => rpc.calls.filter((call) => call.method === "session.report_stop").length === 1, "interactive idle report");
    expect(rpc.calls.slice(1).map((call) => call.method)).toEqual([
      "session.report_metadata",
      "session.report_state",
      "session.report_stop",
    ]);
  });

  test("keeps server-side pinning for staged prompts", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();

    await expect(driver.spawn({
      session_id: "attn-missing-pin",
      run_id: "run-missing-pin",
      cwd: "/tmp",
      initial_prompt: "This must remain bound to a selected model",
    })).rejects.toThrow("staged prompts require a delegated model pin");
  });

  test("resumes only the persisted native identity and metadata pins", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    target.sessions.set("native-resume", {
      id: "native-resume",
      model: { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "low" },
    });
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.resume({
      ...params("run-resume"),
      effort: "low",
      initial_prompt: "",
      metadata: {
        schema: 1,
        opencode_session_id: "native-resume",
        opencode_version: "1.17.18",
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "low",
      },
    });
    await eventually(() => target.requests.some((request) => request.path === "/tui/select-session"), "resume selection");
    expect(target.prompts).toHaveLength(0);
    expect(target.requests.some((request) => request.path === "/session/native-resume")).toBe(true);

    await expect(driver.resume({
      ...params("bad-resume"),
      effort: "max",
      metadata: {
        schema: 1,
        opencode_session_id: "native-resume",
        opencode_version: "1.17.18",
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "low",
      },
    })).rejects.toThrow("effort pin does not match");
  });

  test("resumes after a forward OpenCode upgrade and records the new run version", async () => {
    const rpc = new RecordingRPC();
    const target = server("*", "1.18.0");
    target.sessions.set("native-upgrade", {
      id: "native-upgrade",
      model: { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "low" },
    });
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.18.0\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await driver.resume({
      ...params("run-forward-upgrade"),
      effort: "low",
      initial_prompt: "",
      metadata: {
        schema: 1,
        opencode_session_id: "native-upgrade",
        opencode_version: "1.17.18",
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "low",
      },
    });
    await eventually(() => target.requests.some((request) => request.path === "/tui/select-session"), "upgraded resume selection");
    expect(await registry.get("run-forward-upgrade")).toEqual(expect.objectContaining({
      opencode_session_id: "native-upgrade",
      opencode_version: "1.18.0",
      resume: true,
    }));
  });

  test("rejects an OpenCode downgrade before creating a run", async () => {
    const rpc = new RecordingRPC();
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
    });
    await driver.initialize();
    await expect(driver.resume({
      ...params("run-downgrade"),
      initial_prompt: "",
      metadata: {
        schema: 1,
        opencode_session_id: "native-newer",
        opencode_version: "1.18.0",
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "max",
      },
    })).rejects.toThrow("OpenCode resume would downgrade 1.18.0 to 1.17.18");
    expect(await registry.get("run-downgrade")).toBeUndefined();
  });

  test("keeps exact per-run server version identity on newer releases", async () => {
    const rpc = new RecordingRPC();
    const target = server("*", "1.18.1");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.18.0\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      startupDeadline: 40,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await driver.spawn({ session_id: "attn-version-mismatch", run_id: "run-version-mismatch", cwd: "/tmp" });
    await eventually(
      () => rpc.calls.some((call) => (call.params as { state?: string }).state === "unknown"),
      "version identity failure report",
    );
    expect(driver.health().message).toContain("server version 1.18.1 does not match this run's expected 1.18.0");
  });

  test("resumes an interactive OpenCode session without imposing pins", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    target.sessions.set("native-default", {
      id: "native-default",
      model: { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "low" },
    });
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();

    await driver.resume({
      session_id: "attn-interactive-resume",
      run_id: "run-interactive-resume",
      cwd: "/tmp",
      metadata: {
        schema: 1,
        opencode_session_id: "native-default",
        opencode_version: "1.17.18",
        pinned: false,
        // OpenCode owns model changes for this kind of session. These values
        // are historical observation, not delegated constraints.
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "max",
      },
    });
    await eventually(() => target.requests.some((request) => request.path === "/tui/select-session"), "interactive resume selection");
    expect(target.requests.some((request) => request.path === "/session" && request.method === "POST")).toBe(false);
    expect(await registry.get("run-interactive-resume")).toEqual(expect.objectContaining({
      opencode_session_id: "native-default",
      pinned: false,
      model: "spotify-glm/zai-org/GLM-5.2-FP8",
      variant: "max",
      resume: true,
    }));
  });

  test("relaunches an unbound interactive session instead of claiming it can resume", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();

    await driver.resume({
      session_id: "attn-unbound-recovery",
      run_id: "run-unbound-recovery",
      cwd: "/tmp",
    });
    await eventually(() => target.eventSubscriberCount === 1, "fresh interactive recovery SSE subscription");
    expect(target.requests.some((request) => request.path === "/session" && request.method === "POST")).toBe(false);
  });

  test("keeps two concurrent ports, passwords, identities, and variants isolated", async () => {
    const first = server("*");
    const second = server("*");
    const rpc = new RecordingRPC();
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    let nextPort = 0;
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => [first.port, second.port][nextPort++]!,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await Promise.all([
      driver.spawn({ ...params("run-low"), effort: "low", initial_prompt: "first" }),
      driver.spawn({ ...params("run-max"), effort: "max", initial_prompt: "second" }),
    ]);
    await eventually(() => first.prompts.length === 1 && second.prompts.length === 1, "both isolated prompt submissions");
    const firstModel = (first.sessions.get("native-1") as { model: { variant: string } }).model;
    const secondModel = (second.sessions.get("native-1") as { model: { variant: string } }).model;
    expect([firstModel.variant, secondModel.variant].sort()).toEqual(["low", "max"]);
    const low = await registry.get("run-low");
    const max = await registry.get("run-max");
    expect(low?.port).not.toBe(max?.port);
    expect(low?.password_ref).not.toBe(max?.password_ref);
    expect(await registry.password(low!)).not.toBe(await registry.password(max!));
  });

  test("recovers only attn-owned runs and resumes monitoring without creating a native session", async () => {
    const target = server("*");
    target.sessions.set("native-recovery", {
      id: "native-recovery",
      model: { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "max" },
    });
    target.statuses.set("native-recovery", "busy");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    await registry.initialize();

    const seed = async (runID: string, sessionID: string, nativeID: string) => {
      const record = await registry.create({
        schema: 1,
        attn_session_id: sessionID,
        run_id: runID,
        next_seq: 7,
        port: target.port,
        opencode_session_id: nativeID,
        opencode_version: "1.17.18",
        pinned: true,
        model: "spotify-glm/zai-org/GLM-5.2-FP8",
        variant: "max",
        resume: false,
        created_at: new Date().toISOString(),
      }, "");
      await registry.writeLaunchConfig(record, {
        schema: 1,
        run_id: runID,
        executable: "opencode",
        cwd: "/tmp",
        password_ref: record.password_ref,
        port: target.port,
        yolo: false,
        resume_session_id: nativeID,
      });
    };
    await seed("run-recovery", "attn-recovery", "native-recovery");
    await seed("run-orphan", "attn-orphan", "native-orphan");

    const rpc = new RecordingRPC([{
      session_id: "attn-recovery",
      run_id: "run-recovery",
      metadata: { schema: 1, opencode_session_id: "native-recovery", opencode_version: "1.17.18", pinned: true },
    }]);
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();

    await eventually(
      () => rpc.calls.some((call) => call.method === "session.report_state" && (call.params as { seq?: number }).seq === 7),
      "recovered ordered state report",
    );
    expect(await registry.get("run-orphan")).toBeUndefined();
    expect(await registry.get("run-recovery")).toBeDefined();
    expect([...target.sessions.keys()]).toEqual(["native-recovery"]);
    expect(target.requests.filter((request) => request.path === "/tui/select-session").map((request) => request.body)).toEqual([
      { sessionID: "native-recovery" },
    ]);
    await driver.sessionClosed({ session_id: "attn-recovery", run_id: "run-recovery", reason: "test cleanup" });
  });

  test("keeps classifier sessions and equal-looking caches isolated across two runs", async () => {
    const first = server("*");
    const second = server("*");
    const rpc = new RecordingRPC();
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    let nextPort = 0;
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => [first.port, second.port][nextPort++]!,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await Promise.all([driver.spawn(params("run-classifier-first")), driver.spawn(params("run-classifier-second"))]);
    await eventually(() => first.prompts.length === 1 && second.prompts.length === 1, "both native runs");
    first.messages.set("native-1", [assistantMessage("same-id", "Same text?")]);
    second.messages.set("native-1", [assistantMessage("same-id", "Same text?")]);
    first.classifierReplies.push('{"verdict":"WAITING"}');
    second.classifierReplies.push('{"verdict":"DONE"}');
    first.emit("session.idle", { sessionID: "native-1" });
    second.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => rpc.calls.filter((call) => call.method === "session.report_stop").length === 2, "both prose verdicts");

    const verdictByRun = Object.fromEntries(rpc.calls.filter((call) => call.method === "session.report_stop").map((call) => {
      const params = call.params as { run_id: string; verdict: string };
      return [params.run_id, params.verdict];
    }));
    expect(verdictByRun).toEqual({ "run-classifier-first": "waiting_input", "run-classifier-second": "idle" });
    expect(first.classifierPrompts).toHaveLength(1);
    expect(second.classifierPrompts).toHaveLength(1);
    expect(first.deletedSessions).toEqual(["native-2"]);
    expect(second.deletedSessions).toEqual(["native-2"]);
    expect((await registry.get("run-classifier-first"))?.last_classification?.verdict).toBe("waiting_input");
    expect((await registry.get("run-classifier-second"))?.last_classification?.verdict).toBe("idle");
  });

  test("bounds prose classification and reports unknown on timeout", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const classifier = new BlockingClassifier();
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      stopClassifier: () => classifier,
      classifierTimeout: 5,
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await driver.spawn(params("run-classifier-timeout"));
    await eventually(() => target.prompts.length === 1, "native prompt submission");
    target.messages.set("native-1", [assistantMessage("message-timeout", "Are you there?")]);
    target.emit("session.idle", { sessionID: "native-1" });
    await eventually(() => (rpc.calls.at(-1)?.params as { verdict?: string }).verdict === "unknown", "timed-out unknown verdict");
    expect(classifier.aborted).toBe(true);
  });

  test("reports degraded health and unknown when the authenticated server never becomes ready", async () => {
    const rpc = new RecordingRPC();
    const target = server("*", "1.17.17");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn(params("run-unhealthy"));
    await eventually(() => rpc.calls.some((call) => call.method === "session.report_state"), "unknown failure report");
    expect(driver.health()).toEqual(expect.objectContaining({ ok: false }));
    expect(rpc.calls.at(-1)).toEqual(expect.objectContaining({ method: "session.report_state" }));
    expect((rpc.calls.at(-1)?.params as { state: string }).state).toBe("unknown");
  });

  test("bounds a health request when a loopback server accepts but never responds", async () => {
    const hanging = Bun.serve({
      port: 0,
      hostname: "127.0.0.1",
      fetch: () => new Promise<Response>(() => {}),
    });
    try {
      const rpc = new RecordingRPC();
      const registry = new RunRegistry(join(await tempRoot(), "runtime"));
      const driver = new OpenCodeDriver({
        rpc,
        registry,
        runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
        allocatePort: async () => hanging.port,
        http: (port, password) => new OpenCodeHTTP({ port, password }),
        startupDeadline: 40,
        reconnectDelay: 5,
        healthRequestTimeout: 5,
      });
      await driver.initialize();
      await driver.spawn(params("run-hung-health"));
      await eventually(
        () => rpc.calls.some((call) => call.method === "session.report_state"),
        "bounded failed health report",
      );
      expect(driver.health()).toEqual(expect.objectContaining({ ok: false }));
    } finally {
      hanging.stop(true);
    }
  });

  test("bounds event subscription readiness after health succeeds", async () => {
    const hanging = Bun.serve({
      port: 0,
      hostname: "127.0.0.1",
      fetch: (request) => {
        const path = new URL(request.url).pathname;
        if (path === "/global/health") return Response.json({ healthy: true, version: "1.17.18" });
        if (path === "/event") return new Promise<Response>(() => {});
        return new Response("not found", { status: 404 });
      },
    });
    try {
      const rpc = new RecordingRPC();
      const registry = new RunRegistry(join(await tempRoot(), "runtime"));
      const driver = new OpenCodeDriver({
        rpc,
        registry,
        runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
        allocatePort: async () => hanging.port,
        http: (port, password) => new OpenCodeHTTP({ port, password }),
        startupDeadline: 100,
        reconnectDelay: 5,
        healthRequestTimeout: 5,
      });
      await driver.initialize();
      await driver.spawn(params("run-hung-events"));
      await eventually(
        () => rpc.calls.some((call) => call.method === "session.report_state"),
        "bounded event subscription failure report",
      );
      expect(driver.health()).toEqual(expect.objectContaining({ ok: false }));
    } finally {
      hanging.stop(true);
    }
  });

  test("reconnects after an established SSE stream fails instead of degrading an idle interactive run", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    await driver.spawn({
      session_id: "attn-sse-reconnect",
      run_id: "run-sse-reconnect",
      cwd: "/tmp",
    });
    await eventually(() => target.eventSubscriberCount === 1, "initial SSE subscription");
    target.closeEvents();
    await eventually(
      () => target.requests.filter((request) => request.path === "/event").length >= 2 && target.eventSubscriberCount === 1,
      "reconnected SSE subscription",
    );
    expect(driver.health()).toEqual({ ok: true, message: "OpenCode 1.17.18 is ready" });
    expect(rpc.calls.some((call) => (call.params as { state?: string }).state === "unknown")).toBe(false);
  });

  test("degrades after repeated established SSE stream failures", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    let client: ControlledEventClient | undefined;
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => {
        client ??= new ControlledEventClient({ port, password });
        return client;
      },
      startupDeadline: 300,
      reconnectDelay: 5,
    });
    await driver.initialize();
    await driver.spawn({
      session_id: "attn-sse-repeated-failures",
      run_id: "run-sse-repeated-failures",
      cwd: "/tmp",
    });
    for (let expectedSubscriptions = 1; expectedSubscriptions <= 3; expectedSubscriptions += 1) {
      await eventually(
        () => client !== undefined && client.subscriptions.length === expectedSubscriptions,
        `SSE subscription ${expectedSubscriptions}`,
      );
      client!.failLatest();
    }
    await eventually(
      () => rpc.calls.some((call) => (call.params as { state?: string }).state === "unknown"),
      "terminal SSE failure report",
    );
    expect(driver.health()).toEqual(expect.objectContaining({ ok: false }));
  });

  test("aborts an established SSE subscription when a later setup request times out", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    target.sessions.set("native-hung", {
      id: "native-hung",
      model: { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "max" },
    });
    target.hangingSessionReads.add("native-hung");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = new OpenCodeDriver({
      rpc,
      registry,
      runCommand: async () => ({ exitCode: 0, stdout: "1.17.18\n", stderr: "" }),
      allocatePort: async () => target.port,
      http: (port, password) => new OpenCodeHTTP({ port, password }),
      startupDeadline: 100,
      reconnectDelay: 5,
      healthRequestTimeout: 5,
    });
    await driver.initialize();
    await driver.resume({
      session_id: "attn-hung-resume",
      run_id: "run-hung-resume",
      cwd: "/tmp",
      metadata: {
        schema: 1,
        opencode_session_id: "native-hung",
        opencode_version: "1.17.18",
        pinned: false,
      },
    });

    await eventually(
      () => rpc.calls.some((call) => call.method === "session.report_state"),
      "degraded setup failure report",
    );
    await eventually(() => target.eventSubscriberCount === 0, "aborted established event subscription");
    const reports = rpc.calls.length;
    target.emit("session.status", { sessionID: "native-hung", status: { type: "busy" } });
    await Bun.sleep(20);
    expect(rpc.calls).toHaveLength(reports);
  });
});

test("registry persists atomically with private files and cleans them after session close", async () => {
  const rpc = new RecordingRPC();
  const target = server("*");
  const root = join(await tempRoot(), "runtime");
  const registry = new RunRegistry(root);
  const driver = driverFor(rpc, registry, target);
  await driver.initialize();
  await driver.spawn({
    ...params("run-cleanup"),
    instructions: {
      kind: "workspace",
      content: "Private attn workspace guidance",
      workspace_id: "workspace-a",
      context_revision: 3,
    },
  });
  await eventually(() => target.prompts.length === 1, "run creation");
  const record = await registry.get("run-cleanup");
  expect(record).toBeDefined();
  expect((await stat(record!.password_ref)).mode & 0o077).toBe(0);
  expect((await stat(record!.prompt_ref!)).mode & 0o077).toBe(0);
  expect((await stat(record!.instruction_ref!)).mode & 0o077).toBe(0);
  expect(await readFile(record!.instruction_ref!, "utf8")).toBe("Private attn workspace guidance");
  expect(record!.instruction_kind).toBe("workspace");
  expect((await registry.readLaunchConfig(record!)).instruction_ref).toBe(record!.instruction_ref);
  expect(JSON.parse(await readFile(join(root, "runs.json"), "utf8")).runs["run-cleanup"].password_ref).toBe(record!.password_ref);
  await driver.sessionClosed({ session_id: "attn-run-cleanup", run_id: "run-cleanup", reason: "exited" });
  expect(await registry.get("run-cleanup")).toBeUndefined();
  await expect(stat(record!.password_ref)).rejects.toThrow();
  await expect(stat(record!.instruction_ref!)).rejects.toThrow();
});

test("registry migrates schema 1 without changing existing runs", async () => {
  const root = join(await tempRoot(), "runtime");
  await writePrivate(join(root, "runs.json"), `${JSON.stringify({ schema: 1, runs: {} })}\n`);
  const registry = new RunRegistry(root);
  await registry.initialize();
  expect(JSON.parse(await readFile(join(root, "runs.json"), "utf8")).schema).toBe(3);
});

test("registry migrates schema 2 and persists classification caches", async () => {
  const root = join(await tempRoot(), "runtime");
  await writePrivate(join(root, "runs.json"), `${JSON.stringify({ schema: 2, runs: {} })}\n`);
  const registry = new RunRegistry(root);
  await registry.initialize();
  expect(JSON.parse(await readFile(join(root, "runs.json"), "utf8")).schema).toBe(3);
});

test("OpenCode config merge preserves keys and deduplicates the guidance plugin", () => {
  const pluginRef = "file:///tmp/attn-guidance.ts";
  expect(opencodeConfigForLaunch(JSON.stringify({
    theme: "dark",
    instructions: ["repo.md"],
    plugin: ["user-plugin", pluginRef],
  }), "/tmp/attn.md", pluginRef)).toEqual({
    theme: "dark",
    instructions: ["repo.md"],
    plugin: ["user-plugin", pluginRef],
  });
  expect(opencodeConfigForLaunch(undefined, "/tmp/attn.md", pluginRef)).toEqual({ plugin: [pluginRef] });
});

test("OpenCode config merge rejects malformed inherited config", () => {
  expect(() => opencodeConfigForLaunch("{", "/tmp/attn.md")).toThrow("invalid inherited OPENCODE_CONFIG_CONTENT");
  expect(() => opencodeConfigForLaunch(JSON.stringify({ instructions: [1] }), "/tmp/attn.md")).toThrow("array of strings");
  expect(() => opencodeConfigForLaunch(JSON.stringify({ plugin: "bad" }), "/tmp/attn.md")).toThrow("expected an array");
});

test("guidance plugin reads the current private instruction file for every prompt", async () => {
  const root = await tempRoot();
  const instructionRef = join(root, "instructions.md");
  await writePrivate(instructionRef, "workspace guidance one");
  const previous = process.env.ATTN_OPENCODE_INSTRUCTION_REF;
  process.env.ATTN_OPENCODE_INSTRUCTION_REF = instructionRef;
  try {
    const hooks = await attnGuidancePlugin();
    const transform = hooks["experimental.chat.system.transform"];
    const first = { system: ["base"] };
    await transform({}, first);
    expect(first.system).toEqual(["base", "attn launch instructions:\nworkspace guidance one"]);

    await writePrivate(instructionRef, "chief guidance two");
    const second = { system: ["base"] };
    await transform({}, second);
    expect(second.system).toEqual(["base", "attn launch instructions:\nchief guidance two"]);
  } finally {
    if (previous === undefined) delete process.env.ATTN_OPENCODE_INSTRUCTION_REF;
    else process.env.ATTN_OPENCODE_INSTRUCTION_REF = previous;
  }
});

test("launcher rejects malformed inherited config before starting OpenCode", async () => {
  const root = await tempRoot();
  const marker = join(root, "started");
  const executable = join(root, "fake-opencode");
  await writeFile(executable, `#!/bin/sh\ntouch ${marker}\n`, { mode: 0o755 });
  const password = join(root, "password");
  await writePrivate(password, "secret");
  const config = join(root, "launch.json");
  await writePrivate(config, JSON.stringify({
    schema: 1,
    run_id: "launch-invalid-config",
    executable,
    cwd: root,
    password_ref: password,
    instruction_ref: join(root, "instructions.md"),
    port: 12345,
    yolo: false,
  }));
  const previous = process.env.OPENCODE_CONFIG_CONTENT;
  process.env.OPENCODE_CONFIG_CONTENT = "{";
  try {
    await expect(launch(config)).rejects.toThrow("invalid inherited OPENCODE_CONFIG_CONTENT");
  } finally {
    if (previous === undefined) delete process.env.OPENCODE_CONFIG_CONTENT;
    else process.env.OPENCODE_CONFIG_CONTENT = previous;
  }
  await expect(stat(marker)).rejects.toThrow();
});

test("session close aborts the active SSE subscription", async () => {
  const rpc = new RecordingRPC();
  const target = server("*");
  const registry = new RunRegistry(join(await tempRoot(), "runtime"));
  const driver = driverFor(rpc, registry, target);
  await driver.initialize();
  await driver.spawn(params("run-sse-close"));
  await eventually(() => target.eventSubscriberCount === 1, "active SSE subscription");

  await driver.sessionClosed({ session_id: "attn-run-sse-close", run_id: "run-sse-close", reason: "exited" });
  await eventually(() => target.eventSubscriberCount === 0, "aborted SSE subscription");
  const reports = rpc.calls.length;
  target.emit("session.status", { sessionID: "native-1", status: { type: "busy" } });
  await Bun.sleep(20);
  expect(rpc.calls).toHaveLength(reports);
});

test("launcher retries an address-in-use failure with a fresh port", async () => {
  const root = await tempRoot();
  const marker = join(root, "attempted");
  const executable = join(root, "fake-opencode");
  await writeFile(executable, `#!/bin/sh\nif [ ! -f ${marker} ]; then touch ${marker}; echo EADDRINUSE >&2; exit 1; fi\nexit 0\n`, { mode: 0o755 });
  const password = join(root, "password");
  await writePrivate(password, "secret");
  const config = join(root, "launch.json");
  await writePrivate(config, JSON.stringify({
    schema: 1,
    run_id: "launch-retry",
    executable,
    cwd: root,
    password_ref: password,
    port: 12345,
    yolo: false,
  }));
  expect(await launch(config)).toBe(0);
  expect(JSON.parse(await readFile(config, "utf8")).port).not.toBe(12345);
});

async function tempRoot(): Promise<string> {
  const root = join("/tmp", `attn-opencode-test-${randomUUID()}`);
  await mkdir(root, { recursive: true, mode: 0o700 });
  return root;
}
