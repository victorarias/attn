import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { createConnection, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PiDriver, type CommandResult, type RunCommand } from "../src/driver";
import { RelayServer, type RelayConnection } from "../src/relay";
import type { DriverSpawnParams } from "../src/types";

class FakeRPC {
  readonly requests: Array<{ method: string; params: any }> = [];
  classifyStopVerdict = "waiting_input";

  async request(method: string, params: any): Promise<any> {
    this.requests.push({ method, params });
    if (method === "driver.register") return { ok: true, active_runs: [] };
    if (method === "attn.classify_stop") return { verdict: this.classifyStopVerdict };
    return { ok: true };
  }

  handle(_method: string, _handler: unknown): void {
    // no-op: this driver never dispatches through its own RPC handle table
  }
}

function fakeRunCommand(overrides?: Partial<CommandResult>): RunCommand {
  const result: CommandResult = { exitCode: 0, stdout: "0.80.10\n", stderr: "", ...overrides };
  return async () => result;
}

function spawnParams(overrides?: Partial<DriverSpawnParams>): DriverSpawnParams {
  return { session_id: "session-1", run_id: "run-1", cwd: "/tmp/work", ...overrides };
}

// Keep filenames short: macOS unix socket paths cap sun_path at 104 bytes.
const tmpRoot = mkdtempSync(join(tmpdir(), "attn-pi-"));
const suitePath = join(tmpRoot, "suite.js");
writeFileSync(suitePath, "// fake pi suite entrypoint\n");
let socketCounter = 0;

function nextSocketPath(): string {
  return join(tmpRoot, `s${socketCounter++}.sock`);
}

// A minimal ndjson JSON-RPC 2.0 client standing in for the pi-side suite:
// connects to the driver's relay socket, can send requests (suite -> driver)
// and answer inbound requests (driver -> suite) via a settable responder.
// Returned by a test's responder to simulate a suite that never answers a
// driver -> suite request, so deliverMessage's timeout/close paths can be
// exercised without a real hung process.
const NEVER_RESPOND = Symbol("never-respond");

class FakeSuiteClient {
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, { resolve: (value: any) => void; reject: (error: Error) => void }>();
  readonly received: Array<{ method: string; params: any }> = [];
  responder: ((method: string, params: any) => unknown) | undefined;

  private constructor(private readonly socket: Socket) {
    this.socket.setEncoding("utf8");
    this.socket.on("data", (chunk) => this.consume(chunk));
  }

  static async connect(socketPath: string): Promise<FakeSuiteClient> {
    const socket = await new Promise<Socket>((resolve, reject) => {
      const candidate = createConnection({ path: socketPath });
      candidate.once("error", reject);
      candidate.once("connect", () => {
        candidate.off("error", reject);
        resolve(candidate);
      });
    });
    return new FakeSuiteClient(socket);
  }

  request<TResult = unknown>(method: string, params: unknown): Promise<TResult> {
    const id = this.nextID++;
    const result = new Promise<TResult>((resolve, reject) => {
      this.pending.set(String(id), { resolve, reject });
    });
    this.socket.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    return result;
  }

  close(): void {
    this.socket.destroy();
  }

  private consume(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n");
      if (end < 0) return;
      const line = this.buffer.slice(0, end).trim();
      this.buffer = this.buffer.slice(end + 1);
      if (line === "") continue;
      this.route(JSON.parse(line));
    }
  }

  private route(message: any): void {
    if ("method" in message) {
      this.received.push({ method: message.method, params: message.params });
      const result = this.responder ? this.responder(message.method, message.params) : { delivered: true };
      if (result === NEVER_RESPOND) return; // simulate an unresponsive suite
      this.socket.write(`${JSON.stringify({ jsonrpc: "2.0", id: message.id, result })}\n`);
      return;
    }
    const pending = this.pending.get(String(message.id));
    if (!pending) return;
    this.pending.delete(String(message.id));
    if (message.error) pending.reject(new Error(message.error.message));
    else pending.resolve(message.result);
  }
}

// Wires a PiDriver to a listening RelayServer the same way index.ts does:
// the delegate closes over the `driver` binding since RelayServer and
// PiDriver each need a reference to the other before either is constructed.
async function buildHarness(rpc: FakeRPC): Promise<{ driver: PiDriver; relay: RelayServer; socketPath: string }> {
  const socketPath = nextSocketPath();
  let driver: PiDriver;
  const relay = new RelayServer({
    socketPath,
    delegate: {
      suiteHello: (connection: RelayConnection, params: unknown) => driver.suiteHello(connection, params),
      suiteReportState: (params: unknown) => driver.suiteReportState(params),
      suiteReportStop: (params: unknown) => driver.suiteReportStop(params),
    },
  });
  driver = new PiDriver({ rpc: rpc as any, relay, suitePath, runCommand: fakeRunCommand() });
  await relay.listen();
  return { driver, relay, socketPath };
}

