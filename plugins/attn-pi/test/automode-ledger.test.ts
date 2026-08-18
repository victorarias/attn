// The durable local record of every refused call. The relay is allowed to lose
// a denial — a bare pi has none, and a relay whose socket died drops what it is
// handed — so these tests are about what survives that.
import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { RelayServer } from "../src/relay";
import { AttnPiSuite } from "../suite/core";
import { defaultAutoModeConfig } from "../automode/config";
import {
  DenialLedger,
  denialLedgerEnvVar,
  denialLedgerFileName,
  denialLedgerFor,
  denialLedgerPath,
  denialLedgerSessionEnvVar,
  type DenialLedgerRecord,
} from "../automode/ledger";
import { AutoMode } from "../automode/mode";
import type { AutoModeDenial } from "../automode/index";
import { locatePath } from "../automode/paths";
import type {
  CompletionResult,
  ModelLike,
  ModelRegistryLike,
  ProviderLike,
  RequestAuthLike,
} from "../automode/model-classifier";
import { FakePi, FakeUI, toolCall, uiContext } from "./automode-fake-pi";

class DenyingRegistry implements ModelRegistryLike {
  find(provider: string, id: string): ModelLike | undefined {
    return { provider, id };
  }

  getProvider(): ProviderLike {
    return {
      streamSimple: () => ({
        result: async (): Promise<CompletionResult> => ({
          content: [{ type: "text", text: JSON.stringify({ verdict: "deny", reason: "not asked for" }) }],
          stopReason: "stop",
        }),
      }),
    };
  }

  async getApiKeyAndHeaders(): Promise<RequestAuthLike> {
    return { ok: true, apiKey: "key" };
  }

  async getProviderAuth() {
    return undefined;
  }
}

const push = () => toolCall("bash", { command: "git push --force origin main" });

function tempPath(name = denialLedgerFileName): string {
  return join(mkdtempSync(join(tmpdir(), "attn-ledger-")), name);
}

/** The denials in a generation. A rotation marker is not one. */
function readRecords(path: string): DenialLedgerRecord[] {
  return readLines(path)
    .map((line) => JSON.parse(line) as DenialLedgerRecord & { type?: string })
    .filter((record) => record.type !== "rotated");
}

function readMarkers(path: string): { dropped: number }[] {
  return readLines(path)
    .map((line) => JSON.parse(line) as { type?: string; dropped: number })
    .filter((record) => record.type === "rotated");
}

function readLines(path: string): string[] {
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line.trim() !== "");
}

function denial(overrides: Partial<AutoModeDenial> = {}): AutoModeDenial {
  return {
    toolCallId: "call-1",
    tool: "bash",
    action: "bash: git push --force origin main",
    reason: "not asked for",
    rule: "classifier-2a",
    at: "2026-08-18T10:00:00.000Z",
    ...overrides,
  };
}

/** Runs one denied call through the whole extension against a real ledger. */
async function denyOneCall(options: { path: string; onDenial?: (denial: AutoModeDenial) => void }): Promise<FakeUI> {
  const mode = new AutoMode({
    config: defaultAutoModeConfig,
    ledger: new DenialLedger(options.path, "sess-1"),
    onDenial: options.onDenial,
  });
  const pi = new FakePi();
  mode.register(pi);
  pi.start(uiContext(new FakeUI(), { modelRegistry: new DenyingRegistry() }));
  const ui = new FakeUI();
  expect((await pi.toolCall?.(push(), uiContext(ui)))?.block).toBe(true);
  return ui;
}

describe("where the ledger lives", () => {
  test("attn names the file; bare pi falls back to pi's config directory", () => {
    expect(denialLedgerPath({ [denialLedgerEnvVar]: "/data/attn-dev/denials.jsonl" })).toBe(
      "/data/attn-dev/denials.jsonl",
    );
    expect(denialLedgerPath({ PI_CODING_AGENT_DIR: "/tmp/pi" })).toBe(join("/tmp/pi", denialLedgerFileName));
    expect(denialLedgerPath({})).toContain(join(".pi", "agent", denialLedgerFileName));
  });

  test("the session id rides along, and a bare pi has none to name", () => {
    const path = tempPath();
    denialLedgerFor({ [denialLedgerEnvVar]: path, [denialLedgerSessionEnvVar]: " sess-7 " }).record(denial());
    expect(readRecords(path)[0]?.session_id).toBe("sess-7");

    const bare = tempPath();
    denialLedgerFor({ [denialLedgerEnvVar]: bare }).record(denial());
    expect(readRecords(bare)[0]?.session_id).toBe("");
  });

  test("auto mode's own files are protected wherever they sit, ledger included", () => {
    // The record must not become a path an agent writes policy through.
    expect(locatePath("/work/repo", "/work/repo/attn-automode-denials.jsonl").location).toBe("protected");
    expect(locatePath("/work/repo", "/work/repo/attn-automode.json").location).toBe("protected");
    expect(locatePath("/work/repo", "/work/repo/attn-automode-denials.jsonl.1").location).toBe("protected");
    expect(locatePath("/work/repo", "/work/repo/notes.md").location).toBe("in-envelope");
  });
});

