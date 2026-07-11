import type { OpenCodeModel } from "./types";

export type ServerEvent = {
  type: string;
  sessionID?: string;
  status?: string;
};

export type NativeAttention = "question" | "permission";

export type EventSubscription = {
  ready: Promise<void>;
  done: Promise<void>;
  abort: () => void;
};

export type OpenCodeHTTPOptions = {
  port: number;
  password: string;
  fetch?: typeof fetch;
};

export class OpenCodeHTTP {
  private readonly origin: string;
  private readonly authorization: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: OpenCodeHTTPOptions) {
    this.origin = `http://127.0.0.1:${options.port}`;
    this.authorization = `Basic ${Buffer.from(`opencode:${options.password}`).toString("base64")}`;
    this.fetchImpl = options.fetch ?? fetch;
  }

  async health(signal?: AbortSignal): Promise<{ version: string }> {
    const body = await this.request<{ healthy?: boolean; version?: string }>("/global/health", { signal });
    if (!body.healthy || typeof body.version !== "string") throw new Error("OpenCode health response was invalid");
    return { version: body.version };
  }

  async createSession(model: OpenCodeModel, signal?: AbortSignal): Promise<string> {
    const body = await this.request<{ id?: string }>("/session", {
      method: "POST",
      body: { model },
      signal,
    });
    if (!body.id) throw new Error("OpenCode did not return a native session id");
    return body.id;
  }

  async getSession(sessionID: string, signal?: AbortSignal): Promise<unknown> {
    return this.request(`/session/${encodeURIComponent(sessionID)}`, { signal });
  }

  async selectSession(sessionID: string, signal?: AbortSignal): Promise<void> {
    await this.request("/tui/select-session", { method: "POST", body: { sessionID }, signal });
  }

  async promptAsync(sessionID: string, prompt: string, model: OpenCodeModel, signal?: AbortSignal): Promise<void> {
    await this.request(`/session/${encodeURIComponent(sessionID)}/prompt_async`, {
      method: "POST",
      // OpenCode uses different model shapes for native session creation and
      // prompt submission. Keep the translation at this HTTP boundary so the
      // rest of the driver continues to preserve one native-session identity.
      body: {
        parts: [{ type: "text", text: prompt }],
        model: { providerID: model.providerID, modelID: model.id },
        variant: model.variant,
      },
      signal,
    });
  }

  async statusFor(sessionID: string, signal?: AbortSignal): Promise<string | undefined> {
    const body = await this.request<unknown>("/session/status", { signal });
    return extractStatus(body, sessionID);
  }

  async pendingAttentionFor(sessionID: string, signal?: AbortSignal): Promise<NativeAttention | undefined> {
    const [questions, permissions] = await Promise.all([
      this.request<unknown>("/question", { signal }),
      this.request<unknown>("/permission", { signal }),
    ]);
    // A permission requires a constrained approval response, so prefer that
    // state if a buggy or transitioning server briefly returns both kinds.
    if (hasPendingRequest(permissions, sessionID)) return "permission";
    if (hasPendingRequest(questions, sessionID)) return "question";
    return undefined;
  }

  subscribe(onEvent: (event: ServerEvent) => Promise<void> | void, parentSignal?: AbortSignal): EventSubscription {
    const controller = new AbortController();
    const abortForParent = () => controller.abort(parentSignal?.reason);
    if (parentSignal?.aborted) {
      abortForParent();
    } else {
      parentSignal?.addEventListener("abort", abortForParent, { once: true });
    }
    let markReady: () => void = () => {};
    let markFailed: (error: Error) => void = () => {};
    const ready = new Promise<void>((resolve, reject) => {
      markReady = resolve;
      markFailed = reject;
    });
    const done = this.consumeEvents(controller.signal, onEvent, markReady).catch((error) => {
      markFailed(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }).finally(() => parentSignal?.removeEventListener("abort", abortForParent));
    // A startup failure rejects `ready` before the monitor has reached its
    // `await done`; mark the rejection handled here while still returning the
    // original promise for normal reconnect handling.
    void done.catch(() => undefined);
    return {
      ready,
      done,
      abort: () => {
        parentSignal?.removeEventListener("abort", abortForParent);
        controller.abort();
      },
    };
  }

  private async consumeEvents(
    signal: AbortSignal,
    onEvent: (event: ServerEvent) => Promise<void> | void,
    markReady: () => void,
  ): Promise<void> {
    const response = await this.fetchImpl(`${this.origin}/event`, {
      headers: this.headers(),
      signal,
    });
    if (!response.ok) throw new Error(`OpenCode event stream: HTTP ${response.status}`);
    if (!response.body) throw new Error("OpenCode event stream had no body");
    markReady();

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let eventName = "";
    let data: string[] = [];
    const flush = async () => {
      if (data.length === 0) return;
      const parsed = normalizeEvent(eventName, data.join("\n"));
      eventName = "";
      data = [];
      if (parsed) await onEvent(parsed);
    };

    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        for (;;) {
          const newline = buffer.indexOf("\n");
          if (newline < 0) break;
          const line = buffer.slice(0, newline).replace(/\r$/, "");
          buffer = buffer.slice(newline + 1);
          if (line === "") {
            await flush();
          } else if (line.startsWith("event:")) {
            eventName = line.slice("event:".length).trim();
          } else if (line.startsWith("data:")) {
            data.push(line.slice("data:".length).trimStart());
          }
        }
      }
      await flush();
    } finally {
      reader.releaseLock();
    }
  }

  private async request<T = unknown>(path: string, init: { method?: string; body?: unknown; signal?: AbortSignal } = {}): Promise<T> {
    const response = await this.fetchImpl(`${this.origin}${path}`, {
      method: init.method ?? "GET",
      headers: {
        ...this.headers(),
        ...(init.body === undefined ? {} : { "content-type": "application/json" }),
      },
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
      signal: init.signal,
    });
    if (!response.ok) throw new Error(`OpenCode ${path}: HTTP ${response.status}`);
    if (response.status === 204) return undefined as T;
    return (await response.json()) as T;
  }

  private headers(): HeadersInit {
    return { authorization: this.authorization };
  }
}

