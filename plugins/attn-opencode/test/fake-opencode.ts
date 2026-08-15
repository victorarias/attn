type RequestRecord = {
  method: string;
  path: string;
  search?: string;
  body?: unknown;
  authorization?: string | null;
};

export class FakeOpenCode {
  readonly requests: RequestRecord[] = [];
  readonly statuses = new Map<string, string>();
  readonly sessions = new Map<string, unknown>();
  readonly pendingQuestions = new Map<string, string>();
  readonly pendingPermissions = new Map<string, string>();
  readonly messages = new Map<string, unknown[]>();
  toolIDs: unknown = ["bash", "read", "write"];
  readonly classifierReplies: string[] = [];
  readonly classifierPrompts: Array<{ sessionID: string; body: unknown }> = [];
  readonly deletedSessions: string[] = [];
  failClassifierPrompt = false;
  failDeleteSession = false;
  failPendingLists = false;
  pendingListBarrier?: Promise<void>;
  readonly hangingSessionReads = new Set<string>();
  readonly prompts: Array<{ sessionID: string; body: unknown }> = [];
  readonly tuiSubmissions: Array<{ sessionID: string; text: string }> = [];
  tuiAttached = true;
  // OpenCode's SSE stream can drop between the TUI taking a prompt and the
  // event that announces the session it opened.
  dropSessionCreatedEvent = false;
  tuiModel: unknown = { providerID: "spotify-glm", id: "zai-org/GLM-5.2-FP8", variant: "max" };
  // Every model the tests pin, with the variants OpenCode's catalog declares for
  // it. A model missing here is a model OpenCode does not have.
  variants = new Map<string, string[]>([
    ["spotify-glm/zai-org/GLM-5.2-FP8", ["low", "max"]],
    ["opencode-go/mimo-v2.5", []],
  ]);
  private composer = "";
  private controllers = new Set<ReadableStreamDefaultController<Uint8Array>>();
  private nextSession = 1;
  readonly server: ReturnType<typeof Bun.serve>;

  constructor(readonly password: string, readonly version = "1.17.18") {
    this.server = Bun.serve({
      port: 0,
      hostname: "127.0.0.1",
      fetch: async (request) => this.handle(request),
    });
  }

  get port(): number {
    return this.server.port;
  }

  get eventSubscriberCount(): number {
    return this.controllers.size;
  }

  close(): void {
    for (const controller of this.controllers) controller.close();
    this.controllers.clear();
    this.server.stop(true);
  }

  emit(type: string, properties: Record<string, unknown>): void {
    const frame = `event: ${type}\ndata: ${JSON.stringify({ type, properties })}\n\n`;
    for (const controller of this.controllers) controller.enqueue(new TextEncoder().encode(frame));
  }

  askQuestion(sessionID: string, requestID = "question-1", type = "question.asked"): void {
    this.pendingQuestions.set(requestID, sessionID);
    this.emit(type, { id: requestID, sessionID, questions: [] });
  }

  replyQuestion(sessionID: string, requestID = "question-1", type = "question.replied"): void {
    this.pendingQuestions.delete(requestID);
    this.emit(type, { sessionID, requestID, answers: [] });
  }

  askPermission(sessionID: string, requestID = "permission-1", type = "permission.asked"): void {
    this.pendingPermissions.set(requestID, sessionID);
    this.emit(type, { id: requestID, sessionID, permission: "bash", patterns: [], metadata: {}, always: [] });
  }

  replyPermission(sessionID: string, requestID = "permission-1", type = "permission.replied"): void {
    this.pendingPermissions.delete(requestID);
    this.emit(type, { sessionID, requestID, reply: "once" });
  }

  closeEvents(): void {
    for (const controller of this.controllers) controller.close();
    this.controllers.clear();
  }

