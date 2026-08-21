// The classifier behind classifier.ts's interface: one completion on the
// configured model (layer 2a), and a second on the escalation model when the
// first could not decide or asked for a review (layer 2b).
//
// Each layer names an ordered list of models, and the list is walked only when
// a model cannot be reached — a thrown request, `stopReason: "error"`, an
// endpoint that is down. A model that ANSWERS ends the walk, whatever it
// answered: a deny is a verdict, and asking the next model would be shopping
// for a different one. Output that does not read as a verdict is still an
// answer (parseVerdict turns it into a deny), so it does not advance either.
// Each entry gets one immediate retry before the walk moves on, which is what
// carries a session through a blip rather than an outage.
//
// It reaches the model through `registry.getProvider(...).streamSimple(...)`,
// which is pi's own simple path — it clamps a thinking level to what the model
// supports and fills in the per-API request options. The flatter
// `ModelRegistry.complete()` (pi 0.84.2) is the RAW path and does neither, so
// the model thinks unbounded: measured on glm-5.3 with the same prompt,
// 354 output tokens and 5.7 s against 60 tokens and 2.9 s here (2026-08-17).
// Request auth is assembled the way ModelRuntime.prepareRequest assembles it,
// from the same registry, because the runtime itself is not on the extension
// context.
//
// Duck-typed against pi's shapes (0.83.0, core/model-registry.ts) the same way
// index.ts is duck-typed against ExtensionAPI, so `bun test` covers the whole
// path with a fake registry and no network.
import type { Classifier, ClassifierLayer, ClassifierRequest, ClassifierVerdict } from "./classifier";
import type { AutoModeConfig } from "./config";
import {
  classifierSystemPrompt,
  classifierUserPrompt,
  escalationSystemPrompt,
  parseVerdict,
  type ParsedVerdict,
} from "./prompt";
import { describeCall } from "./policy";
import type { UsageLike } from "./usage";

/**
 * The floor. pi raises it to the model's own minimum — glm-5.3 refuses to stop
 * thinking at all and lands on "low" — so this asks for the cheapest verdict
 * each model can give rather than pinning a level no model has to honour.
 */
export const classifierThinkingLevel = "minimal";

/**
 * Attempts one model gets before the walk moves to the next: the call, and one
 * immediate retry. A retry costs one round trip and covers the blip that a
 * fallback list would otherwise answer by paying a slower model; a second one
 * would only make an outage take longer to admit to.
 */
export const attemptsPerModel = 2;

export type ModelLike = { provider: string; id: string; baseUrl?: string };

export type CompletionMessage = {
  role: "user";
  content: { type: "text"; text: string }[];
  timestamp: number;
};

export type CompletionContext = {
  systemPrompt: string;
  messages: CompletionMessage[];
};

export type CompletionOptions = {
  reasoning?: string;
  apiKey?: string;
  headers?: Record<string, string | null>;
  env?: Record<string, string>;
  signal?: AbortSignal;
};

export type CompletionResult = {
  content?: { type: string; text?: string }[];
  usage?: UsageLike;
  stopReason?: string;
  errorMessage?: string;
};

export type CompletionStream = { result(): Promise<CompletionResult> };

export type ProviderLike = {
  streamSimple(model: ModelLike, context: CompletionContext, options?: CompletionOptions): CompletionStream;
};

export type RequestAuthLike = {
  ok: boolean;
  apiKey?: string;
  headers?: Record<string, string | null>;
  env?: Record<string, string>;
  error?: string;
};

export type ProviderAuthLike = { auth?: { baseUrl?: string } };

/** The ModelRegistry methods a classification needs. */
export type ModelRegistryLike = {
  find(provider: string, modelId: string): ModelLike | undefined;
  getProvider(provider: string): ProviderLike | undefined;
  getApiKeyAndHeaders(model: ModelLike): Promise<RequestAuthLike>;
  getProviderAuth(provider: string): Promise<ProviderAuthLike | undefined>;
};

export type ModelClassifierOptions = {
  registry: ModelRegistryLike;
  config: AutoModeConfig;
  /** Every completion's usage, for the ledger that folds it into the session. */
  onUsage?: (usage: UsageLike) => void;
};

export class ModelClassifier implements Classifier {
  constructor(private readonly options: ModelClassifierOptions) {}

  async classify(request: ClassifierRequest): Promise<ClassifierVerdict> {
    const userPrompt = classifierUserPrompt({
      transcript: request.transcript ?? [],
      environment: request.environment,
      action: describeCall(request.call),
      reason: request.reason,
      cwd: request.cwd,
    });

    const firstAnswer = await this.judge({
      models: this.options.config.classifierModels,
      layer: "classifier",
      systemPrompt: classifierSystemPrompt(request.environment),
      userPrompt,
      signal: request.signal,
    });
    if (firstAnswer.answered === false) return unavailableVerdict(firstAnswer.reason, "2a");
    const first = firstAnswer.parsed;
    // 2b reviews what 2a could not decide, and what it allowed while calling
    // the call expensive to get wrong. A confident deny does not go: the user
    // overturns one by saying so, and a second opinion buys them nothing but
    // the wait.
    if (first.verdict === "deny" || (first.verdict === "allow" && !first.highStakes)) return narrow(first, "2a");

    const secondAnswer = await this.judge({
      models: this.options.config.escalationModels,
      layer: "escalation",
      systemPrompt: escalationSystemPrompt(request.environment, first),
      userPrompt,
      signal: request.signal,
    });
    if (secondAnswer.answered === false) return unavailableVerdict(secondAnswer.reason, "2b");
    const second = secondAnswer.parsed;
    // Uncertain survived both passes: nobody is going to decide this, and a
    // call auto mode cannot judge is refused.
    if (second.verdict === "uncertain") {
      return {
        verdict: "deny",
        layer: "2b",
        reason: second.reason === "" ? "neither classifier could judge this call" : second.reason,
      };
    }
    return narrow(second, "2b");
  }

