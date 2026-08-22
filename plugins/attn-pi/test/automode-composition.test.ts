// How auto mode is turned on: the config it is handed, the flags and the
// `/auto` command that override it, and the gate that keeps a bare pi
// untouched.
import { describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { RelayServer } from "../src/relay";
import { AttnPiSuite } from "../suite/core";
import { defaultAutoModeConfig } from "../automode/config";
import { AutoMode } from "../automode/mode";
import type {
  CompletionResult,
  ModelLike,
  ModelRegistryLike,
  ProviderLike,
  RequestAuthLike,
} from "../automode/model-classifier";
import {
  attnAutoModeSource,
  autoModeConfigEnvVar,
  autoModeConfigFileName,
  autoModeConfigFilePath,
  standaloneAutoModeSource,
} from "../automode/source";
import { autoModeStatusKey } from "../automode/ui";
import { FakePi, FakeUI, toolCall, uiContext } from "./automode-fake-pi";

/** Answers every completion with one verdict, and counts what it was asked. */
class CountingRegistry implements ModelRegistryLike {
  calls = 0;

  constructor(private readonly answer: CompletionResult) {}

  find(provider: string, id: string): ModelLike | undefined {
    return { provider, id };
  }

  getProvider(): ProviderLike {
    return {
      streamSimple: () => ({
        result: async () => {
          this.calls += 1;
          return this.answer;
        },
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

function denies(): CompletionResult {
  return {
    content: [{ type: "text", text: "<severity>80</severity><category>Irreversible Local Destruction</category>" }],
    stopReason: "stop",
  };
}

const push = () => toolCall("bash", { command: "git push --force origin main" });

describe("the config a session is handed", () => {
  test("no environment variable means attn sent nothing at all", () => {
    expect(attnAutoModeSource({})).toBeUndefined();
    expect(attnAutoModeSource({ [autoModeConfigEnvVar]: "   " })).toBeUndefined();
  });

  test("attn's JSON becomes the config auto mode runs on", () => {
    const source = attnAutoModeSource({
      [autoModeConfigEnvVar]: JSON.stringify({
        enabled_default: false,
        environment: ["never touch production data"],
        allow: ["git push origin*"],
        classifier_model: "opencode-go/glm-5.3",
      }),
    });
    expect(source?.problem).toBeUndefined();
    expect(source?.config.enabledDefault).toBe(false);
    expect(source?.config.environment).toEqual(["never touch production data"]);
    expect(source?.config.allow).toEqual(["git push origin*"]);
  });

  test("a config that cannot be read leaves auto mode on the shipped defaults, and says so", () => {
    const source = attnAutoModeSource({ [autoModeConfigEnvVar]: "{ not json" });
    expect(source?.config).toEqual(defaultAutoModeConfig);
    expect(source?.problem).toContain("shipped defaults");
  });

  test("bare pi with no file gets auto mode off", () => {
    const source = standaloneAutoModeSource({}, () => undefined);
    expect(source.config.enabledDefault).toBe(false);
    expect(source.problem).toBeUndefined();
  });

  test("bare pi reads the file, and only an explicit enabled_default turns it on", () => {
    const read = (contents: string) => () => contents;
    expect(standaloneAutoModeSource({}, read(JSON.stringify({ enabled_default: true }))).config.enabledDefault).toBe(
      true,
    );
    expect(standaloneAutoModeSource({}, read(JSON.stringify({ allow: ["ls *"] }))).config.enabledDefault).toBe(false);
    expect(standaloneAutoModeSource({}, read(JSON.stringify({ allow: ["ls *"] }))).config.allow).toEqual(["ls *"]);
  });

  test("bare pi prefers the environment attn set, so `pi -e automode.js` inside attn agrees with the suite", () => {
    const source = standaloneAutoModeSource(
      { [autoModeConfigEnvVar]: JSON.stringify({ enabled_default: true, environment: ["from attn"] }) },
      () => {
        throw new Error("the file must not be read when attn sent a config");
      },
    );
    expect(source.config.environment).toEqual(["from attn"]);
  });

  test("an unreadable file is reported rather than silently ignored", () => {
    const source = standaloneAutoModeSource({}, () => {
      throw new Error("EACCES");
    });
    expect(source.problem).toContain("EACCES");
    expect(source.config.enabledDefault).toBe(false);
  });

  test("the file lives under pi's config directory", () => {
    expect(autoModeConfigFilePath({ PI_CODING_AGENT_DIR: "/tmp/pi" })).toBe(join("/tmp/pi", autoModeConfigFileName));
    expect(autoModeConfigFilePath({})).toContain(join(".pi", "agent", autoModeConfigFileName));
  });
});

describe("turning auto mode on and off", () => {
  test("the configured default decides a session nobody told otherwise", () => {
    expect(new AutoMode({ config: { ...defaultAutoModeConfig, enabledDefault: true } }).enabled()).toBe(true);
    expect(new AutoMode({ config: { ...defaultAutoModeConfig, enabledDefault: false } }).enabled()).toBe(false);
  });

  test("a launch flag outranks the configured default, in both directions", () => {
    const on = new AutoMode({ config: { ...defaultAutoModeConfig, enabledDefault: false } });
    on.register(new FakePi().pass("auto"));
    expect(on.enabled()).toBe(true);

    const off = new AutoMode({ config: { ...defaultAutoModeConfig, enabledDefault: true } });
    off.register(new FakePi().pass("no-auto"));
    expect(off.enabled()).toBe(false);
  });

  test("a session given both flags starts off", () => {
    const mode = new AutoMode({ config: defaultAutoModeConfig });
    mode.register(new FakePi().pass("auto").pass("no-auto"));
    expect(mode.enabled()).toBe(false);
  });

  test("/auto toggles, outranks the flag it was typed to override, and says where it landed", async () => {
    const ui = new FakeUI();
    const mode = new AutoMode({ config: { ...defaultAutoModeConfig, enabledDefault: true } });
    const pi = new FakePi().pass("auto");
    mode.register(pi);

    await pi.run("auto", "", uiContext(ui));
    expect(mode.enabled()).toBe(false);
    expect(ui.statuses.get(autoModeStatusKey)).toBe("auto: off");
    expect(ui.notices.at(-1)?.message).toContain("auto mode is off");

    await pi.run("auto", "on", uiContext(ui));
    expect(mode.enabled()).toBe(true);
    expect(ui.statuses.get(autoModeStatusKey)).toBe("auto: on");
  });

  test("/auto status reports without changing anything", async () => {
    const ui = new FakeUI();
    const mode = new AutoMode({ config: defaultAutoModeConfig });
    const pi = new FakePi();
    mode.register(pi);
    await pi.run("auto", "status", uiContext(ui));
    expect(mode.enabled()).toBe(true);
    expect(ui.notices.at(-1)?.message).toContain("auto mode is on");
  });

  test("/auto refuses an argument it does not understand, and changes nothing", async () => {
    const ui = new FakeUI();
    const mode = new AutoMode({ config: defaultAutoModeConfig });
    const pi = new FakePi();
    mode.register(pi);
    await pi.run("auto", "maybe", uiContext(ui));
    expect(mode.enabled()).toBe(true);
    expect(ui.notices.at(-1)?.type).toBe("error");
    expect(ui.statuses.size).toBe(0);
  });

  test("the status is painted as the session opens, before anything is asked of it", () => {
    const ui = new FakeUI();
    const mode = new AutoMode({ config: defaultAutoModeConfig });
    const pi = new FakePi();
    mode.register(pi);
    pi.start(uiContext(ui));
    expect(ui.statuses.get(autoModeStatusKey)).toBe("auto: on");
  });

  test("a broken config is said once, however many session transitions follow", () => {
    const ui = new FakeUI();
    const mode = new AutoMode({ config: defaultAutoModeConfig, notice: "the config could not be read" });
    const pi = new FakePi();
    mode.register(pi);
    pi.start(uiContext(ui));
    pi.start(uiContext(ui));
    expect(ui.notices.filter((notice) => notice.message.includes("could not be read"))).toHaveLength(1);
  });

  test("auto mode off judges nothing, so a toggled-off session costs no classifier call", async () => {
    const registry = new CountingRegistry(denies());
    const mode = new AutoMode({ config: { ...defaultAutoModeConfig, enabledDefault: false } });
    const pi = new FakePi();
    mode.register(pi);
    pi.start(uiContext(new FakeUI(), { modelRegistry: registry }));

    expect(await pi.toolCall?.(push(), uiContext(new FakeUI()))).toBeUndefined();
    expect(registry.calls).toBe(0);
  });

  test("the classifier is built from the session's own model registry", async () => {
    const registry = new CountingRegistry(denies());
    const mode = new AutoMode({ config: defaultAutoModeConfig });
    const pi = new FakePi();
    mode.register(pi);
    pi.start(uiContext(new FakeUI(), { modelRegistry: registry }));

    const blocked = await pi.toolCall?.(push(), uiContext(new FakeUI()));
    expect(blocked?.block).toBe(true);
    expect(blocked?.reason).toContain("Irreversible Local Destruction");
    expect(registry.calls).toBe(2);
  });

  test("a session with no model catalog refuses the call and names why", async () => {
    const mode = new AutoMode({ config: defaultAutoModeConfig });
    const pi = new FakePi();
    mode.register(pi);

    const blocked = await pi.toolCall?.(push(), uiContext(new FakeUI()));
    expect(blocked?.block).toBe(true);
    expect(blocked?.reason).toContain("no model catalog");
  });
});

// The seam suite/index.ts wires: a denied call in the session becomes one
// suite.report_denial on the driver's relay socket. Everything here is real
// except pi itself and the driver behind the socket.
describe("a denial leaving the session", () => {
  test("reaches the relay with the call, the reason and the layer that decided", async () => {
    const socketPath = join(mkdtempSync(join(tmpdir(), "attn-pi-denial-")), "s.sock");
    const reported: unknown[] = [];
    const relay = new RelayServer({
      socketPath,
      delegate: {
        async suiteHello() {
          return { ok: true as const };
        },
        async suiteReportState() {},
        async suiteReportStop() {},
        async suiteReportDenial(params: unknown) {
          reported.push(params);
        },
      },
    });
    await relay.listen();
    const suite = new AttnPiSuite({ socketPath, token: "tok-denial", piVersion: "0.83.0" });
    const mode = new AutoMode({
      config: defaultAutoModeConfig,
      onDenial: (denial) => suite.reportDenial(denial),
    });
    const pi = new FakePi();
    mode.register(pi);
    pi.start(uiContext(new FakeUI(), { modelRegistry: new CountingRegistry(denies()) }));

    expect((await pi.toolCall?.(push(), uiContext(new FakeUI())))?.block).toBe(true);

    const deadline = Date.now() + 2_000;
    while (reported.length === 0) {
      if (Date.now() > deadline) throw new Error("no denial reached the relay");
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
    expect(reported[0]).toEqual({
      token: "tok-denial",
      tool: "bash",
      action: "bash: git push --force origin main",
      reason: "the classifier placed this call at severity 80 under the Irreversible Local Destruction rule",
      rule: "classifier-intent",
      at: expect.any(String),
    });

    suite.close();
    relay.close();
  });
});