export function parseModelPin(value: string): Omit<OpenCodeModel, "variant"> {
  const slash = value.indexOf("/");
  if (slash <= 0 || slash === value.length - 1) {
    throw new Error("OpenCode model pin must use provider/model form");
  }
  return { providerID: value.slice(0, slash), id: value.slice(slash + 1) };
}

export function variantForEffort(value: string): string {
  const normalized = value.trim().toLowerCase();
  if (!normalized) throw new Error("OpenCode requires an explicit effort pin to select a variant");
  const variants: Record<string, string> = {
    low: "low",
    max: "max",
  };
  const variant = variants[normalized];
  if (!variant) throw new Error(`OpenCode effort ${JSON.stringify(value)} has no verified variant mapping`);
  return variant;
}

export function sessionModelMatches(value: unknown, model: OpenCodeModel): boolean {
  const selected = sessionModel(value);
  return selected?.providerID === model.providerID &&
    selected.id === model.id &&
    selected.variant === model.variant;
}

export function sessionModel(value: unknown): OpenCodeModel | undefined {
  if (!value || typeof value !== "object") return undefined;
  const session = value as Record<string, unknown>;
  const selected = asObject(session.model) ?? asObject(asObject(session.info)?.model);
  const providerID = asString(selected?.providerID);
  const id = asString(selected?.id) ?? asString(selected?.modelID);
  const variant = asString(selected?.variant);
  if (!providerID || !id || !variant) return undefined;
  return { providerID, id, variant };
}

function normalizeEvent(eventName: string, raw: string): ServerEvent | undefined {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return undefined;
  }
  const root = asObject(data);
  const properties = asObject(root?.properties) ?? root;
  const type = normalizeEventType(eventName || asString(root?.type) || asString(root?.event));
  if (!type) return undefined;
  const statusValue = properties?.status;
  const status = typeof statusValue === "string" ? statusValue : asString(asObject(statusValue)?.type);
  return { type, sessionID: sessionIDFrom(properties) ?? sessionIDFrom(root), status };
}

function normalizeEventType(type: string | undefined): string | undefined {
  return type?.replace(/^(question|permission)\.v2\./, "$1.");
}

function hasPendingRequest(body: unknown, sessionID: string): boolean {
  const root = asObject(body);
  const requests = Array.isArray(body)
    ? body
    : Array.isArray(root?.requests)
      ? root.requests
      : Array.isArray(root?.data)
        ? root.data
        : [];
  return requests.some((request) => sessionIDFrom(asObject(request)) === sessionID);
}

function extractStatus(body: unknown, sessionID: string): string | undefined {
  const root = asObject(body);
  const candidates: unknown[] = [
    root?.[sessionID],
    asObject(root?.sessions)?.[sessionID],
    Array.isArray(body) ? body.find((entry) => sessionIDFrom(asObject(entry)) === sessionID) : undefined,
    Array.isArray(root?.sessions) ? root.sessions.find((entry) => sessionIDFrom(asObject(entry)) === sessionID) : undefined,
  ];
  for (const candidate of candidates) {
    const object = asObject(candidate);
    const statusValue = object?.status ?? candidate;
    if (typeof statusValue === "string") return statusValue;
    const nested = asObject(statusValue);
    if (typeof nested?.type === "string") return nested.type;
  }
  return undefined;
}

function sessionIDFrom(value: Record<string, unknown> | undefined): string | undefined {
  if (!value) return undefined;
  return asString(value.sessionID) ?? asString(value.session_id) ?? asString(value.id) ?? asString(asObject(value.session)?.id) ?? asString(asObject(value.info)?.id);
}

function asObject(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}