// A bare RelayServer (no PiDriver) for testing wire-level behavior that
// doesn't depend on driver semantics: unknown methods and deliverMessage's
// timeout/close-rejection paths.
async function buildBareRelay(): Promise<{ relay: RelayServer; socketPath: string; connections: () => RelayConnection[] }> {
  const socketPath = nextSocketPath();
  const connections: RelayConnection[] = [];
  const relay = new RelayServer({
    socketPath,
    delegate: {
      async suiteHello(connection) {
        connections.push(connection);
        return { ok: true as const };
      },
      async suiteReportState() {},
      async suiteReportStop() {},
    },
  });
  await relay.listen();
  return { relay, socketPath, connections: () => connections };
}

describe("RelayServer wire behavior", () => {
  test("an unknown method gets a JSON-RPC -32601 error", async () => {
    const { socketPath } = await buildBareRelay();
    const suite = await FakeSuiteClient.connect(socketPath);

    await expect(suite.request("suite.no_such_method", {})).rejects.toThrow(/unknown method/);
    suite.close();
  });

  test("deliverMessage rejects on timeout when the suite never answers", async () => {
    const { relay, socketPath, connections } = await buildBareRelay();
    const suite = await FakeSuiteClient.connect(socketPath);
    suite.responder = () => NEVER_RESPOND;
    await suite.request("suite.hello", { token: "t", pi_session_id: "x", pi_version: "0.80.10", reason: "session_start" });
    const connection = connections()[0]!;

    await expect(relay.deliverMessage(connection, { text: "hi" }, 30)).rejects.toThrow(/did not respond/);

    suite.close();
  });

  test("deliverMessage rejects when the suite connection closes while a request is pending", async () => {
    const { relay, socketPath, connections } = await buildBareRelay();
    const suite = await FakeSuiteClient.connect(socketPath);
    suite.responder = () => NEVER_RESPOND;
    await suite.request("suite.hello", { token: "t", pi_session_id: "x", pi_version: "0.80.10", reason: "session_start" });
    const connection = connections()[0]!;

    const pending = relay.deliverMessage(connection, { text: "hi" }, 5_000);
    suite.close();

    await expect(pending).rejects.toThrow(/suite connection closed/);
  });
});

