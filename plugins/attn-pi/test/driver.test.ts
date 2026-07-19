import { describe, expect, test } from "bun:test";
import { PiDriver, type CommandResult, type RunCommand } from "../src/driver";
import type { DriverSpawnParams } from "../src/types";

class FakeRPC {
  readonly requests: Array<{ method: string; params: any }> = [];

  async request(method: string, params: any): Promise<any> {
    this.requests.push({ method, params });
    if (method === "driver.register") return { ok: true, active_runs: [] };
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

describe("PiDriver", () => {
  test("initialize registers the driver with agent pi and the expected capabilities", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });
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
      },
    });
  });

  test("initialize does not register when pi --version fails, and health reports not ok", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({
      rpc: rpc as any,
      runCommand: fakeRunCommand({ exitCode: 1, stdout: "", stderr: "command not found" }),
      executable: "pi",
    });
    await driver.initialize();

    expect(rpc.requests.find((call) => call.method === "driver.register")).toBeUndefined();
    const health = driver.health();
    expect(health.ok).toBe(false);
  });

  test("spawn returns a fresh session id, passes cwd through, and reports metadata", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.spawn(params());

    expect(result.argv[0]).toBe("pi");
    expect(result.argv[1]).toBe("--session-id");
    const sessionID = result.argv[2];
    expect(sessionID).toMatch(uuidPattern);
    expect(result.argv).toEqual(["pi", "--session-id", sessionID]);
    expect(result.cwd).toBe("/tmp/work");

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
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    const first = await driver.spawn(params({ session_id: "session-1", run_id: "run-1" }));
    const second = await driver.spawn(params({ session_id: "session-2", run_id: "run-2" }));

    expect(first.argv[2]).not.toBe(second.argv[2]);
  });

  test("spawn with model, effort, and initial_prompt composes argv and metadata", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.spawn(
      params({ model: "gpt-5.5", effort: "high", initial_prompt: "do the thing" }),
    );

    const sessionID = result.argv[2];
    expect(result.argv).toEqual(["pi", "--session-id", sessionID, "--model", "gpt-5.5", "--thinking", "high", "do the thing"]);

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
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    await expect(driver.spawn(params({ effort: "sky-high" }))).rejects.toThrow(/unsupported pi thinking level/);

    const result = await driver.spawn(params({ effort: "" }));
    expect(result.argv).not.toContain("--thinking");
  });

  test("spawn refuses a pi version below the minimum supported", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({
      rpc: rpc as any,
      runCommand: fakeRunCommand({ stdout: "0.79.0\n" }),
      executable: "pi",
    });

    await expect(driver.spawn(params())).rejects.toThrow(/minimum supported/);
  });

  test("resume with existing metadata and no pins reuses the pi_session_id and pins", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.resume(
      params({
        metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.80.10", model: "m1", thinking: "low" },
      }),
    );

    expect(result.argv).toEqual(["pi", "--session-id", "abc-123", "--model", "m1", "--thinking", "low"]);

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
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.resume(
      params({
        model: "m2",
        effort: "max",
        metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.80.10", model: "m1", thinking: "low" },
      }),
    );

    expect(result.argv).toEqual(["pi", "--session-id", "abc-123", "--model", "m2", "--thinking", "max"]);
  });

  test("resume refuses a downgrade from the recorded pi version", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand({ stdout: "0.80.10\n" }), executable: "pi" });

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
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    await expect(driver.resume(params({ metadata: "not-an-object" }))).rejects.toThrow();
    await expect(driver.resume(params({ metadata: { schema: 2, pi_session_id: "abc", pi_version: "0.80.10" } }))).rejects.toThrow();
    await expect(driver.resume(params({ metadata: { schema: 1, pi_version: "0.80.10" } }))).rejects.toThrow();
  });

  test("resume never includes an initial prompt in argv", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    const result = await driver.resume(
      params({
        initial_prompt: "should be ignored",
        metadata: { schema: 1, pi_session_id: "abc-123", pi_version: "0.80.10" },
      }),
    );

    expect(result.argv).toEqual(["pi", "--session-id", "abc-123"]);
  });

  test("sessionClosed resolves ok", async () => {
    const rpc = new FakeRPC();
    const driver = new PiDriver({ rpc: rpc as any, runCommand: fakeRunCommand(), executable: "pi" });

    await expect(driver.sessionClosed({ session_id: "session-1", run_id: "run-1", reason: "exit" })).resolves.toEqual({
      ok: true,
    });
  });
});
