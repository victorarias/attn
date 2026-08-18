import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PiDriver, type CommandResult, type RunCommand } from "../src/driver";
import { RelayServer } from "../src/relay";
import type { DriverSpawnParams } from "../src/types";

class FakeRPC {
  readonly requests: Array<{ method: string; params: any }> = [];
  classifyStopResult = "waiting_input";

  async request(method: string, params: any): Promise<any> {
    this.requests.push({ method, params });
    if (method === "driver.register") return { ok: true, active_runs: [] };
    if (method === "attn.classify_stop") return { verdict: this.classifyStopResult };
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

function params(overrides?: Partial<DriverSpawnParams>): DriverSpawnParams {
  return {
    session_id: "session-1",
    run_id: "run-1",
    cwd: "/tmp/work",
    ...overrides,
  };
}

const uuidPattern = /^[0-9a-f-]{36}$/;

// Shared tmp dir for everything socket/file-path related in this file. Keep
// filenames short: macOS unix socket paths cap at 104 bytes.
const tmpRoot = mkdtempSync(join(tmpdir(), "attn-pi-"));
const suitePath = join(tmpRoot, "suite.js");
writeFileSync(suitePath, "// fake pi suite entrypoint\n");
let relayCounter = 0;

function noopRelay(): RelayServer {
  return new RelayServer({
    socketPath: join(tmpRoot, `r${relayCounter++}.sock`),
    delegate: {
      async suiteHello() {
        return { ok: true as const };
      },
      async suiteReportState() {},
      async suiteReportStop() {},
      async suiteReportDenial() {},
    },
  });
}

// The half of RelayConnection the driver touches when a suite says hello: it
// binds the connection and asks to hear when it closes.
function fakeConnection(): any {
  const handlers: Array<() => void> = [];
  return {
    onClose(handler: () => void) {
      handlers.push(handler);
    },
    close() {
      for (const handler of handlers) handler();
    },
  };
}

function newDriver(options: {
  rpc: any;
  runCommand?: RunCommand;
  executable?: string;
  suitePath?: string;
  unbackedGraceMs?: number;
}): PiDriver {
  return new PiDriver({
    rpc: options.rpc,
    runCommand: options.runCommand ?? fakeRunCommand(),
    executable: options.executable ?? "pi",
    relay: noopRelay(),
    suitePath: options.suitePath ?? suitePath,
    // Far past anything a test waits for, so only the tests that mean to
    // exercise the alarm ever see it fire.
    unbackedGraceMs: options.unbackedGraceMs ?? 60_000,
  });
}

describe("PiDriver", () => {
  test("initialize registers the driver with agent pi and the expected capabilities", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });
    await driver.initialize();

    const register = rpc.requests.find((call) => call.method === "driver.register");
    expect(register).toBeDefined();
    expect(register?.params).toEqual({
      agent: "pi",
      capabilities: {
        resume: true,
        initial_prompt: true,
        model_pin: true,
        effort_pin: true,
        state_reporting: true,
        message_delivery: true,
        auto_mode: true,
      },
    });
  });

