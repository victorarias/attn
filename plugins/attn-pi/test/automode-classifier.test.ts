import { describe, expect, test } from "bun:test";
import type { ClassifierRequest } from "../automode/classifier";
import { defaultAutoModeConfig, type AutoModeConfig } from "../automode/config";
import {
  attemptsPerModel,
  classifierCacheRetention,
  classifierThinkingLevel,
  harmMaxTokens,
  intentMaxTokens,
  ModelClassifier,
  type CompletionContext,
  type CompletionOptions,
  type CompletionResult,
  type ModelLike,
  type ModelRegistryLike,
  type ProviderLike,
  type RequestAuthLike,
} from "../automode/model-classifier";
import { blockLine, parseSeverity, stageOneAllowCeiling } from "../automode/prompt";
import type { TranscriptEntry } from "../automode/transcript";
import type { UsageLike } from "../automode/usage";

type Call = { model: ModelLike; context: CompletionContext; options?: CompletionOptions };

const judgedOn = (layer: "harm" | "intent") => ({ layer, system: expect.any(String), user: expect.any(String) });

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

function graded(severity: number, category?: string, usage?: UsageLike): CompletionResult {
  const tail = category ? `<category>${category}</category>` : "";
  return says(`<severity>${severity}</severity>${tail}`, usage);
}

const routine = () => graded(stageOneAllowCeiling - 5);

const dangerous = () => graded(stageOneAllowCeiling + 20);

function messagesOf(call: Call | undefined): string[] {
  return (call?.context.messages ?? []).map((message) => message.content[0]?.text ?? "");
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

function classifierWith(
  registry: ModelRegistryLike,
  config: AutoModeConfig = defaultAutoModeConfig,
  onUsage?: (usage: UsageLike) => void,
  sessionKey?: string,
) {
  return new ModelClassifier({ registry, config, onUsage, ...(sessionKey ? { sessionKey } : {}) });
}

describe("classifier prompt", () => {
  test("carries the environment prose, the transcript, the call and the cwd", async () => {
    const registry = new FakeRegistry([dangerous(), graded(80, "Irreversible Local Destruction")]);
    await classifierWith(registry).classify(request());

    const [call] = registry.calls;
    expect(call?.model).toEqual({ provider: "opencode-go", id: "glm-5.3" });
    expect(call?.context.systemPrompt).toContain("Trusted source control: github.com/victorarias.");
    const user = messagesOf(call).join("\n");
    expect(user).toContain('{"user":"address the PR feedback and get CI green"}');
    expect(user).toContain('{"assistant":"I\'ll fix the retry and run the suite."}');
    expect(user).toContain('{"bash":"git push --force origin main"}');
    expect(user).toContain("/work/repo");
    expect(user).toContain("git push is not in the read-only set");
  });

  test("an empty transcript and empty environment still produce a judgeable prompt", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request({ transcript: [], environment: [] }));
    const [call] = registry.calls;
    expect(call?.context.systemPrompt).toContain("(nothing stated about this machine)");
    expect(messagesOf(call)[0]).toContain("(nothing said yet in this session)");
  });

  test("pi's abort signal and the request auth travel into the completion", async () => {
    const controller = new AbortController();
    const registry = new FakeRegistry([routine()]);
    registry.baseUrl = "https://proxy.internal/v1";
    await classifierWith(registry).classify(request({ signal: controller.signal }));

    const [call] = registry.calls;
    expect(call?.options?.signal).toBe(controller.signal);
    expect(call?.options?.apiKey).toBe("key");
    expect(call?.options?.headers).toEqual({ "x-test": "1" });
    expect(call?.options?.reasoning).toBe(classifierThinkingLevel);
    expect(call?.model.baseUrl).toBe("https://proxy.internal/v1");
  });

  test("one cache key covers both passes, stable across the session's calls", async () => {
    const registry = new FakeRegistry([dangerous(), graded(80, "Git Destructive"), routine()]);
    const classifier = classifierWith(registry, defaultAutoModeConfig, undefined, "session-7");
    await classifier.classify(request());
    await classifier.classify(request());

    expect(registry.calls.map((call) => call.options?.sessionId)).toEqual([
      "session-7",
      "session-7",
      "session-7",
    ]);
    expect(registry.calls.every((call) => call.options?.cacheRetention === classifierCacheRetention)).toBe(true);
  });

  test("a session with no key still classifies", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request());
    expect(registry.calls[0]?.options?.sessionId).toBeUndefined();
  });

  test("the cheap stage is capped far tighter than the escalation", async () => {
    const registry = new FakeRegistry([dangerous(), graded(80, "Git Destructive")]);
    await classifierWith(registry).classify(request());
    expect(registry.calls.map((call) => call.options?.maxTokens)).toEqual([harmMaxTokens, intentMaxTokens]);
  });

  test("no resolved base url leaves the catalog's model alone", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request());
    expect(registry.calls[0]?.model.baseUrl).toBeUndefined();
  });
});

