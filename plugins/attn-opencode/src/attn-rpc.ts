import { createConnection, type Socket } from "node:net";
import { pluginAPIVersion } from "./types";

type JSONRPCID = number | string;

type JSONRPCRequest = {
  jsonrpc: "2.0";
  id: JSONRPCID;
  method: string;
  params?: unknown;
};

type JSONRPCResponse = {
  jsonrpc: "2.0";
  id: JSONRPCID;
  result?: unknown;
  error?: { code: number; message: string };
};

type Pending = {
  resolve: (result: unknown) => void;
  reject: (error: Error) => void;
};

export type RPCHandler = (params: unknown) => Promise<unknown> | unknown;

export class AttnRPCClient {
  private socket?: Socket;
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, Pending>();
  private readonly handlers = new Map<string, RPCHandler>();

  constructor(
    private readonly options: {
      socketPath: string;
      name: string;
      version: string;
    },
  ) {}

  handle(method: string, handler: RPCHandler): void {
    if (this.socket) {
      throw new Error("register RPC handlers before connecting");
    }
    this.handlers.set(method, handler);
  }

  async connect(): Promise<void> {
    if (this.socket) return;
    const socket = await new Promise<Socket>((resolve, reject) => {
      const candidate = createConnection({ path: this.options.socketPath });
      candidate.once("error", reject);
      candidate.once("connect", () => {
        candidate.off("error", reject);
        resolve(candidate);
      });
    });
    socket.setEncoding("utf8");
    socket.on("data", (chunk) => this.consume(chunk));
    socket.on("error", (error) => this.failPending(error));
    socket.on("close", () => {
      this.socket = undefined;
      this.failPending(new Error("attn plugin socket closed"));
    });
    this.socket = socket;

    try {
      const result = await this.request<{ ok: boolean }>("hello", {
        name: this.options.name,
        version: this.options.version,
        attn_api_version: pluginAPIVersion,
      });
      if (!result.ok) throw new Error("attn rejected plugin hello");
    } catch (error) {
      this.close();
      throw error;
    }
  }

  close(): void {
    this.socket?.destroy();
    this.socket = undefined;
    this.failPending(new Error("attn plugin client closed"));
  }

  request<TResult = unknown>(method: string, params?: unknown): Promise<TResult> {
    const id = this.nextID++;
    const result = new Promise<TResult>((resolve, reject) => {
      this.pending.set(String(id), { resolve: (value) => resolve(value as TResult), reject });
    });
    try {
      this.send({ jsonrpc: "2.0", id, method, params });
    } catch (error) {
      this.pending.delete(String(id));
      throw error;
    }
    return result;
  }

  private consume(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n");
      if (end < 0) return;
      const line = this.buffer.slice(0, end).trim();
      this.buffer = this.buffer.slice(end + 1);
      if (line === "") continue;
      this.route(JSON.parse(line) as JSONRPCRequest | JSONRPCResponse);
    }
  }

  private route(message: JSONRPCRequest | JSONRPCResponse): void {
    if ("method" in message) {
      void this.respond(message);
      return;
    }
    const pending = this.pending.get(String(message.id));
    if (!pending) return;
    this.pending.delete(String(message.id));
    if (message.error) {
      pending.reject(new Error(message.error.message));
      return;
    }
    pending.resolve(message.result);
  }

  private async respond(request: JSONRPCRequest): Promise<void> {
    const handler = this.handlers.get(request.method);
    if (!handler) {
      this.sendError(request.id, -32601, `unknown method ${request.method}`);
      return;
    }
    try {
      this.send({ jsonrpc: "2.0", id: request.id, result: await handler(request.params) });
    } catch (error) {
      this.sendError(request.id, -32603, error instanceof Error ? error.message : String(error));
    }
  }

  private send(message: JSONRPCRequest | JSONRPCResponse): void {
    if (!this.socket) throw new Error("attn plugin socket is not connected");
    this.socket.write(`${JSON.stringify(message)}\n`);
  }

  private sendError(id: JSONRPCID, code: number, message: string): void {
    this.send({ jsonrpc: "2.0", id, error: { code, message } });
  }

  private failPending(error: Error): void {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }
}