  /**
   * One layer's answer: the first model in its list that answers at all, or
   * the report that none of them could be reached.
   */
  private async judge(input: {
    models: readonly string[];
    layer: LayerName;
    systemPrompt: string;
    userPrompt: string;
    signal?: AbortSignal;
  }): Promise<LayerAnswer> {
    let lastFailure = "no model was configured for this layer";
    for (const modelSpec of input.models) {
      for (let attempt = 0; attempt < attemptsPerModel; attempt += 1) {
        let result: CompletionResult;
        try {
          result = await this.complete({ ...input, modelSpec });
        } catch (error) {
          // An abort is the user taking their turn back, not a verdict. It
          // travels to index.ts, which blocks the call without charging the
          // breaker, and it ends the walk: the turn it belonged to is over.
          if (input.signal?.aborted) throw error;
          lastFailure = `${modelSpec}: ${message(error)}`;
          continue;
        }

        if (result.usage) this.options.onUsage?.(result.usage);

        if (result.stopReason === "aborted") throw new Error("classification aborted");
        if (result.stopReason === "error") {
          lastFailure = `${modelSpec}: ${result.errorMessage ?? "no reason given"}`;
          continue;
        }
        return { answered: true, parsed: parseVerdict(textOf(result)) };
      }
    }
    return { answered: false, reason: unavailableReason(input.layer, input.models, lastFailure) };
  }

  /** Everything ModelRuntime.prepareRequest does, from the extension's registry. */
  private async complete(input: {
    modelSpec: string;
    layer: LayerName;
    systemPrompt: string;
    userPrompt: string;
    signal?: AbortSignal;
  }): Promise<CompletionResult> {
    const registry = this.options.registry;
    const model = this.resolve(input.modelSpec);
    if (!model) {
      throw new Error(`${JSON.stringify(input.modelSpec)} is not in this session's model catalog`);
    }
    const provider = registry.getProvider(model.provider);
    if (!provider) throw new Error(`provider ${JSON.stringify(model.provider)} is not configured`);

    const auth = await registry.getApiKeyAndHeaders(model);
    if (!auth.ok) throw new Error(auth.error ?? `no credential for ${model.provider}`);
    const baseUrl = (await registry.getProviderAuth(model.provider))?.auth?.baseUrl;

    return provider
      .streamSimple(
        baseUrl ? { ...model, baseUrl } : model,
        {
          systemPrompt: input.systemPrompt,
          messages: [{ role: "user", content: [{ type: "text", text: input.userPrompt }], timestamp: Date.now() }],
        },
        {
          reasoning: classifierThinkingLevel,
          apiKey: auth.apiKey,
          headers: auth.headers,
          env: auth.env,
          signal: input.signal,
        },
      )
      .result();
  }

  private resolve(spec: string): ModelLike | undefined {
    const separator = spec.indexOf("/");
    if (separator <= 0 || separator === spec.length - 1) return undefined;
    return this.options.registry.find(spec.slice(0, separator), spec.slice(separator + 1));
  }
}

type LayerName = "classifier" | "escalation";

/** What one layer produced: a model's answer, or nobody's. */
type LayerAnswer = { answered: true; parsed: ParsedVerdict } | { answered: false; reason: string };

/**
 * The block a human reads when no model could be reached. It says which layer,
 * what was tried and what the last endpoint said, because the difference
 * between "a model refused this" and "nothing looked at this" decides whether
 * the user argues with the verdict or fixes the outage.
 */
function unavailableReason(layer: LayerName, models: readonly string[], lastFailure: string): string {
  const tried = models.length > 0 ? models.join(", ") : "(no model configured)";
  return (
    `auto mode could not reach its ${layer} model (layer ${layer === "classifier" ? "2a" : "2b"}): ` +
    `tried ${tried}, ${attemptsPerModel} attempts each; last failure: ${lastFailure}. ` +
    `No model judged this call — auto mode fails closed when its classifier is unreachable, so this ` +
    `is an outage and not a refusal. Say so to the user rather than retrying the call.`
  );
}

function unavailableVerdict(reason: string, layer: ClassifierLayer): ClassifierVerdict {
  return { verdict: "deny", layer, reason, unavailable: true };
}

function narrow(parsed: ParsedVerdict, layer: ClassifierLayer): ClassifierVerdict {
  if (parsed.verdict === "allow") return { verdict: "allow", layer, reason: parsed.reason };
  if (parsed.verdict === "deny") {
    return {
      verdict: "deny",
      layer,
      reason: parsed.reason === "" ? "the classifier refused this call" : parsed.reason,
      ...(parsed.unreadable === true ? { unreadable: true } : {}),
    };
  }
  return { verdict: "uncertain", layer, reason: parsed.reason };
}

function textOf(result: CompletionResult): string {
  return (result.content ?? [])
    .filter((block) => block.type === "text" && typeof block.text === "string")
    .map((block) => block.text)
    .join("");
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