describe("the session's opening message rides in its own seat", () => {
  test("it goes ahead of the transcript, with the policy that bounds it", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request({ grant: "force-push your own branch whenever you need to" }));

    const messages = messagesOf(registry.calls[0]);
    expect(messages).toHaveLength(2);
    expect(messages[0]).toContain("force-push your own branch whenever you need to");
    expect(messages[0]).toContain("explicitly authorizes the SPECIFIC action");
    expect(messages[0]).toContain("is not authorization");
    expect(messages[1]).toContain('{"bash":"git push --force origin main"}');
  });

  test("no opening message means no extra seat", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request());
    expect(messagesOf(registry.calls[0])).toHaveLength(1);
  });

  test("a blank opening message means no extra seat either", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request({ grant: "   " }));
    expect(messagesOf(registry.calls[0])).toHaveLength(1);
  });

  test("both stages see it", async () => {
    const registry = new FakeRegistry([dangerous(), graded(80, "Production Writes")]);
    await classifierWith(registry).classify(request({ grant: "deploy to prod when the tests pass" }));
    expect(messagesOf(registry.calls[1])[0]).toContain("deploy to prod when the tests pass");
  });
});

describe("severity parsing", () => {
  test("reads a bare severity tag", () => {
    expect(parseSeverity("<severity>7</severity>")).toEqual({ severity: 7 });
  });

  test("reads a decimal and an unclosed tag", () => {
    expect(parseSeverity("<severity>12.5</severity>")?.severity).toBe(12.5);
    expect(parseSeverity("<severity>40")?.severity).toBe(40);
  });

  test("reads the category beside the severity", () => {
    expect(parseSeverity("<severity>90</severity><category>Data Exfiltration</category>")).toEqual({
      severity: 90,
      category: "Data Exfiltration",
    });
  });

  test("keeps the reasoning that came with it, and does not read a severity out of it", () => {
    const text = "<thinking>the user never named <severity>1</severity> anything</thinking><severity>88</severity>";
    expect(parseSeverity(text)).toEqual({
      severity: 88,
      thinking: "the user never named <severity>1</severity> anything",
    });
  });

  test("two severities are no answer at all", () => {
    expect(parseSeverity("<severity>10</severity> or maybe <severity>90</severity>")).toBeUndefined();
  });

  test("an unclosed thinking block is no answer", () => {
    expect(parseSeverity("<thinking>still going <severity>10</severity>")).toBeUndefined();
  });

  test("output with no severity at all is no answer", () => {
    for (const text of ["", "I'd rather not say.", "{", "severity: high"]) {
      expect(parseSeverity(text)).toBeUndefined();
    }
  });
});

