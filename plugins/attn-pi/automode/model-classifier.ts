import type { Classifier, ClassifierLayer, ClassifierPrompt, ClassifierRequest, ClassifierVerdict } from "./classifier";
import type { AutoModeConfig } from "./config";
import {
  blockLine,
  classifierUserPrompt,
  grantPrompt,
  hardBlockRule,
  harmSystemPrompt,
  intentSystemPrompt,
  parseSeverity,
  stageOneAllowCeiling,
  unreadableReason,
  type ParsedSeverity,
  type PromptInput,
} from "./prompt";
import { callSignature } from "./policy";
import type { UsageLike } from "./usage";

export const classifierThinkingLevel = "minimal";

export const harmMaxTokens = 512;

export const intentMaxTokens = 8_192;

export const classifierCacheRetention = "long";

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
  maxTokens?: number;
  cacheRetention?: string;
  sessionId?: string;
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

export type ModelRegistryLike = {
  find(provider: string, modelId: string): ModelLike | undefined;
  getProvider(provider: string): ProviderLike | undefined;
  getApiKeyAndHeaders(model: ModelLike): Promise<RequestAuthLike>;
  getProviderAuth(provider: string): Promise<ProviderAuthLike | undefined>;
};

export type ModelClassifierOptions = {
  registry: ModelRegistryLike;
  config: AutoModeConfig;

  sessionKey?: string;

  onUsage?: (usage: UsageLike) => void;
};

export class ModelClassifier implements Classifier {
  constructor(private readonly options: ModelClassifierOptions) {}

  async classify(request: ClassifierRequest): Promise<ClassifierVerdict> {
    const input: PromptInput = {
      transcript: request.transcript ?? [],
      environment: request.environment,
      action: callSignature(request.call),
      tool: request.call.toolName,
      reason: request.reason,
      cwd: request.cwd,
    };
    const grant = request.grant?.trim();
    const preamble = grant && grant !== "" ? [grantPrompt(grant)] : [];

    const harmMessages = [...preamble, classifierUserPrompt(input, "harm")];
    const harmPrompt: ClassifierPrompt = {
      layer: "harm",
      system: harmSystemPrompt(request.environment),
      user: harmMessages.join("\n\n"),
    };
    const harm = await this.judge({
      models: this.options.config.classifierModels,
      layer: "harm",
      systemPrompt: harmPrompt.system,
      messages: harmMessages,
      maxTokens: harmMaxTokens,
      ...(this.sessionId("harm") ?? {}),
      signal: request.signal,
    });
    if (harm.answered === false) return unansweredVerdict(harm, harmPrompt);
    if (harm.parsed && harm.parsed.severity <= stageOneAllowCeiling) {
      return { verdict: "allow", layer: "harm", severity: harm.parsed.severity };
    }

    const intentMessages = [...preamble, classifierUserPrompt(input, "intent")];
    const intentPrompt: ClassifierPrompt = {
      layer: "intent",
      system: intentSystemPrompt(request.environment),
      user: intentMessages.join("\n\n"),
    };
    const intent = await this.judge({
      models: this.options.config.escalationModels,
      layer: "intent",
      systemPrompt: intentPrompt.system,
      messages: intentMessages,
      maxTokens: intentMaxTokens,
      ...(this.sessionId("intent") ?? {}),
      signal: request.signal,
    });
    if (intent.answered === false) return unansweredVerdict(intent, intentPrompt);
    if (!intent.parsed) {
      return {
        verdict: "deny",
        layer: "intent",
        prompt: intentPrompt,
        reason: unreadableReason(intent.text),
        unreadable: true,
      };
    }
    return settle(intent.parsed, intentPrompt);
  }

  private sessionId(layer: ClassifierLayer): { sessionId: string } | undefined {
    const key = this.options.sessionKey?.trim();
    return key ? { sessionId: `${key}:${layer}` } : undefined;
  }

