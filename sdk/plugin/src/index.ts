import { createConnection, type Socket } from "node:net";

export type PluginRole = "driver" | "observer" | "actor";

export type ProviderDecline = {
  status: "decline";
};

export type ProviderError = {
  status: "error";
  error: string;
};

export type ProviderHandled<T extends object = Record<string, never>> = {
  status: "handled";
} & T;

export type ProviderResult<T extends object = Record<string, never>> =
  | ProviderHandled<T>
  | ProviderDecline
  | ProviderError;

export type WorktreeCreateParams = {
  main_repo: string;
  branch: string;
  starting_from?: string;
  requested_path?: string | null;
};

export type WorktreeCreateHandled = {
  path: string;
  branch: string;
};

export type WorktreeCreateResult = ProviderResult<WorktreeCreateHandled>;

export type WorktreeDeleteParams = {
  main_repo: string;
  path: string;
  branch?: string;
};

export type WorktreeDeleteResult = ProviderResult;

export type AttnPluginClientOptions = {
  socketPath: string;
  name: string;
  version: string;
  roles?: PluginRole[];
  attnAPIVersion?: number;
};

export type ConnectOptions = {
  providerSurfaces?: string[];
};

export type PluginHandler<TParams = unknown, TResult = unknown> = (
  params: TParams,
) => Promise<TResult> | TResult;

type JsonRpcID = string | number;

type JsonRpcRequest = {
  jsonrpc: "2.0";
  id: JsonRpcID;
  method: string;
  params?: unknown;
};

type JsonRpcResponse = {
  jsonrpc: "2.0";
  id: JsonRpcID;
  result?: unknown;
  error?: {
    code: number;
    message: string;
  };
};

type JsonRpcMessage = JsonRpcRequest | JsonRpcResponse;

type PendingRequest = {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
};

type HelloResult = {
  ok: boolean;
};

type ProviderRegisterResult = {
  ok: boolean;
  surfaces: string[];
};

const pluginAPIVersion = 1;

export class AttnPluginClient {
  private socket?: Socket;
  private nextID = 1;
  private readBuffer = "";
  private readonly handlers = new Map<string, PluginHandler>();
  private readonly pending = new Map<string, PendingRequest>();

  constructor(private readonly options: AttnPluginClientOptions) {}

  async connect(options: ConnectOptions = {}): Promise<void> {
    if (this.socket) {
      return;
    }

    try {
      this.socket = await new Promise<Socket>((resolve, reject) => {
        const socket = createConnection({ path: this.options.socketPath });
        const onError = (error: Error) => reject(error);
        socket.once("error", onError);
        socket.once("connect", () => {
          socket.off("error", onError);
          resolve(socket);
        });
      });

      this.socket.setEncoding("utf8");
      this.socket.on("data", (chunk) => this.consume(chunk));
      this.socket.on("error", (error) => this.failPending(error));
      this.socket.on("close", () => {
        this.socket = undefined;
        this.failPending(new Error("attn plugin socket closed"));
      });

      const result = await this.request<HelloResult>("hello", {
        name: this.options.name,
        version: this.options.version,
        attn_api_version: this.options.attnAPIVersion ?? pluginAPIVersion,
        roles: this.options.roles ?? [],
      });
      if (!result.ok) {
        throw new Error("attn rejected plugin hello");
      }
      if (options.providerSurfaces && options.providerSurfaces.length > 0) {
        await this.registerProvider(options.providerSurfaces);
      }
    } catch (error) {
      this.resetSocket(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
  }

  close(): void {
    this.socket?.end();
    this.socket = undefined;
    this.failPending(new Error("attn plugin client closed"));
  }

  on<TParams = unknown, TResult = unknown>(
    method: string,
    handler: PluginHandler<TParams, TResult>,
  ): void {
    this.handlers.set(method, handler as PluginHandler);
  }

  async registerProvider(surfaces: string[]): Promise<string[]> {
    const result = await this.request<ProviderRegisterResult>("provider.register", {
      surfaces,
    });
    if (!result.ok) {
      throw new Error("attn rejected provider registration");
    }
    return result.surfaces;
  }

  async request<TResult = unknown>(method: string, params?: unknown): Promise<TResult> {
    const id = this.nextID++;
    const response = new Promise<TResult>((resolve, reject) => {
      this.pending.set(String(id), {
        resolve: (value) => resolve(value as TResult),
        reject,
      });
    });

    try {
      this.send({
        jsonrpc: "2.0",
        id,
        method,
        params,
      });
    } catch (error) {
      this.pending.delete(String(id));
      throw error;
    }

    return response;
  }

  private consume(chunk: string): void {
    this.readBuffer += chunk;
    for (;;) {
      const newline = this.readBuffer.indexOf("\n");
      if (newline === -1) {
        return;
      }
      const line = this.readBuffer.slice(0, newline).trim();
      this.readBuffer = this.readBuffer.slice(newline + 1);
      if (line === "") {
        continue;
      }
      this.route(JSON.parse(line) as JsonRpcMessage);
    }
  }

  private route(message: JsonRpcMessage): void {
    if ("method" in message) {
      void this.handleRequest(message);
      return;
    }

    const pending = this.pending.get(String(message.id));
    if (!pending) {
      return;
    }
    this.pending.delete(String(message.id));
    if (message.error) {
      pending.reject(new Error(message.error.message));
      return;
    }
    pending.resolve(message.result);
  }

  private async handleRequest(request: JsonRpcRequest): Promise<void> {
    const handler = this.handlers.get(request.method);
    if (!handler) {
      this.sendError(request.id, -32601, `unknown method ${request.method}`);
      return;
    }

    try {
      const result = await handler(request.params);
      this.send({
        jsonrpc: "2.0",
        id: request.id,
        result,
      });
    } catch (error) {
      this.sendError(request.id, -32603, error instanceof Error ? error.message : String(error));
    }
  }

  private send(message: JsonRpcMessage): void {
    if (!this.socket) {
      throw new Error("attn plugin socket is not connected");
    }
    this.socket.write(`${JSON.stringify(message)}\n`);
  }

  private sendError(id: JsonRpcID, code: number, message: string): void {
    this.send({
      jsonrpc: "2.0",
      id,
      error: { code, message },
    });
  }

  private failPending(error: Error): void {
    for (const pending of this.pending.values()) {
      pending.reject(error);
    }
    this.pending.clear();
  }

  private resetSocket(error: Error): void {
    const socket = this.socket;
    this.socket = undefined;
    this.failPending(error);
    socket?.destroy();
  }
}

export function handled<T extends object = Record<string, never>>(payload?: T): ProviderHandled<T> {
  return {
    status: "handled",
    ...(payload ?? ({} as T)),
  };
}

export function decline(): ProviderDecline {
  return { status: "decline" };
}

export function providerError(error: string): ProviderError {
  return {
    status: "error",
    error,
  };
}
