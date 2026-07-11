import { afterEach, describe, expect, test } from "bun:test";
import { randomUUID } from "node:crypto";
import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { OpenCodeDriver } from "../src/driver";
import { launch } from "../src/launcher";
import { type EventSubscription, type ServerEvent, OpenCodeHTTP } from "../src/opencode-http";
import { RunRegistry, writePrivate } from "../src/run-registry";
import type { DriverSpawnParams } from "../src/types";
import { FakeOpenCode, eventually } from "./fake-opencode";

const servers: FakeOpenCode[] = [];
afterEach(() => {
  for (const server of servers.splice(0)) server.close();
});

class RecordingRPC {
  readonly calls: Array<{ method: string; params: unknown }> = [];
  async request(method: string, params?: unknown): Promise<unknown> {
    this.calls.push({ method, params });
    return { ok: true };
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
  test("accepts both explicitly contract-tested OpenCode versions", async () => {
    for (const version of ["1.17.16", "1.17.18"]) {
      const rpc = new RecordingRPC();
      const registry = new RunRegistry(join(await tempRoot(), "runtime"));
      const driver = new OpenCodeDriver({
        rpc,
        registry,
        runCommand: async () => ({ exitCode: 0, stdout: `${version}\n`, stderr: "" }),
      });
      await driver.initialize();
      expect(driver.health()).toEqual({ ok: true, message: `OpenCode ${version} is ready` });
      expect(rpc.calls[0]?.method).toBe("driver.register");
    }
  });

  test("uses per-run Basic authentication on every HTTP endpoint", async () => {
    const target = server("correct-password");
    await expect(new OpenCodeHTTP({ port: target.port, password: "wrong-password" }).health()).rejects.toThrow("HTTP 401");
    const client = new OpenCodeHTTP({ port: target.port, password: "correct-password" });
    await expect(client.health()).resolves.toEqual({ version: "1.17.18" });
    expect(target.requests).toHaveLength(2);
  });

  test("creates a pinned native session after authenticated SSE subscription and does not infer idle from a missing status", async () => {
    const rpc = new RecordingRPC();
    const target = server("*");
    const registry = new RunRegistry(join(await tempRoot(), "runtime"));
    const driver = driverFor(rpc, registry, target);
    await driver.initialize();
    expect(rpc.calls[0]?.method).toBe("driver.register");

    await driver.spawn(params());
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
  await driver.spawn(params("run-cleanup"));
  await eventually(() => target.prompts.length === 1, "run creation");
  const record = await registry.get("run-cleanup");
  expect(record).toBeDefined();
  expect((await stat(record!.password_ref)).mode & 0o077).toBe(0);
  expect((await stat(record!.prompt_ref!)).mode & 0o077).toBe(0);
  expect(JSON.parse(await readFile(join(root, "runs.json"), "utf8")).runs["run-cleanup"].password_ref).toBe(record!.password_ref);
  await driver.sessionClosed({ session_id: "attn-run-cleanup", run_id: "run-cleanup", reason: "exited" });
  expect(await registry.get("run-cleanup")).toBeUndefined();
  await expect(stat(record!.password_ref)).rejects.toThrow();
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