  test("initialize does not register when pi --version fails, and health reports not ok", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({
      rpc,
      runCommand: fakeRunCommand({ exitCode: 1, stdout: "", stderr: "command not found" }),
      executable: "pi",
    });
    await driver.initialize();

    expect(rpc.requests.find((call) => call.method === "driver.register")).toBeUndefined();
    const health = driver.health();
    expect(health.ok).toBe(false);
  });

  test("spawn forwards attn's auto mode config in the environment, and omits it when there is none", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const bare = await driver.spawn(params());
    expect(bare.env?.ATTN_PI_AUTOMODE_CONFIG).toBeUndefined();

    const config = {
      enabled_default: true,
      environment: ["never touch prod"],
      allow: ["git push origin*"],
      hard_deny: [],
      classifier_models: ["opencode-go/glm-5.3"],
      escalation_models: ["opencode-go/qwen3.8-max"],
    };
    const withConfig = await driver.spawn(params({ session_id: "session-2", run_id: "run-2", auto_mode: config }));
    expect(JSON.parse(withConfig.env?.ATTN_PI_AUTOMODE_CONFIG ?? "null")).toEqual(config);
  });

  test("spawn returns a fresh session id, passes cwd through, and reports metadata", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.spawn(params());

    expect(result.argv[0]).toBe("pi");
    expect(result.argv[1]).toBe("--session-id");
    const sessionID = result.argv[2];
    expect(sessionID).toMatch(uuidPattern);
    expect(result.argv).toEqual(["pi", "--session-id", sessionID, "-e", suitePath]);
    expect(result.cwd).toBe("/tmp/work");
    // The token is the run id, so a driver that restarted can rebuild the map
    // from what attn hands back at driver.register.
    expect(result.env?.ATTN_PI_TOKEN).toBe("run-1");
    expect(result.env?.ATTN_PI_SUITE_SOCKET).toBeTruthy();

    const report = rpc.requests.find((call) => call.method === "session.report_metadata");
    expect(report).toBeDefined();
    expect(report?.params).toEqual({
      session_id: "session-1",
      run_id: "run-1",
      seq: 1,
      metadata: {
        schema: 1,
        pi_session_id: sessionID,
        pi_version: "0.80.10",
      },
    });
  });

  test("two spawns mint distinct pi_session_ids", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const first = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const second = await driver.spawn(params({ session_id: "session-2", run_id: "run-2" }));

    expect(first.argv[2]).not.toBe(second.argv[2]);
  });

  test("spawn wires the suite into argv and env, and tokens each run by its run id", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const first = await driver.spawn(params({ session_id: "session-1", run_id: "run-1", initial_prompt: "go" }));
    const second = await driver.spawn(params({ session_id: "session-2", run_id: "run-2", initial_prompt: "go" }));

    const suiteIndex = first.argv.indexOf("-e");
    expect(suiteIndex).toBeGreaterThan(-1);
    expect(first.argv[suiteIndex + 1]).toBe(suitePath);
    expect(first.argv.indexOf("go")).toBeGreaterThan(suiteIndex + 1);

    expect(first.env?.ATTN_PI_SUITE_SOCKET).toBeTruthy();
    expect(first.env?.ATTN_PI_TOKEN).toBe("run-1");
    expect(second.env?.ATTN_PI_TOKEN).toBe("run-2");
  });

  test("spawn throws when the suite entrypoint is missing", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi", suitePath: join(tmpRoot, "does-not-exist.js") });

    await expect(driver.spawn(params())).rejects.toThrow(/suite entrypoint not found/);
  });

  test("spawn with model, effort, and initial_prompt composes argv and metadata", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.spawn(
      params({ model: "gpt-5.5", effort: "high", initial_prompt: "do the thing" }),
    );

    const sessionID = result.argv[2];
    expect(result.argv).toEqual([
      "pi",
      "--session-id",
      sessionID,
      "--model",
      "gpt-5.5",
      "--thinking",
      "high",
      "-e",
      suitePath,
      "do the thing",
    ]);

    const report = rpc.requests.find((call) => call.method === "session.report_metadata");
    expect(report?.params.metadata).toEqual({
      schema: 1,
      pi_session_id: sessionID,
      pi_version: "0.80.10",
      model: "gpt-5.5",
      thinking: "high",
    });
  });

  test("spawn with an unsupported thinking level throws, and empty effort is treated as absent", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    await expect(driver.spawn(params({ effort: "sky-high" }))).rejects.toThrow(/unsupported pi thinking level/);

    const result = await driver.spawn(params({ effort: "" }));
    expect(result.argv).not.toContain("--thinking");
  });

  test("spawn refuses a pi version below the minimum supported", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({
      rpc,
      runCommand: fakeRunCommand({ stdout: "0.79.0\n" }),
      executable: "pi",
    });

    await expect(driver.spawn(params())).rejects.toThrow(/minimum supported/);
  });

  test("resume with existing metadata and no pins reuses the pi_session_id and pins", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.resume(
      params({
        metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.80.10", model: "m1", thinking: "low" },
      }),
    );

    expect(result.argv).toEqual(["pi", "--session-id", "abc-123", "--model", "m1", "--thinking", "low", "-e", suitePath]);

    const report = rpc.requests.find((call) => call.method === "session.report_metadata");
    expect(report?.params.metadata).toEqual({
      schema: 1,
      pi_session_id: "abc-123",
      pi_version: "0.80.10",
      model: "m1",
      thinking: "low",
    });
  });

  test("resume params pins override metadata pins", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.resume(
      params({
        model: "m2",
        effort: "max",
        metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.80.10", model: "m1", thinking: "low" },
      }),
    );

    expect(result.argv).toEqual(["pi", "--session-id", "abc-123", "--model", "m2", "--thinking", "max", "-e", suitePath]);
  });

  test("resume refuses a downgrade from the recorded pi version", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand({ stdout: "0.80.10\n" }), executable: "pi" });

    await expect(
      driver.resume(
        params({
          metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.81.0" },
        }),
      ),
    ).rejects.toThrow(/older/);
  });

  test("resume rejects malformed metadata", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    await expect(driver.resume(params({ metadata: "not-an-object" }))).rejects.toThrow();
    await expect(driver.resume(params({ metadata: { schema: 2, pi_session_id: "abc", pi_version: "0.80.10" } }))).rejects.toThrow();
    await expect(driver.resume(params({ metadata: { schema: 1, pi_version: "0.80.10" } }))).rejects.toThrow();
  });

  test("resume never includes an initial prompt in argv", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.resume(
      params({
        initial_prompt: "should be ignored",
        metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.80.10" },
      }),
    );

    expect(result.argv).toEqual(["pi", "--session-id", "abc-123", "-e", suitePath]);
  });

  test("sessionClosed resolves ok", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    await expect(driver.sessionClosed({ session_id: "session-1", run_id: "run-1", reason: "exit" })).resolves.toEqual({
      ok: true,
    });
  });

  test("sessionClosed invalidates the run's token: a later hello with it is rejected", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.sessionClosed({ session_id: "session-1", run_id: "run-1", reason: "exit" });

    await expect(
      driver.suiteHello(fakeConnection(), { token, pi_session_id: "native-1", pi_version: "0.80.10", reason: "session_start" }),
    ).rejects.toThrow(/unknown pi suite token/);
  });

  test("reserves the stop seq before awaiting classification so a newer working report outranks it", async () => {
    let resolveClassify: (result: { verdict: string }) => void = () => {};
    const classifyDeferred = new Promise<{ verdict: string }>((resolve) => {
      resolveClassify = resolve;
    });
    const requests: Array<{ method: string; params: any }> = [];
    const rpc = {
      async request(method: string, params: any): Promise<any> {
        requests.push({ method, params });
        if (method === "driver.register") return { ok: true, active_runs: [] };
        if (method === "attn.classify_stop") return classifyDeferred;
        return { ok: true };
      },
      handle(_method: string, _handler: unknown): void {
        // no-op: this driver never dispatches through its own RPC handle table
      },
    };
    const driver = newDriver({ rpc, runCommand: fakeRunCommand(), executable: "pi" });

    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    // Do not await yet: this is the classification in flight while a new
    // message starts a new turn below.
    const stopPromise = driver.suiteReportStop({ token, assistant_text: "done with the task" });

    await driver.suiteReportState({ token, state: "working" });

    resolveClassify({ verdict: "idle" });
    await stopPromise;

    const stopReport = requests.find((call) => call.method === "session.report_stop");
    const stateReport = requests.find((call) => call.method === "session.report_state");
    expect(stopReport).toBeDefined();
    expect(stateReport).toBeDefined();
    expect(stopReport?.params.seq).toBeLessThan(stateReport?.params.seq);
    expect(stopReport?.params.verdict).toBe("idle");
  });

  // A pi session outlives the driver process that launched it: its pi keeps
  // running in a daemon-owned PTY and keeps reporting over the relay. These
  // cover the recovery that gives those reports somewhere to land.
  describe("adopting the runs attn reports still live", () => {
    function registerWith(activeRuns: unknown[]) {
      const requests: Array<{ method: string; params: any }> = [];
      const rpc = {
        async request(method: string, params: any): Promise<any> {
          requests.push({ method, params });
          if (method === "driver.register") return { ok: true, active_runs: activeRuns };
          if (method === "attn.classify_stop") return { verdict: "idle" };
          return { ok: true };
        },
        handle(_method: string, _handler: unknown): void {},
      };
      return { rpc, requests };
    }

    const piMetadata = { schema: 1, pi_session_id: "native-1", pi_version: "0.80.10", model: "glm-5.3" };

    test("a report from an inherited run lands, addressed to the run and continuing its cursor", async () => {
      const { rpc, requests } = registerWith([
        { session_id: "session-1", run_id: "run-1", metadata: piMetadata, seq: 5 },
      ]);
      const driver = newDriver({ rpc });
      await driver.initialize();

      await driver.suiteReportState({ token: "run-1", state: "working" });

      const report = requests.find((call) => call.method === "session.report_state");
      expect(report?.params).toEqual({ session_id: "session-1", run_id: "run-1", seq: 6, state: "working" });
    });

    test("the inherited run keeps its metadata, so a hello only replaces pi's own identity", async () => {
      const { rpc, requests } = registerWith([
        { session_id: "session-1", run_id: "run-1", metadata: piMetadata, seq: 2 },
      ]);
      const driver = newDriver({ rpc });
      await driver.initialize();

      await driver.suiteHello(fakeConnection(), {
        token: "run-1",
        pi_session_id: "native-2",
        pi_version: "0.84.2",
        reason: "reconnect",
      });

      const report = requests.filter((call) => call.method === "session.report_metadata").at(-1);
      expect(report?.params).toEqual({
        session_id: "session-1",
        run_id: "run-1",
        seq: 3,
        metadata: { schema: 1, pi_session_id: "native-2", pi_version: "0.84.2", model: "glm-5.3" },
      });
    });

    test("a run without a report cursor is declined rather than reported into a void", async () => {
      const { rpc } = registerWith([{ session_id: "session-1", run_id: "run-1", metadata: piMetadata }]);
      const driver = newDriver({ rpc });
      await driver.initialize();

      await expect(driver.suiteReportState({ token: "run-1", state: "working" })).rejects.toThrow(
        /unknown pi suite token/,
      );
    });

    test("runs belonging to the other agent this plugin registers are left alone", async () => {
      const { rpc } = registerWith([{ session_id: "nisse-1", run_id: "run-9", seq: 4 }]);
      const driver = newDriver({ rpc });
      await driver.initialize();

      await expect(driver.suiteReportState({ token: "run-9", state: "working" })).rejects.toThrow(
        /unknown pi suite token/,
      );
    });

    test("a relaunch of an inherited session supersedes it rather than stacking on it", async () => {
      const { rpc, requests } = registerWith([
        { session_id: "session-1", run_id: "run-1", metadata: piMetadata, seq: 5 },
      ]);
      const driver = newDriver({ rpc });
      await driver.initialize();

      await driver.spawn(params({ session_id: "session-1", run_id: "run-2" }));

      await expect(driver.suiteReportState({ token: "run-1", state: "working" })).rejects.toThrow(
        /unknown pi suite token/,
      );
      await driver.suiteReportState({ token: "run-2", state: "working" });
      const report = requests.find((call) => call.method === "session.report_state");
      expect(report?.params.run_id).toBe("run-2");
    });
  });

  test("a reported denial reaches the daemon addressed to the run that raised it", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.suiteReportDenial({
      token,
      tool: "bash",
      action: "bash: curl https://example.com",
      reason: "the user never asked to reach that host",
      rule: "classifier-2a",
      at: "2026-08-17T10:00:00.000Z",
    });

    expect(rpc.requests.find((call) => call.method === "session.report_automode_denial")?.params).toEqual({
      session_id: "session-1",
      run_id: "run-1",
      tool: "bash",
      action: "bash: curl https://example.com",
      reason: "the user never asked to reach that host",
      rule: "classifier-2a",
      at: "2026-08-17T10:00:00.000Z",
    });
  });

  test("a denial from an unknown token is refused rather than attributed to somebody", async () => {
    const driver = newDriver({ rpc: new FakeRPC() });
    await expect(driver.suiteReportDenial({ token: "not-a-token", action: "bash: rm -rf /" })).rejects.toThrow(
      /unknown pi suite token/,
    );
  });

  test("a denial with no action named is refused: an empty row says nothing", async () => {
    const driver = newDriver({ rpc: new FakeRPC() });
    await expect(driver.suiteReportDenial({ token: "tok", action: "  " })).rejects.toThrow(/missing action/);
  });

  test("a session blocked on the user is reported as such, not as working", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.suiteReportState({ token, state: "pending_approval" });

    expect(rpc.requests.find((call) => call.method === "session.report_state")?.params).toEqual({
      session_id: "session-1",
      run_id: "run-1",
      seq: 2,
      state: "pending_approval",
    });
  });

  test("a state nothing in pi can be blocked on is refused rather than forwarded", async () => {
    const driver = newDriver({ rpc: new FakeRPC() });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await expect(driver.suiteReportState({ token, state: "idle" })).rejects.toThrow(/pending_approval/);
  });

  test("an interrupted turn settles as idle without paying for a classification", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.suiteReportStop({ token, assistant_text: "half a paragraph the user cut off", aborted: true });

    expect(rpc.requests.some((call) => call.method === "attn.classify_stop")).toBe(false);
    expect(rpc.requests.find((call) => call.method === "session.report_stop")?.params.verdict).toBe("idle");
  });

  test("a turn that ended on its own is still classified", async () => {
    const rpc = new FakeRPC();
    rpc.classifyStopResult = "waiting_input";
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.suiteReportStop({ token, assistant_text: "want me to ship it?", aborted: false });

    expect(rpc.requests.some((call) => call.method === "attn.classify_stop")).toBe(true);
    expect(rpc.requests.find((call) => call.method === "session.report_stop")?.params.verdict).toBe("waiting_input");
  });

  // A declaration is only as current as the thing declaring it. These cover the
  // driver withdrawing one it can no longer stand behind.
  describe("a run with no suite connected", () => {
    test("is reported unknown once the grace passes, rather than left as it was", async () => {
      const rpc = new FakeRPC();
      const driver = newDriver({ rpc, unbackedGraceMs: 20 });
      await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));

      await waitFor(() => rpc.requests.some((call) => call.method === "session.report_state"));
      expect(rpc.requests.find((call) => call.method === "session.report_state")?.params).toEqual({
        session_id: "session-1",
        run_id: "run-1",
        seq: 2,
        state: "unknown",
      });
    });

    test("a suite that says hello in time keeps its declaration standing", async () => {
      const rpc = new FakeRPC();
      const driver = newDriver({ rpc, unbackedGraceMs: 40 });
      const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
      const token = spawned.env?.ATTN_PI_TOKEN as string;

      await driver.suiteHello(fakeConnection(), {
        token,
        pi_session_id: "native-1",
        pi_version: "0.80.10",
        reason: "startup",
      });
      await new Promise((resolve) => setTimeout(resolve, 120));

      expect(rpc.requests.some((call) => call.method === "session.report_state")).toBe(false);
    });

    test("a suite that disconnects re-arms it, so a pi that outlives its suite does not freeze attn", async () => {
      const rpc = new FakeRPC();
      const driver = newDriver({ rpc, unbackedGraceMs: 20 });
      const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
      const token = spawned.env?.ATTN_PI_TOKEN as string;
      const connection = fakeConnection();
      await driver.suiteHello(connection, {
        token,
        pi_session_id: "native-1",
        pi_version: "0.80.10",
        reason: "startup",
      });

      connection.close();

      await waitFor(() => rpc.requests.some((call) => call.method === "session.report_state"));
      expect(rpc.requests.find((call) => call.method === "session.report_state")?.params.state).toBe("unknown");
    });

    test("a closed session takes its alarm with it: nothing is reported for a run that ended", async () => {
      const rpc = new FakeRPC();
      const driver = newDriver({ rpc, unbackedGraceMs: 20 });
      await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));

      await driver.sessionClosed({ session_id: "session-1", run_id: "run-1", reason: "exit" });
      await new Promise((resolve) => setTimeout(resolve, 80));

      expect(rpc.requests.some((call) => call.method === "session.report_state")).toBe(false);
    });
  });
});

