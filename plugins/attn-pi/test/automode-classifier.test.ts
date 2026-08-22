import { describe, expect, test } from "bun:test";
import type { ClassifierRequest } from "../automode/classifier";
import { defaultAutoModeConfig, type AutoModeConfig } from "../automode/config";
import {
  attemptsPerModel,
  classifierThinkingLevel,
  ModelClassifier,
  type CompletionContext,
  type CompletionOptions,
  type CompletionResult,
  type ModelLike,
  type ModelRegistryLike,
  type ProviderLike,
  type RequestAuthLike,
} from "../automode/model-classifier";
import { classifierSystemPrompt, parseVerdict } from "../automode/prompt";
import type { TranscriptEntry } from "../automode/transcript";
import type { UsageLike } from "../automode/usage";

type Call = { model: ModelLike; context: CompletionContext; options?: CompletionOptions };

const judgedOn = (layer: "2a" | "2b") => ({ layer, system: expect.any(String), user: expect.any(String) });

class FakeRegistry implements ModelRegistryLike {
  readonly calls: Call[] = [];
  auth: RequestAuthLike = { ok: true, apiKey: "key", headers: { "x-test": "1" } };
  baseUrl: string | undefined;
  private readonly answers: (CompletionResult | Error)[];

  constructor(
    answers: (CompletionResult | Error)[],
    private readonly missing: readonly string[] = [],
  ) {
    this.answers = [...answers];
  }

  find(provider: string, modelId: string): ModelLike | undefined {
    if (this.missing.includes(`${provider}/${modelId}`)) return undefined;
    return { provider, id: modelId };
  }

  getProvider(provider: string): ProviderLike | undefined {
    if (this.missing.includes(provider)) return undefined;
    return {
      streamSimple: (model, context, options) => {
        this.calls.push({ model, context, options });
        return {
          result: async () => {
            const answer = this.answers.shift();
            if (answer === undefined) throw new Error("fake registry ran out of answers");
            if (answer instanceof Error) throw answer;
            return answer;
          },
        };
      },
    };
  }

  async getApiKeyAndHeaders(): Promise<RequestAuthLike> {
    return this.auth;
  }

  async getProviderAuth() {
    return this.baseUrl === undefined ? undefined : { auth: { baseUrl: this.baseUrl } };
  }
}

function says(text: string, usage?: UsageLike): CompletionResult {
  return { content: [{ type: "text", text }], usage, stopReason: "stop" };
}

function errors(count: number, text: string): Error[] {
  return Array.from({ length: count }, () => new Error(text));
}

function providerErrors(count: number, text: string): CompletionResult[] {
  return Array.from({ length: count }, () => ({ stopReason: "error", errorMessage: text }));
}

function verdictJSON(verdict: string, reason: string, highStakes = false): CompletionResult {
  return says(JSON.stringify({ verdict, reason, high_stakes: highStakes }));
}

function request(overrides: Partial<ClassifierRequest> = {}): ClassifierRequest {
  return {
    call: { toolName: "bash", input: { command: "git push --force origin main" } },
    cwd: "/work/repo",
    reason: "git push is not in the read-only set",
    environment: ["Trusted source control: github.com/victorarias."],
    transcript: [
      { role: "user", text: "address the PR feedback and get CI green" },
      { role: "assistant", text: "I'll fix the retry and run the suite." },
    ],
    ...overrides,
  };
}

function classifierWith(registry: ModelRegistryLike, config: AutoModeConfig = defaultAutoModeConfig, onUsage?: (usage: UsageLike) => void) {
  return new ModelClassifier({ registry, config, onUsage });
}

