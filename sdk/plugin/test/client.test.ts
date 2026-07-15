import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer, type Socket } from "node:net";

import {
  AttnPluginClient,
  decline,
  handled,
  providerError,
  type WorktreeAfterCreateParams,
  type WorktreeCreateParams,
  type WorktreeCreateResult,
} from "../src";

type JsonRpcMessage = {
  jsonrpc: "2.0";
  id: number;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: {
    code: number;
    message: string;
  };
};

const tempRoots: string[] = [];

afterEach(async () => {
  while (tempRoots.length > 0) {
    const root = tempRoots.pop();
    if (root) {
      await rm(root, { recursive: true, force: true });
    }
  }
});

describe("AttnPluginClient", () => {
  test("connects and declares registered surfaces in hello", async () => {
    const server = await startServer(async (socket, request) => {
      if (request.method === "hello") {
        socket.write(`${JSON.stringify(response(request.id, { ok: true }))}\n`);
      }
    });

    const client = new AttnPluginClient({
      socketPath: server.socketPath,
      name: "sdk-provider",
      version: "0.1.0",
    });
    client.handle<"worktree.create">("worktree.create", async () => {
      return decline();
    });
    client.handle<"worktree.after_create">("worktree.after_create", async () => {});

    await client.connect();

    expect(server.requests.map((request) => request.method)).toEqual(["hello"]);
    expect(server.requests[0]?.params).toMatchObject({
      attn_api_version: 4,
      generation: 1,
      surfaces: ["worktree.after_create", "worktree.create"],
    });

    client.close();
    await server.close();
  });

  test("resolves daemon-provided socket path and plugin name from env", async () => {
    const server = await startServer(async (socket, request) => {
      if (request.method === "hello") {
        socket.write(`${JSON.stringify(response(request.id, { ok: true }))}\n`);
      }
    });
    const previousSocketPath = process.env.ATTN_SOCKET_PATH;
    const previousPluginName = process.env.ATTN_PLUGIN_NAME;
    const previousGeneration = process.env.ATTN_PLUGIN_GENERATION;
    process.env.ATTN_SOCKET_PATH = server.socketPath;
    process.env.ATTN_PLUGIN_NAME = "sdk-env-provider";
    process.env.ATTN_PLUGIN_GENERATION = "9";

    let client: AttnPluginClient | undefined;
    try {
      client = new AttnPluginClient({
        version: "0.1.0",
      });

      await client.connect();

      expect(server.requests[0]?.params).toMatchObject({
        name: "sdk-env-provider",
        generation: 9,
      });
    } finally {
      client?.close();
      restoreEnv("ATTN_SOCKET_PATH", previousSocketPath);
      restoreEnv("ATTN_PLUGIN_NAME", previousPluginName);
      restoreEnv("ATTN_PLUGIN_GENERATION", previousGeneration);
      await server.close();
    }
  });

  test("rejects an invalid daemon-provided generation", () => {
    const previousGeneration = process.env.ATTN_PLUGIN_GENERATION;
    process.env.ATTN_PLUGIN_GENERATION = "stale";
    try {
      expect(() => new AttnPluginClient({
        socketPath: "/tmp/unused.sock",
        name: "sdk-provider",
        version: "0.1.0",
      })).toThrow("ATTN_PLUGIN_GENERATION must be a positive integer");
    } finally {
      restoreEnv("ATTN_PLUGIN_GENERATION", previousGeneration);
    }
  });

  test("routes daemon worktree requests to typed handlers", async () => {
    const server = await startServer(async (socket, request) => {
      if (request.method === "hello") {
        socket.write(`${JSON.stringify(response(request.id, { ok: true }))}\n`);
        socket.write(
          `${JSON.stringify({
            jsonrpc: "2.0",
            id: 99,
            method: "worktree.create",
            params: {
              main_repo: "/repo",
              branch: "feature/sdk",
              starting_from: "origin/main",
            },
          })}\n`,
        );
      }
    });

    const client = new AttnPluginClient({
      socketPath: server.socketPath,
      name: "sdk-provider",
      version: "0.1.0",
    });
    client.handle<"worktree.create">("worktree.create", async (params) => {
      return handled({
        path: `${params.main_repo}/.worktrees/feature-sdk`,
        branch: params.branch,
      });
    });

    await client.connect();
    await waitFor(() => server.responses.length === 1);

    expect(server.responses[0]).toEqual(
      response(99, {
        status: "handled",
        path: "/repo/.worktrees/feature-sdk",
        branch: "feature/sdk",
      }),
    );

    client.close();
    await server.close();
  });

  test("responds to daemon healthchecks without plugin code", async () => {
    const server = await startServer(async (socket, request) => {
      if (request.method === "hello") {
        socket.write(`${JSON.stringify(response(request.id, { ok: true }))}\n`);
        socket.write(
          `${JSON.stringify({
            jsonrpc: "2.0",
            id: 100,
            method: "attn.health",
            params: { now: "2026-05-22T00:00:00Z" },
          })}\n`,
        );
      }
    });

    const client = new AttnPluginClient({
      socketPath: server.socketPath,
      name: "sdk-health",
      version: "0.1.0",
    });

    await client.connect();
    await waitFor(() => server.responses.length === 1);

    expect(server.responses[0]).toEqual(response(100, { ok: true }));

    client.close();
    await server.close();
  });

  test("drops pending requests when send fails before writing", async () => {
    const client = new AttnPluginClient({
      socketPath: "/tmp/unused.sock",
      name: "sdk-provider",
      version: "0.1.0",
    });

    const unsafeClient = client as unknown as {
      request<TResult = unknown>(method: string, params?: unknown): Promise<TResult>;
    };
    await expect(unsafeClient.request("transport.probe", {})).rejects.toThrow(
      "attn plugin socket is not connected",
    );
    expect(pendingRequestCount(client)).toBe(0);
  });

  test("resets a rejected hello so connect can retry cleanly", async () => {
    let helloCount = 0;
    const server = await startServer(async (socket, request) => {
      if (request.method !== "hello") {
        return;
      }
      helloCount += 1;
      socket.write(
        `${JSON.stringify(response(request.id, { ok: helloCount > 1 }))}\n`,
      );
    });

    const client = new AttnPluginClient({
      socketPath: server.socketPath,
      name: "sdk-provider",
      version: "0.1.0",
    });

    await expect(client.connect()).rejects.toThrow("attn rejected plugin hello");
    await client.connect();

    expect(helloCount).toBe(2);
    client.close();
    await server.close();
  });

  test("routes hook surfaces without requiring a result payload", async () => {
    const seen: WorktreeAfterCreateParams[] = [];
    const server = await startServer(async (socket, request) => {
      if (request.method === "hello") {
        socket.write(`${JSON.stringify(response(request.id, { ok: true }))}\n`);
        socket.write(
          `${JSON.stringify({
            jsonrpc: "2.0",
            id: 100,
            method: "worktree.after_create",
            params: {
              main_repo: "/repo",
              path: "/repo/.worktrees/feature-sdk",
              branch: "feature/sdk",
            },
          })}\n`,
        );
      }
    });

    const client = new AttnPluginClient({
      socketPath: server.socketPath,
      name: "sdk-hooks",
      version: "0.1.0",
    });
    client.handle<"worktree.after_create">("worktree.after_create", async (params) => {
      seen.push(params);
    });

    await client.connect();
    await waitFor(() => server.responses.length === 1);

    expect(seen).toEqual([
      {
        main_repo: "/repo",
        path: "/repo/.worktrees/feature-sdk",
        branch: "feature/sdk",
      },
    ]);
    expect(server.responses[0]).toEqual({
      jsonrpc: "2.0",
      id: 100,
    });

    client.close();
    await server.close();
  });

});