async function waitFor(predicate: () => boolean, timeoutMs = 2_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error("timed out waiting for the driver");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

describe("PiDriver: coming back after attn had nothing", () => {
  test("hands attn what pi says it is, for attn to use only if it still says unknown", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.suiteHello(fakeConnection(), {
      token,
      pi_session_id: "native-1",
      pi_version: "0.80.10",
      reason: "reconnect",
      pi_state: "idle",
    });

    expect(rpc.requests.find((call) => call.method === "session.report_state")?.params).toEqual({
      session_id: "session-1",
      run_id: "run-1",
      seq: 3,
      state: "idle",
      only_if_unknown: true,
    });
  });

  test("says nothing when the hello carries no state, so an older suite changes nothing", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await driver.suiteHello(fakeConnection(), {
      token,
      pi_session_id: "native-1",
      pi_version: "0.80.10",
      reason: "reconnect",
    });

    expect(rpc.requests.some((call) => call.method === "session.report_state")).toBe(false);
  });

  test("refuses a hello whose state is not one pi can be in", async () => {
    const rpc = new FakeRPC();
    const driver = newDriver({ rpc });
    const spawned = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const token = spawned.env?.ATTN_PI_TOKEN as string;

    await expect(
      driver.suiteHello(fakeConnection(), {
        token,
        pi_session_id: "native-1",
        pi_version: "0.80.10",
        reason: "reconnect",
        pi_state: "recoverable",
      }),
    ).rejects.toThrow(/pi_state/);
  });
});
