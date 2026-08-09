import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PiHostDriver, defaultHostModel, hostAgentName } from "../src/host-driver";
import type { DriverSpawnParams } from "../src/types";

/**
 * The `pi-host` launcher. What it decides is small — the model, and whether the
 * launch carries a first message — but both are the whole of what a delegated
 * conversation session is: an agent handed a brief instead of an empty pane.
 */

class FakeRPC {
  readonly requests: Array<{ method: string; params: any }> = [];

  async request(method: string, params: any): Promise<any> {
    this.requests.push({ method, params });
    return { ok: true };
  }

  handle(_method: string, _handler: unknown): void {}
}

const tmpRoot = mkdtempSync(join(tmpdir(), "attn-pi-host-"));
const hostBinary = join(tmpRoot, "attn-pi-host");
writeFileSync(hostBinary, "// fake compiled host\n");

function newDriver(rpc: FakeRPC = new FakeRPC()): PiHostDriver {
  return new PiHostDriver({ rpc, hostCommand: [hostBinary] });
}

function params(overrides?: Partial<DriverSpawnParams>): DriverSpawnParams {
  return { session_id: "session-1", run_id: "run-1", cwd: "/tmp/work", ...overrides };
}

describe("PiHostDriver", () => {
  test("registers pi-host with the capabilities a delegable conversation agent needs", async () => {
    const rpc = new FakeRPC();
    await newDriver(rpc).initialize();

    const register = rpc.requests.find((call) => call.method === "driver.register");
    expect(register?.params).toEqual({
      agent: hostAgentName,
      capabilities: {
        conversation: true,
        initial_prompt: true,
        model_pin: true,
        state_reporting: true,
      },
    });
  });

  test("a launch with a brief carries it in the environment, not in argv", async () => {
    const brief = "Fix the flaky test in internal/store\nand report on the ticket.";
    const result = await newDriver().spawn(params({ initial_prompt: brief }));

    expect(result.argv).toEqual([hostBinary]);
    expect(result.env).toEqual({
      ATTN_PI_HOST_MODEL: defaultHostModel,
      ATTN_PI_HOST_INITIAL_PROMPT: brief,
    });
  });

  test("a launch with no brief sets no prompt variable at all", async () => {
    // Absent rather than empty: the host treats "set" as "there is something to
    // say", so an empty string would make every ordinary session look like a
    // delegation whose brief went missing.
    for (const prompt of [undefined, "", "   "]) {
      const result = await newDriver().spawn(params({ initial_prompt: prompt }));
      expect(result.env).toEqual({ ATTN_PI_HOST_MODEL: defaultHostModel });
    }
  });

  test("a pinned model wins over the default and travels beside the prompt", async () => {
    const result = await newDriver().spawn(params({ model: " anthropic/claude-x ", initial_prompt: "go" }));
    expect(result.env).toEqual({
      ATTN_PI_HOST_MODEL: "anthropic/claude-x",
      ATTN_PI_HOST_INITIAL_PROMPT: "go",
    });
  });

  test("spawn refuses when the host binary is missing, naming it", async () => {
    const driver = new PiHostDriver({ rpc: new FakeRPC(), hostCommand: [join(tmpRoot, "not-here")] });
    expect(driver.health().ok).toBe(false);
    await expect(driver.spawn(params())).rejects.toThrow(/not-here/);
  });
});