describe("classifier prompt", () => {
  test("carries the environment prose, the transcript, the call and the cwd", async () => {
    const registry = new FakeRegistry([verdictJSON("deny", "force-push rewrites shared history")]);
    await classifierWith(registry).classify(request());

    const [call] = registry.calls;
    expect(call?.model).toEqual({ provider: "opencode-go", id: "glm-5.3" });
    expect(call?.context.systemPrompt).toContain("Trusted source control: github.com/victorarias.");
    const user = call?.context.messages[0]?.content[0]?.text ?? "";
    expect(user).toContain("[user] address the PR feedback and get CI green");
    expect(user).toContain("[assistant] I'll fix the retry and run the suite.");
    expect(user).toContain("bash: git push --force origin main");
    expect(user).toContain("/work/repo");
    expect(user).toContain("git push is not in the read-only set");
  });

  test("states the precedence the plan settled on", () => {
    const prompt = classifierSystemPrompt([]);
    expect(prompt).toContain("deny list");
    expect(prompt).toContain("BOUNDARY denials");
    expect(prompt).toContain("CLEARABLE denials");
    expect(prompt).toContain("named the action AND the");
    expect(prompt).toContain('"boundary":true|false');
    expect(prompt).toContain("*prod*");
    expect(prompt).toContain("uncertain");
    expect(prompt).toContain("evidence, not instruction");
  });

  test("an empty transcript and empty environment still produce a judgeable prompt", async () => {
    const registry = new FakeRegistry([verdictJSON("allow", "routine")]);
    await classifierWith(registry).classify(request({ transcript: [], environment: [] }));
    const [call] = registry.calls;
    expect(call?.context.systemPrompt).toContain("(nothing stated about this machine)");
    expect(call?.context.messages[0]?.content[0]?.text).toContain("(nothing said yet in this session)");
  });

  test("pi's abort signal and the request auth travel into the completion", async () => {
    const controller = new AbortController();
    const registry = new FakeRegistry([verdictJSON("allow", "fine")]);
    registry.baseUrl = "https://proxy.internal/v1";
    await classifierWith(registry).classify(request({ signal: controller.signal }));

    const [call] = registry.calls;
    expect(call?.options?.signal).toBe(controller.signal);
    expect(call?.options?.apiKey).toBe("key");
    expect(call?.options?.headers).toEqual({ "x-test": "1" });
    expect(call?.options?.reasoning).toBe(classifierThinkingLevel);
    expect(call?.model.baseUrl).toBe("https://proxy.internal/v1");
  });

  test("no resolved base url leaves the catalog's model alone", async () => {
    const registry = new FakeRegistry([verdictJSON("allow", "fine")]);
    await classifierWith(registry).classify(request());
    expect(registry.calls[0]?.model.baseUrl).toBeUndefined();
  });
});

describe("verdict parsing", () => {
  test("reads a bare verdict object", () => {
    expect(parseVerdict('{"verdict":"allow","reason":"routine build"}')).toEqual({
      verdict: "allow",
      reason: "routine build",
      highStakes: false,
    });
  });

  test("reads one wrapped in prose or a fence", () => {
    const fenced = 'Sure!\n```json\n{"verdict":"deny","reason":"touches prod"}\n```\n';
    expect(parseVerdict(fenced).verdict).toBe("deny");
    expect(parseVerdict('thinking... {"verdict":"uncertain","reason":"unknown script"} done').verdict).toBe(
      "uncertain",
    );
  });

  test("reads the high-stakes flag", () => {
    expect(parseVerdict('{"verdict":"allow","reason":"ok","high_stakes":true}').highStakes).toBe(true);
  });

  test("reads the boundary flag off a denial", () => {
    expect(parseVerdict('{"verdict":"deny","reason":"this leaves the machine","boundary":true}')).toMatchObject({
      verdict: "deny",
      boundary: true,
    });
    expect(parseVerdict('{"verdict":"deny","reason":"no"}').boundary).toBeUndefined();
  });

  test("a boundary flag on anything but a denial is ignored", () => {
    expect(parseVerdict('{"verdict":"allow","reason":"ok","boundary":true}').boundary).toBeUndefined();
    expect(parseVerdict('{"verdict":"uncertain","reason":"?","boundary":true}').boundary).toBeUndefined();
  });

  test("skips a leading object that is not a verdict", () => {
    const text = '{"note":"scratch"} then {"verdict":"deny","reason":"exfiltration"}';
    expect(parseVerdict(text)).toMatchObject({ verdict: "deny", reason: "exfiltration" });
  });

  test("malformed output fails closed, naming what came back", () => {
    for (const text of ["", "yes, allow it", "{", '{"verdict":"maybe"}', '{"verdict":']) {
      const parsed = parseVerdict(text);
      expect(parsed.verdict).toBe("deny");
      expect(parsed.reason).toContain("cannot read as a verdict");
    }
  });

  test("a model that answers nothing readable denies rather than throwing", async () => {
    const registry = new FakeRegistry([says("I'd rather not say.")]);
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(registry.calls).toHaveLength(1);
  });
});