describe("suite <-> driver relay integration", () => {
  test("suite.hello with a valid token binds the connection, refreshes metadata, and reports seq 2", async () => {
    const rpc = new FakeRPC();
    const { driver, socketPath } = await buildHarness(rpc);

    const spawned = await driver.spawn(spawnParams({ model: "gpt-5.5", effort: "low" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    const suite = await FakeSuiteClient.connect(socketPath);
    const helloResult = await suite.request("suite.hello", {
      token,
      pi_session_id: "native-session-2",
      pi_version: "0.80.10",
      reason: "resume",
    });

    expect(helloResult).toEqual({ ok: true });

    const reports = rpc.requests.filter((call) => call.method === "session.report_metadata");
    expect(reports).toHaveLength(2);
    expect(reports[1]?.params).toEqual({
      session_id: "session-1",
      run_id: "run-1",
      seq: 2,
      metadata: {
        schema: 1,
        pi_session_id: "native-session-2",
        pi_version: "0.80.10",
        model: "gpt-5.5",
        thinking: "low",
      },
    });

    suite.close();
  });

  test("suite.hello with an unknown token is rejected and reports nothing", async () => {
    const rpc = new FakeRPC();
    const { socketPath } = await buildHarness(rpc);
    const suite = await FakeSuiteClient.connect(socketPath);

    const before = rpc.requests.length;
    await expect(
      suite.request("suite.hello", { token: "does-not-exist", pi_session_id: "x", pi_version: "0.80.10", reason: "session_start" }),
    ).rejects.toThrow(/unknown pi suite token/);
    expect(rpc.requests.length).toBe(before);

    suite.close();
  });

  test("suite.report_state working reports session.report_state with an advancing seq", async () => {
    const rpc = new FakeRPC();
    const { driver, socketPath } = await buildHarness(rpc);
    const spawned = await driver.spawn(spawnParams());
    const token = spawned.env?.ATTN_PI_TOKEN as string;
    const suite = await FakeSuiteClient.connect(socketPath);
    await suite.request("suite.hello", { token, pi_session_id: "native-1", pi_version: "0.80.10", reason: "session_start" });

    await suite.request("suite.report_state", { token, state: "working" });
    await suite.request("suite.report_state", { token, state: "working" });

    const reports = rpc.requests.filter((call) => call.method === "session.report_state");
    expect(reports).toHaveLength(2);
    expect(reports[0]?.params).toEqual({ session_id: "session-1", run_id: "run-1", seq: 3, state: "working" });
    expect(reports[1]?.params).toEqual({ session_id: "session-1", run_id: "run-1", seq: 4, state: "working" });

    suite.close();
  });

  test("suite.report_stop with text classifies and reports the classifier's verdict", async () => {
    const rpc = new FakeRPC();
    rpc.classifyStopVerdict = "waiting_input";
    const { driver, socketPath } = await buildHarness(rpc);
    const spawned = await driver.spawn(spawnParams());
    const token = spawned.env?.ATTN_PI_TOKEN as string;
    const suite = await FakeSuiteClient.connect(socketPath);
    await suite.request("suite.hello", { token, pi_session_id: "native-1", pi_version: "0.80.10", reason: "session_start" });

    await suite.request("suite.report_stop", { token, assistant_text: "done, want a review?" });

    const classify = rpc.requests.find((call) => call.method === "attn.classify_stop");
    expect(classify?.params).toEqual({
      session_id: "session-1",
      run_id: "run-1",
      assistant_text: "done, want a review?",
    });
    const stop = rpc.requests.find((call) => call.method === "session.report_stop");
    expect(stop?.params).toEqual({ session_id: "session-1", run_id: "run-1", seq: 3, verdict: "waiting_input" });

    suite.close();
  });

  test("suite.report_stop with whitespace-only text skips the classifier and reports idle", async () => {
    const rpc = new FakeRPC();
    const { driver, socketPath } = await buildHarness(rpc);
    const spawned = await driver.spawn(spawnParams());
    const token = spawned.env?.ATTN_PI_TOKEN as string;
    const suite = await FakeSuiteClient.connect(socketPath);
    await suite.request("suite.hello", { token, pi_session_id: "native-1", pi_version: "0.80.10", reason: "session_start" });

    await suite.request("suite.report_stop", { token, assistant_text: "   \n  " });

    expect(rpc.requests.find((call) => call.method === "attn.classify_stop")).toBeUndefined();
    const stop = rpc.requests.find((call) => call.method === "session.report_stop");
    expect(stop?.params).toEqual({ session_id: "session-1", run_id: "run-1", seq: 3, verdict: "idle" });

    suite.close();
  });

  test("driver.deliver_message forwards to the connected suite and returns its answer", async () => {
    const rpc = new FakeRPC();
    const { driver, socketPath } = await buildHarness(rpc);
    const spawned = await driver.spawn(spawnParams());
    const token = spawned.env?.ATTN_PI_TOKEN as string;
    const suite = await FakeSuiteClient.connect(socketPath);
    suite.responder = (method, params) => {
      expect(method).toBe("driver.deliver_message");
      expect(params).toEqual({ text: "hey, are you there?" });
      return { delivered: true };
    };
    await suite.request("suite.hello", { token, pi_session_id: "native-1", pi_version: "0.80.10", reason: "session_start" });

    const result = await driver.deliverMessage({ session_id: "session-1", run_id: "run-1", text: "hey, are you there?" });

    expect(result).toEqual({ ok: true });
    expect(suite.received).toContainEqual({ method: "driver.deliver_message", params: { text: "hey, are you there?" } });

    suite.close();
  });

  test("driver.deliver_message throws for an unknown session_id", async () => {
    const rpc = new FakeRPC();
    const { driver } = await buildHarness(rpc);

    await expect(
      driver.deliverMessage({ session_id: "no-such-session", run_id: "run-1", text: "hi" }),
    ).rejects.toThrow(/no active pi run/);
  });

  test("driver.deliver_message throws when no suite connection is bound yet", async () => {
    const rpc = new FakeRPC();
    const { driver } = await buildHarness(rpc);
    await driver.spawn(spawnParams());

    await expect(
      driver.deliverMessage({ session_id: "session-1", run_id: "run-1", text: "hi" }),
    ).rejects.toThrow(/no live pi suite connection/);
  });
});
