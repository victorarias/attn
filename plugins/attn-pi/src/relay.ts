import { existsSync, unlinkSync } from "node:fs";
import { createServer, type Server, type Socket } from "node:net";
import { relayMethods } from "./relay-protocol";

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

export type RelayDelegate = {
  suiteHello(connection: RelayConnection, params: unknown): Promise<{ ok: true }>;
  suiteReportState(params: unknown): Promise<void>;
  suiteReportStop(params: unknown): Promise<void>;
};

// One RelayConnection per suite that dials in. Mirrors attn-rpc.ts's
// consume/route framing, but a request here is driver -> suite (its own id
// space) while inbound requests are suite -> driver, dispatched to the
// delegate.
export class RelayConnection {
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, Pending>();
  private readonly closeHandlers: Array<() => void> = [];

  constructor(
    private readonly socket: Socket,
    private readonly delegate: RelayDelegate,
  ) {
    this.socket.setEncoding("utf8");
    this.socket.on("data", (chunk) => this.consume(chunk));
    this.socket.on("error", (error) => this.failPending(error));
    this.socket.on("close", () => {
      this.failPending(new Error("suite connection closed"));
      for (const handler of this.closeHandlers) handler();
    });
  }

  onClose(handler: () => void): void {
    this.closeHandlers.push(handler);
  }

  request<TResult = unknown>(method: string, params: unknown, timeoutMs: number): Promise<TResult> {
    const id = this.nextID++;
    const result = new Promise<TResult>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(String(id));
        reject(new Error(`suite did not respond to ${method} within ${timeoutMs}ms`));
      }, timeoutMs);
      this.pending.set(String(id), {
        resolve: (value) => {
          clearTimeout(timer);
          resolve(value as TResult);
        },
        reject: (error) => {
          clearTimeout(timer);
          reject(error);
        },
      });
    });
    try {
      this.send({ jsonrpc: "2.0", id, method, params });
    } catch (error) {
      this.pending.delete(String(id));
      throw error;
    }
    return result;
  }

  close(): void {
    this.socket.destroy();
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
    const handler = this.handlerFor(request.method);
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

  private handlerFor(method: string): ((params: unknown) => Promise<unknown>) | undefined {
    switch (method) {
      case relayMethods.hello:
        return (params) => this.delegate.suiteHello(this, params);
      case relayMethods.reportState:
        return async (params) => {
          await this.delegate.suiteReportState(params);
          return { ok: true };
        };
      case relayMethods.reportStop:
        return async (params) => {
          await this.delegate.suiteReportStop(params);
          return { ok: true };
        };
      default:
        return undefined;
    }
  }

  private send(message: JSONRPCRequest | JSONRPCResponse): void {
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

// Driver-owned unix socket that the pi-side suite dials into. One suite
// connection per live pi run; the driver binds a connection to a RunState
// once suite.hello arrives with a matching token.
export class RelayServer {
  readonly socketPath: string;
  private readonly delegate: RelayDelegate;
  private server?: Server;
  private readonly connections = new Set<RelayConnection>();

  constructor(options: { socketPath: string; delegate: RelayDelegate }) {
    this.socketPath = options.socketPath;
    this.delegate = options.delegate;
  }

  async listen(): Promise<void> {
    if (existsSync(this.socketPath)) unlinkSync(this.socketPath);
    const server = createServer((socket) => {
      const connection = new RelayConnection(socket, this.delegate);
      this.connections.add(connection);
      connection.onClose(() => this.connections.delete(connection));
    });
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(this.socketPath, () => {
        server.off("error", reject);
        resolve();
      });
    });
    this.server = server;
  }

  deliverMessage<TParams, TResult>(connection: RelayConnection, params: TParams, timeoutMs: number): Promise<TResult> {
    return connection.request<TResult>(relayMethods.deliverMessage, params, timeoutMs);
  }

  close(): void {
    for (const connection of this.connections) connection.close();
    this.connections.clear();
    this.server?.close();
    this.server = undefined;
    if (existsSync(this.socketPath)) unlinkSync(this.socketPath);
  }
}