describe("2a to 2b routing", () => {
  test("a confident verdict is the answer, and 2b never runs", async () => {
    const registry = new FakeRegistry([verdictJSON("allow", "routine work in the working directory")]);
    expect(await classifierWith(registry).classify(request())).toEqual({
      verdict: "allow",
      layer: "2a",
      reason: "routine work in the working directory",
    });
    expect(registry.calls).toHaveLength(1);
  });

  test("uncertain escalates to the escalation model", async () => {
    const registry = new FakeRegistry([
      verdictJSON("uncertain", "cannot tell what the script does"),
      verdictJSON("deny", "an unreviewed script can do anything"),
    ]);
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict).toEqual({
      verdict: "deny",
      layer: "2b",
      prompt: judgedOn("2b"),
      reason: "an unreviewed script can do anything",
    });
    expect(registry.calls.map((call) => `${call.model.provider}/${call.model.id}`)).toEqual([
      "opencode-go/glm-5.3",
      "opencode-go/qwen3.8-max",
    ]);
  });

  test("the escalation prompt carries the first pass as one opinion", async () => {
    const registry = new FakeRegistry([
      verdictJSON("uncertain", "cannot tell what the script does"),
      verdictJSON("allow", "the user asked for exactly this"),
    ]);
    await classifierWith(registry).classify(request());
    const escalation = registry.calls[1]?.context.systemPrompt ?? "";
    expect(escalation).toContain("cannot tell what the script does");
    expect(escalation).toContain("Yours is final");
    expect(registry.calls[1]?.context.messages[0]?.content[0]?.text).toEqual(
      registry.calls[0]?.context.messages[0]?.content[0]?.text,
    );
  });

  test("a confident verdict flagged high stakes is reviewed anyway", async () => {
    const registry = new FakeRegistry([
      verdictJSON("allow", "the user said to deploy", true),
      verdictJSON("deny", "the target is a production namespace"),
    ]);
    expect(await classifierWith(registry).classify(request())).toEqual({
      verdict: "deny",
      layer: "2b",
      prompt: judgedOn("2b"),
      reason: "the target is a production namespace",
    });
  });

  test("a confident deny is final, high stakes or not: the user overturns it by saying so", async () => {
    const registry = new FakeRegistry([verdictJSON("deny", "force-push rewrites shared history", true)]);
    expect(await classifierWith(registry).classify(request())).toEqual({
      verdict: "deny",
      layer: "2a",
      prompt: judgedOn("2a"),
      reason: "force-push rewrites shared history",
    });
    expect(registry.calls).toHaveLength(1);
  });

  test("uncertain out of 2b resolves to deny", async () => {
    const registry = new FakeRegistry([verdictJSON("uncertain", "no idea"), verdictJSON("uncertain", "still no idea")]);
    expect(await classifierWith(registry).classify(request())).toEqual({
      verdict: "deny",
      layer: "2b",
      prompt: judgedOn("2b"),
      reason: "still no idea",
    });
  });

  test("2b runs at most once", async () => {
    const registry = new FakeRegistry([verdictJSON("uncertain", "a"), verdictJSON("uncertain", "b", true)]);
    await classifierWith(registry).classify(request());
    expect(registry.calls).toHaveLength(2);
  });
});

describe("classification failures fail closed", () => {
  test("a model missing from the catalog denies, naming it", async () => {
    const registry = new FakeRegistry([], ["opencode-go/glm-5.3"]);
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("opencode-go/glm-5.3");
    expect(registry.calls).toHaveLength(0);
  });

  test("a model spec that names no provider denies", async () => {
    const registry = new FakeRegistry([]);
    const config: AutoModeConfig = { ...defaultAutoModeConfig, classifierModels: ["glm-5.3"] };
    const verdict = await classifierWith(registry, config).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("glm-5.3");
  });

  test("a provider that is not configured denies", async () => {
    const registry = new FakeRegistry([], ["opencode-go"]);
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("opencode-go");
  });

  test("an unresolvable credential denies, carrying the registry's own reason", async () => {
    const registry = new FakeRegistry([]);
    registry.auth = { ok: false, error: 'No API key found for "opencode-go"' };
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain('No API key found for "opencode-go"');
    expect(registry.calls).toHaveLength(0);
  });

  test("a completion that rejects denies, naming the failure", async () => {
    const registry = new FakeRegistry(errors(attemptsPerModel, "provider unreachable"));
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("provider unreachable");
  });

  test("a provider error message denies rather than parsing as a verdict", async () => {
    const registry = new FakeRegistry(providerErrors(attemptsPerModel, "404 model retired"));
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("404 model retired");
  });

  test("an aborted turn is not a verdict: it travels as a throw", async () => {
    const controller = new AbortController();
    controller.abort();
    const registry = new FakeRegistry([new Error("aborted")]);
    await expect(classifierWith(registry).classify(request({ signal: controller.signal }))).rejects.toThrow("aborted");
  });

  test("a stream that reports itself aborted throws too", async () => {
    const registry = new FakeRegistry([{ stopReason: "aborted" }]);
    await expect(classifierWith(registry).classify(request())).rejects.toThrow("aborted");
  });
});