  private async judge(input: {
    models: readonly string[];
    layer: ClassifierLayer;
    systemPrompt: string;
    messages: readonly string[];
    maxTokens: number;
    sessionId?: string;
    signal?: AbortSignal;
  }): Promise<LayerAnswer> {
    let lastFailure = "no model was configured for this layer";
    let everyFailureTooLong = input.models.length > 0;
    for (const modelSpec of input.models) {
      for (let attempt = 0; attempt < attemptsPerModel; attempt += 1) {
        let result: CompletionResult;
        try {
          result = await this.complete({ ...input, modelSpec });
        } catch (error) {
          if (input.signal?.aborted) throw error;
          const failure = message(error);
          if (!promptIsTooLong(failure)) everyFailureTooLong = false;
          lastFailure = `${modelSpec}: ${failure}`;
          continue;
        }

        if (result.usage) this.options.onUsage?.(result.usage);

        if (result.stopReason === "aborted") throw new Error("classification aborted");
        if (result.stopReason === "error") {
          const failure = result.errorMessage ?? "no reason given";
          if (!promptIsTooLong(failure)) everyFailureTooLong = false;
          lastFailure = `${modelSpec}: ${failure}`;
          continue;
        }
        const text = textOf(result);
        return { answered: true, text, parsed: parseSeverity(text) };
      }
    }
    if (everyFailureTooLong) {
      return { answered: false, tooLong: true, reason: tooLongReason(input.layer, lastFailure) };
    }
    return { answered: false, reason: unavailableReason(input.layer, input.models, lastFailure) };
  }

  private async complete(input: {
    modelSpec: string;
    systemPrompt: string;
    messages: readonly string[];
    maxTokens: number;
    sessionId?: string;
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
    const timestamp = Date.now();

    return provider
      .streamSimple(
        baseUrl ? { ...model, baseUrl } : model,
        {
          systemPrompt: input.systemPrompt,
          messages: input.messages.map((text) => ({
            role: "user" as const,
            content: [{ type: "text" as const, text }],
            timestamp,
          })),
        },
        {
          reasoning: classifierThinkingLevel,
          maxTokens: input.maxTokens,
          cacheRetention: classifierCacheRetention,
          ...(input.sessionId ? { sessionId: input.sessionId } : {}),
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

type LayerAnswer =
  | { answered: true; text: string; parsed: ParsedSeverity | undefined }
  | { answered: false; reason: string; tooLong?: boolean };

function promptIsTooLong(failure: string): boolean {
  return /prompt is too long|context[_ ]length[_ ]exceeded|too many tokens|maximum context length/i.test(failure);
}

function tooLongReason(layer: ClassifierLayer, lastFailure: string): string {
  return (
    `auto mode's ${layer} model refused this conversation for its size: ${lastFailure}. ` +
    `Nothing judged the call and nothing refused the action - the classifier was never shown it.`
  );
}

function unavailableReason(layer: ClassifierLayer, models: readonly string[], lastFailure: string): string {
  const tried = models.length > 0 ? models.join(", ") : "(no model configured)";
  return (
    `auto mode could not reach its ${layer} model: ` +
    `tried ${tried}, ${attemptsPerModel} attempts each; last failure: ${lastFailure}. ` +
    `No model judged this call.`
  );
}

function unansweredVerdict(answer: { reason: string; tooLong?: boolean }, prompt: ClassifierPrompt): ClassifierVerdict {
  if (answer.tooLong === true) {
    return { verdict: "deny", layer: prompt.layer, prompt, reason: answer.reason, tooLong: true };
  }
  return { verdict: "deny", layer: prompt.layer, prompt, reason: answer.reason, unavailable: true };
}

function settle(parsed: ParsedSeverity, prompt: ClassifierPrompt): ClassifierVerdict {
  if (parsed.severity <= blockLine) {
    return { verdict: "allow", layer: prompt.layer, severity: parsed.severity };
  }
  const category = parsed.category;
  return {
    verdict: "deny",
    layer: prompt.layer,
    prompt,
    severity: parsed.severity,
    reason: denyReason(parsed),
    ...(category ? { category } : {}),
    ...(isHardBlock(category) ? { boundary: true } : {}),
  };
}

function isHardBlock(category: string | undefined): boolean {
  return category !== undefined && category.toLowerCase() === hardBlockRule.toLowerCase();
}

function denyReason(parsed: ParsedSeverity): string {
  const rule = parsed.category ? `the ${parsed.category} rule` : "auto mode's rules";
  const thinking = parsed.thinking?.replace(/\s+/g, " ").trim();
  const head = `the classifier placed this call at severity ${parsed.severity} under ${rule}`;
  return thinking && thinking !== "" ? `${head}: ${thinking}` : head;
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