  private async handle(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const body = request.method === "GET" || request.method === "DELETE" ? undefined : await request.json();
    this.requests.push({
      method: request.method,
      path: url.pathname,
      search: url.search || undefined,
      body,
      authorization: request.headers.get("authorization"),
    });
    if (this.password !== "*" && request.headers.get("authorization") !== basic(this.password)) {
      return new Response("unauthorized", { status: 401 });
    }
    if (url.pathname === "/global/health") return Response.json({ healthy: true, version: this.version });
    if (url.pathname === "/event") {
      let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
      const stream = new ReadableStream<Uint8Array>({
        start: (nextController) => {
          controller = nextController;
          this.controllers.add(nextController);
          nextController.enqueue(new TextEncoder().encode(": connected\n\n"));
        },
        cancel: () => {
          if (controller) this.controllers.delete(controller);
        },
      });
      return new Response(stream, { headers: { "content-type": "text/event-stream" } });
    }
    if (url.pathname === "/session" && request.method === "POST") {
      const id = `native-${this.nextSession++}`;
      this.sessions.set(id, { id, model: (body as { model: unknown }).model });
      return Response.json({ id });
    }
    if (url.pathname === "/config/providers") {
      const providers = new Map<string, Record<string, unknown>>();
      for (const [pin, variants] of this.variants) {
        const slash = pin.indexOf("/");
        const providerID = pin.slice(0, slash);
        const models = providers.get(providerID) ?? {};
        models[pin.slice(slash + 1)] = { id: pin.slice(slash + 1), variants: Object.fromEntries(variants.map((name) => [name, {}])) };
        providers.set(providerID, models);
      }
      return Response.json({
        providers: [...providers].map(([id, models]) => ({ id, models })),
      });
    }
    if (url.pathname === "/session" && request.method === "GET") {
      return Response.json([...this.sessions.values()]);
    }
    if (url.pathname === "/session/status") return Response.json(Object.fromEntries(this.statuses));
    if (url.pathname === "/question") {
      if (this.failPendingLists) return Response.json({ error: "question list failed" }, { status: 500 });
      const snapshot = [...this.pendingQuestions].map(([id, sessionID]) => ({ id, sessionID, questions: [] }));
      await this.pendingListBarrier;
      return Response.json(snapshot);
    }
    if (url.pathname === "/permission") {
      if (this.failPendingLists) return Response.json({ error: "permission list failed" }, { status: 500 });
      const snapshot = [...this.pendingPermissions].map(([id, sessionID]) => ({
        id,
        sessionID,
        permission: "bash",
        patterns: [],
        metadata: {},
        always: [],
      }));
      await this.pendingListBarrier;
      return Response.json(snapshot);
    }
    if (url.pathname === "/experimental/tool/ids") return Response.json(this.toolIDs);
    if (url.pathname.startsWith("/session/") && url.pathname.endsWith("/prompt_async")) {
      const sessionID = decodeURIComponent(url.pathname.slice("/session/".length, -"/prompt_async".length));
      const prompt = body as {
        parts?: unknown;
        model?: { providerID?: unknown; modelID?: unknown };
        variant?: unknown;
      };
      if (!Array.isArray(prompt.parts) ||
        typeof prompt.model?.providerID !== "string" ||
        typeof prompt.model?.modelID !== "string" ||
        typeof prompt.variant !== "string") {
        return Response.json({ error: "invalid prompt_async body" }, { status: 400 });
      }
      this.prompts.push({ sessionID, body });
      return new Response(null, { status: 204 });
    }
    if (url.pathname.startsWith("/session/") && url.pathname.endsWith("/message") && request.method === "GET") {
      const sessionID = decodeURIComponent(url.pathname.slice("/session/".length, -"/message".length));
      return Response.json(this.messages.get(sessionID) ?? []);
    }
    if (url.pathname.startsWith("/session/") && url.pathname.endsWith("/message") && request.method === "POST") {
      const sessionID = decodeURIComponent(url.pathname.slice("/session/".length, -"/message".length));
      const prompt = body as {
        model?: { providerID?: unknown; modelID?: unknown };
        variant?: unknown;
      };
      this.classifierPrompts.push({ sessionID, body });
      if (this.failClassifierPrompt) return Response.json({ error: "classifier failed" }, { status: 500 });
      return Response.json({
        info: {
          id: `classifier-reply-${this.classifierPrompts.length}`,
          role: "assistant",
          time: { completed: Date.now() },
          model: {
            providerID: prompt.model?.providerID,
            modelID: prompt.model?.modelID,
            variant: prompt.variant,
          },
        },
        parts: [{ type: "text", text: this.classifierReplies.shift() ?? '{"verdict":"DONE"}' }],
      });
    }
    if (url.pathname.startsWith("/session/") && request.method === "DELETE") {
      const sessionID = decodeURIComponent(url.pathname.slice("/session/".length));
      if (this.failDeleteSession) return Response.json({ error: "delete failed" }, { status: 500 });
      this.sessions.delete(sessionID);
      this.deletedSessions.push(sessionID);
      return Response.json(true);
    }
    if (url.pathname.startsWith("/session/") && request.method === "GET") {
      const sessionID = decodeURIComponent(url.pathname.slice("/session/".length));
      if (this.hangingSessionReads.has(sessionID)) return new Promise<Response>(() => {});
      const session = this.sessions.get(sessionID);
      return session ? Response.json(session) : new Response("missing", { status: 404 });
    }
    // Every `/tui/*` route answers `true` whether or not a TUI is attached to
    // the event bus, exactly like OpenCode: a control request published before
    // the TUI attaches is simply dropped.
    if (url.pathname === "/tui/clear-prompt") {
      if (this.tuiAttached) this.composer = "";
      return Response.json(true);
    }
    if (url.pathname === "/tui/append-prompt") {
      if (this.tuiAttached) this.composer += (body as { text?: string }).text ?? "";
      return Response.json(true);
    }
    if (url.pathname === "/tui/submit-prompt") {
      if (this.tuiAttached && this.composer !== "") {
        const id = `native-${this.nextSession++}`;
        this.sessions.set(id, { id, model: this.tuiModel });
        this.messages.set(id, [{ info: { id: `user-${id}`, role: "user" }, parts: [{ type: "text", text: this.composer }] }]);
        this.tuiSubmissions.push({ sessionID: id, text: this.composer });
        this.composer = "";
        if (!this.dropSessionCreatedEvent) this.emit("session.created", { sessionID: id });
      }
      return Response.json(true);
    }
    return new Response("not found", { status: 404 });
  }
}

export function basic(password: string): string {
  return `Basic ${Buffer.from(`opencode:${password}`).toString("base64")}`;
}

export async function eventually(predicate: () => boolean, message: string | (() => string)): Promise<void> {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await Bun.sleep(10);
  }
  throw new Error(`timed out: ${typeof message === "function" ? message() : message}`);
}