describe("what a denial keeps of the prompt", () => {
  test("the recorded prompt is the bytes the provider was handed, not a rebuild", async () => {
    const registry = new FakeRegistry([verdictJSON("deny", "touches prod")]);
    const verdict = await classifierWith(registry).classify(request());
    const sent = registry.calls[0]?.context;
    expect(verdict).toMatchObject({
      verdict: "deny",
      prompt: { layer: "2a", system: sent?.systemPrompt, user: sent?.messages[0]?.content[0]?.text },
    });
  });

  test("an escalated deny keeps 2b's prompt, which is the one that decided", async () => {
    const registry = new FakeRegistry([verdictJSON("uncertain", "cannot tell"), verdictJSON("deny", "prod namespace")]);
    const verdict = await classifierWith(registry).classify(request());
    const escalation = registry.calls[1]?.context;
    expect(verdict).toMatchObject({ verdict: "deny", prompt: { layer: "2b", system: escalation?.systemPrompt } });
    expect((verdict as { prompt?: { system: string } }).prompt?.system).toContain("Yours is final");
  });

  test("an outage keeps the prompt nobody read — it is what tells it from a bad window", async () => {
    const registry = new FakeRegistry(errors(2 * attemptsPerModel, "503 upstream"));
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", unavailable: true, prompt: judgedOn("2a") });
  });

  test("an allow carries none: a call that ran needs no forensics", async () => {
    const registry = new FakeRegistry([verdictJSON("allow", "routine work")]);
    expect(await classifierWith(registry).classify(request())).not.toHaveProperty("prompt");
  });
});

