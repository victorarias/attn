type RequestRecord = {
  method: string;
  path: string;
  body?: unknown;
  authorization?: string | null;
};

export class FakeOpenCode {
  readonly requests: RequestRecord[] = [];
  readonly statuses = new Map<string, string>();
  readonly sessions = new Map<string, unknown>();
  readonly hangingSessionReads = new Set<string>();
  readonly prompts: Array<{ sessionID: string; body: unknown }> = [];
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

  closeEvents(): void {
    for (const controller of this.controllers) controller.close();
    this.controllers.clear();
  }

  private async handle(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const body = request.method === "GET" ? undefined : await request.json();
    this.requests.push({
      method: request.method,
      path: url.pathname,
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
    if (url.pathname === "/session/status") return Response.json(Object.fromEntries(this.statuses));
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
    if (url.pathname.startsWith("/session/") && request.method === "GET") {
      const sessionID = decodeURIComponent(url.pathname.slice("/session/".length));
      if (this.hangingSessionReads.has(sessionID)) return new Promise<Response>(() => {});
      const session = this.sessions.get(sessionID);
      return session ? Response.json(session) : new Response("missing", { status: 404 });
    }
    if (url.pathname === "/tui/select-session") return Response.json(true);
    return new Response("not found", { status: 404 });
  }
}

export function basic(password: string): string {
  return `Basic ${Buffer.from(`opencode:${password}`).toString("base64")}`;
}

export async function eventually(predicate: () => boolean, message: string | (() => string)): Promise<void> {
  const deadline = Date.now() + 2_000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await Bun.sleep(10);
  }
  throw new Error(`timed out: ${typeof message === "function" ? message() : message}`);
}