describe("provider result helpers", () => {
  test("build the protocol result shapes", () => {
    expect(handled({ path: "/tmp/wt", branch: "feature/x" })).toEqual({
      status: "handled",
      path: "/tmp/wt",
      branch: "feature/x",
    });
    expect(handled()).toEqual({ status: "handled" });
    expect(decline()).toEqual({ status: "decline" });
    expect(providerError("nope")).toEqual({ status: "error", error: "nope" });
  });
});

async function startServer(
  onRequest: (socket: Socket, request: JsonRpcMessage) => Promise<void> | void,
): Promise<{
  socketPath: string;
  requests: JsonRpcMessage[];
  responses: JsonRpcMessage[];
  close: () => Promise<void>;
}> {
  const root = await mkdtemp(join(tmpdir(), "attn-plugin-sdk-"));
  tempRoots.push(root);
  const socketPath = join(root, "plugin.sock");
  const requests: JsonRpcMessage[] = [];
  const responses: JsonRpcMessage[] = [];

  const server = createServer((socket) => {
    let buffer = "";
    socket.setEncoding("utf8");
    socket.on("data", async (chunk) => {
      buffer += chunk;
      for (;;) {
        const newline = buffer.indexOf("\n");
        if (newline === -1) {
          return;
        }
        const line = buffer.slice(0, newline).trim();
        buffer = buffer.slice(newline + 1);
        if (line === "") {
          continue;
        }
        const message = JSON.parse(line) as JsonRpcMessage;
        if (message.method) {
          requests.push(message);
          await onRequest(socket, message);
          continue;
        }
        responses.push(message);
      }
    });
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, () => resolve());
  });

  return {
    socketPath,
    requests,
    responses,
    close: () =>
      new Promise<void>((resolve, reject) => {
        server.close((error) => {
          if (error) {
            reject(error);
            return;
          }
          resolve();
        });
      }),
  };
}

function response(id: number, result: unknown): JsonRpcMessage {
  return {
    jsonrpc: "2.0",
    id,
    result,
  };
}

async function waitFor(predicate: () => boolean): Promise<void> {
  const deadline = Date.now() + 2_000;
  while (Date.now() < deadline) {
    if (predicate()) {
      return;
    }
    await Bun.sleep(10);
  }
  throw new Error("timed out waiting for condition");
}

function pendingRequestCount(client: AttnPluginClient): number {
  return (client as unknown as { pending: Map<string, unknown> }).pending.size;
}

function restoreEnv(name: "ATTN_SOCKET_PATH" | "ATTN_PLUGIN_NAME", value: string | undefined): void {
  if (value === undefined) {
    delete process.env[name];
    return;
  }
  process.env[name] = value;
}