describe("the fallback walk", () => {
  const twoDeep: AutoModeConfig = {
    ...defaultAutoModeConfig,
    classifierModels: ["vendor/primary", "vendor/fallback"],
    escalationModels: ["vendor/big", "vendor/bigger"],
  };
  const specs = (registry: FakeRegistry) => registry.calls.map((call) => `${call.model.provider}/${call.model.id}`);

  test("an unreachable model is retried once, then the walk moves on", async () => {
    const registry = new FakeRegistry([...errors(attemptsPerModel, "503 upstream"), verdictJSON("allow", "routine")]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toEqual({
      verdict: "allow",
      layer: "2a",
      reason: "routine",
    });
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/primary", "vendor/fallback"]);
  });

  test("a provider error advances the walk the same way a throw does", async () => {
    const registry = new FakeRegistry([
      ...providerErrors(attemptsPerModel, "503 Service Unavailable"),
      verdictJSON("deny", "force-push rewrites shared history"),
    ]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toEqual({
      verdict: "deny",
      layer: "2a",
      prompt: judgedOn("2a"),
      reason: "force-push rewrites shared history",
    });
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/primary", "vendor/fallback"]);
  });

  test("the retry is enough on its own: a blip never reaches the fallback", async () => {
    const registry = new FakeRegistry([new Error("connection reset"), verdictJSON("allow", "routine")]);
    await classifierWith(registry, twoDeep).classify(request());
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/primary"]);
  });

  test("a deny is a verdict, so the next model is never asked", async () => {
    const registry = new FakeRegistry([verdictJSON("deny", "touches prod")]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toEqual({
      verdict: "deny",
      layer: "2a",
      prompt: judgedOn("2a"),
      reason: "touches prod",
    });
    expect(specs(registry)).toEqual(["vendor/primary"]);
  });

  test("output that reads as no verdict is still an answer, and ends the walk", async () => {
    const registry = new FakeRegistry([says("I'd rather not say.")]);
    const verdict = await classifierWith(registry, twoDeep).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("cannot read as a verdict");
    expect(specs(registry)).toEqual(["vendor/primary"]);
  });

  test("an abort ends the walk instead of advancing it", async () => {
    const controller = new AbortController();
    controller.abort();
    const registry = new FakeRegistry(errors(6, "aborted"));
    await expect(classifierWith(registry, twoDeep).classify(request({ signal: controller.signal }))).rejects.toThrow(
      "aborted",
    );
    expect(specs(registry)).toEqual(["vendor/primary"]);
  });

  test("an exhausted list blocks as an outage, naming the layer, the models and the last failure", async () => {
    const registry = new FakeRegistry([
      ...errors(attemptsPerModel, "503 upstream"),
      ...providerErrors(attemptsPerModel, "502 bad gateway"),
    ]);
    const verdict = await classifierWith(registry, twoDeep).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", layer: "2a", unavailable: true });
    expect(verdict.reason).toContain("classifier model (layer 2a)");
    expect(verdict.reason).toContain("vendor/primary, vendor/fallback");
    expect(verdict.reason).toContain("502 bad gateway");
    expect(verdict.reason).toContain("outage");
    expect(specs(registry)).toHaveLength(2 * attemptsPerModel);
  });

  test("escalation walks its own list", async () => {
    const registry = new FakeRegistry([
      verdictJSON("uncertain", "cannot tell what the script does"),
      ...errors(attemptsPerModel, "503 upstream"),
      verdictJSON("allow", "the user asked for exactly this"),
    ]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toEqual({
      verdict: "allow",
      layer: "2b",
      reason: "the user asked for exactly this",
    });
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/big", "vendor/big", "vendor/bigger"]);
  });

  test("an exhausted escalation list blocks as a 2b outage", async () => {
    const registry = new FakeRegistry([
      verdictJSON("uncertain", "cannot tell what the script does"),
      ...errors(2 * attemptsPerModel, "503 upstream"),
    ]);
    const verdict = await classifierWith(registry, twoDeep).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", layer: "2b", unavailable: true });
    expect(verdict.reason).toContain("escalation model (layer 2b)");
    expect(verdict.reason).toContain("vendor/big, vendor/bigger");
  });

  test("a model missing from the catalog is walked past, not judged", async () => {
    const registry = new FakeRegistry([verdictJSON("allow", "routine")], ["vendor/primary"]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toMatchObject({
      verdict: "allow",
      layer: "2a",
    });
    expect(specs(registry)).toEqual(["vendor/fallback"]);
  });
});

describe("usage accounting", () => {
  test("every layer's usage is reported", async () => {
    const reported: UsageLike[] = [];
    const registry = new FakeRegistry([
      says(JSON.stringify({ verdict: "uncertain", reason: "hm" }), { input: 900, output: 20, cost: { total: 0.0004 } }),
      says(JSON.stringify({ verdict: "deny", reason: "no" }), { input: 950, output: 25, cost: { total: 0.0019 } }),
    ]);
    await classifierWith(registry, defaultAutoModeConfig, (usage) => reported.push(usage)).classify(request());
    expect(reported).toHaveLength(2);
    expect(reported.map((usage) => usage.cost?.total)).toEqual([0.0004, 0.0019]);
  });

  test("a failed completion reports nothing rather than a zero", async () => {
    const reported: UsageLike[] = [];
    const registry = new FakeRegistry([new Error("down")]);
    await classifierWith(registry, defaultAutoModeConfig, (usage) => reported.push(usage)).classify(request());
    expect(reported).toEqual([]);
  });
});

describe("the transcript the classifier is handed", () => {
  test("is rendered oldest first, one line per turn", async () => {
    const transcript: TranscriptEntry[] = [
      { role: "user", text: "don't push until I review the diff" },
      { role: "assistant", text: "understood, I'll hold off." },
    ];
    const registry = new FakeRegistry([verdictJSON("deny", "the user stated a boundary")]);
    await classifierWith(registry).classify(request({ transcript }));
    const user = registry.calls[0]?.context.messages[0]?.content[0]?.text ?? "";
    expect(user.indexOf("[user] don't push")).toBeLessThan(user.indexOf("[assistant] understood"));
  });
});