describe("the harm stage and the intent stage", () => {
  test("harm below the ceiling allows, and the intent stage never runs", async () => {
    const registry = new FakeRegistry([graded(stageOneAllowCeiling)]);
    expect(await classifierWith(registry).classify(request())).toEqual({
      verdict: "allow",
      layer: "harm",
      severity: stageOneAllowCeiling,
    });
    expect(registry.calls).toHaveLength(1);
  });

  test("harm above the ceiling goes to the intent model, which can still clear it", async () => {
    const registry = new FakeRegistry([dangerous(), graded(blockLine)]);
    expect(await classifierWith(registry).classify(request())).toEqual({
      verdict: "allow",
      layer: "intent",
      severity: blockLine,
    });
    expect(registry.calls.map((call) => `${call.model.provider}/${call.model.id}`)).toEqual([
      "opencode-go/glm-5.3",
      "opencode-go/qwen3.8-max",
    ]);
  });

  test("past the block line the intent stage refuses, carrying the rule it named", async () => {
    const registry = new FakeRegistry([dangerous(), graded(blockLine + 1, "Irreversible Local Destruction")]);
    expect(await classifierWith(registry).classify(request())).toMatchObject({
      verdict: "deny",
      layer: "intent",
      severity: blockLine + 1,
      category: "Irreversible Local Destruction",
      prompt: judgedOn("intent"),
    });
  });

  // Both passes send the rulebook byte for byte, so the provider caches one
  // copy per session. Only the last user message says which pass this is.
  test("both passes share one system prompt, and the pass is named in the message", async () => {
    const registry = new FakeRegistry([dangerous(), graded(90, "Data Exfiltration")]);
    await classifierWith(registry).classify(request());
    const first = registry.calls[0]?.context.systemPrompt;
    expect(first).toContain("You are a security monitor");
    expect(registry.calls[1]?.context.systemPrompt).toBe(first);
    expect(messagesOf(registry.calls[0]).at(-1)).toContain("This is pass 1");
    expect(messagesOf(registry.calls[1]).at(-1)).toContain("This is pass 2");
    expect(messagesOf(registry.calls[1]).at(-1)).toContain("<thinking>");
  });

  test("the one hard block is the one denial an approval cannot lift", async () => {
    const registry = new FakeRegistry([dangerous(), graded(98, "Data Exfiltration")]);
    expect(await classifierWith(registry).classify(request())).toMatchObject({
      verdict: "deny",
      category: "Data Exfiltration",
      boundary: true,
    });
  });

  test("a soft block stays clearable by the user", async () => {
    const registry = new FakeRegistry([dangerous(), graded(70, "Git Destructive")]);
    expect(await classifierWith(registry).classify(request())).not.toHaveProperty("boundary");
  });

  test("the model's own reasoning becomes the reason the agent is told", async () => {
    const registry = new FakeRegistry([
      dangerous(),
      says("<thinking>main is shared and nobody asked for this</thinking><severity>80</severity><category>Irreversible Local Destruction</category>"),
    ]);
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.reason).toContain("Irreversible Local Destruction");
    expect(verdict.reason).toContain("main is shared and nobody asked for this");
  });

  test("the intent stage runs at most once", async () => {
    const registry = new FakeRegistry([dangerous(), graded(90, "Production Writes")]);
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

  test("a provider error message denies rather than parsing as a severity", async () => {
    const registry = new FakeRegistry(providerErrors(attemptsPerModel, "404 model retired"));
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(verdict.reason).toContain("404 model retired");
  });

  test("an unreadable harm answer escalates rather than deciding", async () => {
    const registry = new FakeRegistry([says("I'd rather not say."), graded(10)]);
    expect(await classifierWith(registry).classify(request())).toMatchObject({ verdict: "allow", layer: "intent" });
    expect(registry.calls).toHaveLength(2);
  });

  test("an unreadable intent answer denies, naming what came back", async () => {
    const registry = new FakeRegistry([dangerous(), says("I'd rather not say.")]);
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", layer: "intent", unreadable: true });
    expect(verdict.reason).toContain("cannot read as a severity");
    expect(verdict.reason).toContain("I'd rather not say.");
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
    const registry = new FakeRegistry([dangerous(), graded(90, "Production Writes")]);
    const verdict = await classifierWith(registry).classify(request());
    const sent = registry.calls[1]?.context;
    expect(verdict).toMatchObject({
      verdict: "deny",
      prompt: { layer: "intent", system: sent?.systemPrompt, user: messagesOf(registry.calls[1]).join("\n\n") },
    });
  });

  test("an outage keeps the prompt nobody read — it is what tells it from a bad window", async () => {
    const registry = new FakeRegistry(errors(2 * attemptsPerModel, "503 upstream"));
    const verdict = await classifierWith(registry).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", unavailable: true, prompt: judgedOn("harm") });
  });

  test("an allow carries none: a call that ran needs no forensics", async () => {
    const registry = new FakeRegistry([routine()]);
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
    const registry = new FakeRegistry([...errors(attemptsPerModel, "503 upstream"), routine()]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toMatchObject({
      verdict: "allow",
      layer: "harm",
    });
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/primary", "vendor/fallback"]);
  });

  test("a provider error advances the walk the same way a throw does", async () => {
    const registry = new FakeRegistry([...providerErrors(attemptsPerModel, "503 Service Unavailable"), routine()]);
    await classifierWith(registry, twoDeep).classify(request());
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/primary", "vendor/fallback"]);
  });

  test("the retry is enough on its own: a blip never reaches the fallback", async () => {
    const registry = new FakeRegistry([new Error("connection reset"), routine()]);
    await classifierWith(registry, twoDeep).classify(request());
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/primary"]);
  });

  test("a severity is an answer, so the next model in the list is never asked", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry, twoDeep).classify(request());
    expect(specs(registry)).toEqual(["vendor/primary"]);
  });

  test("output that reads as no severity is still an answer, and ends the walk", async () => {
    const registry = new FakeRegistry([says("I'd rather not say."), says("nor will I.")]);
    const verdict = await classifierWith(registry, twoDeep).classify(request());
    expect(verdict.verdict).toBe("deny");
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/big"]);
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

  test("an exhausted list blocks as an outage, naming the stage, the models and the last failure", async () => {
    const registry = new FakeRegistry([
      ...errors(attemptsPerModel, "503 upstream"),
      ...providerErrors(attemptsPerModel, "502 bad gateway"),
    ]);
    const verdict = await classifierWith(registry, twoDeep).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", layer: "harm", unavailable: true });
    expect(verdict.reason).toContain("harm model");
    expect(verdict.reason).toContain("vendor/primary, vendor/fallback");
    expect(verdict.reason).toContain("502 bad gateway");
    expect(specs(registry)).toHaveLength(2 * attemptsPerModel);
  });

  test("the intent stage walks its own list", async () => {
    const registry = new FakeRegistry([dangerous(), ...errors(attemptsPerModel, "503 upstream"), graded(10)]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toMatchObject({
      verdict: "allow",
      layer: "intent",
    });
    expect(specs(registry)).toEqual(["vendor/primary", "vendor/big", "vendor/big", "vendor/bigger"]);
  });

  test("an exhausted intent list blocks as an intent-stage outage", async () => {
    const registry = new FakeRegistry([dangerous(), ...errors(2 * attemptsPerModel, "503 upstream")]);
    const verdict = await classifierWith(registry, twoDeep).classify(request());
    expect(verdict).toMatchObject({ verdict: "deny", layer: "intent", unavailable: true });
    expect(verdict.reason).toContain("intent model");
    expect(verdict.reason).toContain("vendor/big, vendor/bigger");
  });

  test("a model missing from the catalog is walked past, not judged", async () => {
    const registry = new FakeRegistry([routine()], ["vendor/primary"]);
    expect(await classifierWith(registry, twoDeep).classify(request())).toMatchObject({
      verdict: "allow",
      layer: "harm",
    });
    expect(specs(registry)).toEqual(["vendor/fallback"]);
  });
});

describe("usage accounting", () => {
  test("every stage's usage is reported", async () => {
    const reported: UsageLike[] = [];
    const registry = new FakeRegistry([
      graded(60, undefined, { input: 900, output: 20, cost: { total: 0.0004 } }),
      graded(80, "Production Writes", { input: 950, output: 25, cost: { total: 0.0019 } }),
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
  test("is projected oldest first, one object per turn", async () => {
    const transcript: TranscriptEntry[] = [
      { role: "user", text: "don't push until I review the diff" },
      { role: "assistant", text: "understood, I'll hold off." },
      { role: "tool", text: "git status", tool: "bash" },
    ];
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request({ transcript }));
    const user = messagesOf(registry.calls[0])[0] ?? "";
    expect(user).toContain('{"user":"don\'t push until I review the diff"}');
    expect(user).toContain('{"bash":"git status"}');
    expect(user.indexOf('{"user":"don\'t push')).toBeLessThan(user.indexOf('{"assistant":"understood'));
  });

  test("carries no tool results, only what was said and what was run", async () => {
    const registry = new FakeRegistry([routine()]);
    await classifierWith(registry).classify(request());
    expect(messagesOf(registry.calls[0])[0]).not.toContain("tool_result");
  });
});

describe("a conversation the model will not take", () => {
  const refusal = "prompt is too long: 412000 tokens > 200000 maximum";

  test("is told apart from an outage, whichever way the provider reports it", async () => {
    for (const answers of [errors(attemptsPerModel, refusal), providerErrors(attemptsPerModel, refusal)]) {
      const verdict = await classifierWith(new FakeRegistry(answers)).classify(request());
      expect(verdict.verdict).toBe("deny");
      if (verdict.verdict !== "deny") return;
      expect(verdict.tooLong).toBe(true);
      expect(verdict.unavailable).toBeUndefined();
      expect(verdict.reason).toContain("refused this conversation for its size");
    }
  });

  test("stays an outage when any model failed for another reason", async () => {
    const config: AutoModeConfig = { ...defaultAutoModeConfig, classifierModels: ["p/a", "p/b"] };
    const registry = new FakeRegistry([...errors(attemptsPerModel, refusal), ...errors(attemptsPerModel, "ECONNRESET")]);
    const verdict = await classifierWith(registry, config).classify(request());
    if (verdict.verdict !== "deny") throw new Error("expected a deny");
    expect(verdict.unavailable).toBe(true);
    expect(verdict.tooLong).toBeUndefined();
  });

  test("is still walked across the model list, in case a bigger window takes it", async () => {
    const config: AutoModeConfig = { ...defaultAutoModeConfig, classifierModels: ["p/small", "p/large"] };
    const registry = new FakeRegistry([...errors(attemptsPerModel, refusal), routine()]);
    const verdict = await classifierWith(registry, config).classify(request());
    expect(verdict.verdict).toBe("allow");
    expect(registry.calls.map((call) => call.model.id)).toEqual(["small", "small", "large"]);
  });
});