describe("what a denial leaves behind", () => {
  test("a denial with no relay at all is still recorded — this is bare pi", async () => {
    const path = tempPath();
    await denyOneCall({ path });

    const records = readRecords(path);
    expect(records).toHaveLength(1);
    expect(records[0]).toEqual({
      session_id: "sess-1",
      tool_call_id: "call-1",
      tool: "bash",
      action: "bash: git push --force origin main",
      reason: "not asked for",
      rule: "classifier-2a",
      at: expect.any(String),
    });
  });

  test("a report that reaches a relay and dies there still leaves the record", async () => {
    // The 2026-08-17 episode's shape: the denial left the session and nothing
    // on the other side kept it. A relay that refuses the report stands in for
    // the socket a plugin reload removed — bun's runner cannot host a failed
    // unix connect, and the two are the same thing from here: the report is
    // gone and nothing tells the session so.
    const socketPath = join(mkdtempSync(join(tmpdir(), "attn-pi-lost-")), "s.sock");
    const relay = new RelayServer({
      socketPath,
      delegate: {
        async suiteHello() {
          return { ok: true as const };
        },
        async suiteReportState() {},
        async suiteReportStop() {},
        async suiteReportDenial() {
          throw new Error("the daemon never saw this");
        },
      },
    });
    await relay.listen();
    const suite = new AttnPiSuite({ socketPath, token: "tok", piVersion: "0.83.0" });
    const path = tempPath();

    await denyOneCall({ path, onDenial: (d) => suite.reportDenial(d) });

    expect(readRecords(path)).toHaveLength(1);
    suite.close();
    relay.close();
  });

  test("a reporter that throws costs the report, never the record", async () => {
    const path = tempPath();
    const ui = await denyOneCall({
      path,
      onDenial: () => {
        throw new Error("relay is gone");
      },
    });

    expect(readRecords(path)).toHaveLength(1);
    expect(ui.notices.some((notice) => notice.message.includes("relay is gone"))).toBe(true);
  });

  test("a record that cannot be written is said out loud, and the call stays blocked", async () => {
    // A directory where the file should be: the write fails, nothing else does.
    const path = tempPath();
    const mode = new AutoMode({
      config: defaultAutoModeConfig,
      ledger: {
        record() {
          throw new Error("EACCES");
        },
      },
    });
    const pi = new FakePi();
    mode.register(pi);
    pi.start(uiContext(new FakeUI(), { modelRegistry: new DenyingRegistry() }));
    const ui = new FakeUI();

    expect((await pi.toolCall?.(push(), uiContext(ui)))?.block).toBe(true);
    const complaint = ui.notices.find((notice) => notice.message.includes("local record"));
    expect(complaint?.message).toContain("EACCES");
    expect(complaint?.type).toBe("error");
    expect(() => statSync(path)).toThrow();
  });

  test("the record is written before the report, so a slow relay cannot reorder them", async () => {
    const path = tempPath();
    let recordedWhenReported = 0;
    await denyOneCall({
      path,
      onDenial: () => {
        recordedWhenReported = readRecords(path).length;
      },
    });
    expect(recordedWhenReported).toBe(1);
  });
});

describe("what the ledger admits it lost", () => {
  test("rotation keeps the previous generation and counts nothing as dropped yet", () => {
    const path = tempPath();
    const ledger = new DenialLedger(path, "sess-1", 100);

    ledger.record(denial({ toolCallId: "one" }));
    ledger.record(denial({ toolCallId: "two" }));

    expect(readRecords(`${path}.1`).map((record) => record.tool_call_id)).toEqual(["one"]);
    expect(readRecords(path)).toHaveLength(1);
    expect(readRecords(path)[0]?.tool_call_id).toBe("two");
  });

  test("a second rotation drops a generation, and the count travels in the file", () => {
    const path = tempPath();
    const ledger = new DenialLedger(path, "sess-1", 100);

    for (const id of ["one", "two", "three", "four"]) ledger.record(denial({ toolCallId: id }));

    // Four records, one per generation: "one" and "two" are gone for good and
    // the active file says so, while "three" and "four" are still on disk.
    expect(readMarkers(path).at(-1)?.dropped).toBe(2);
    expect(readRecords(path).map((record) => record.tool_call_id)).toEqual(["four"]);
    expect(readRecords(`${path}.1`).map((record) => record.tool_call_id)).toEqual(["three"]);
  });

  test("a marker is not a denial", () => {
    const path = tempPath();
    writeFileSync(path, `${JSON.stringify({ type: "rotated", dropped: 3, at: "2026-08-18T10:00:00.000Z" })}\n`);
    const ledger = new DenialLedger(path, "sess-1", 1);

    ledger.record(denial());

    // The new active file carries the 3 the marker already claimed, plus the
    // zero records the rotated generation held.
    expect(readMarkers(path)[0]?.dropped).toBe(3);
    expect(readRecords(path)).toHaveLength(1);
  });
});
