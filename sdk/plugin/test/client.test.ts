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
  test("connects, handshakes, and registers provider surfaces", async () => {
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

    await client.connect({ providerSurfaces: ["worktree.create"] });

    expect(server.requests.map((request) => request.method)).toEqual(["hello"]);
    expect(server.requests[0]?.params).toMatchObject({
      provider_surfaces: ["worktree.create"],
    });

    client.close();
    await server.close();
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
    client.on<WorktreeCreateParams, WorktreeCreateResult>("worktree.create", async (params) => {
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

  test("drops pending requests when send fails before writing", async () => {
    const client = new AttnPluginClient({
      socketPath: "/tmp/unused.sock",
      name: "sdk-provider",
      version: "0.1.0",
    });

    await expect(client.request("transport.probe", {})).rejects.toThrow(
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
