import { getSupportedThinkingLevels, type Api, type Model } from "@earendil-works/pi-ai/compat";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { homedir } from "node:os";

export type EnvironmentLike = Record<string, string | undefined>;

export type CatalogModel = {
  id: string;
  name?: string;
  contextWindow?: number;
  effortSupport?: "supported" | "unsupported";
  effortLevels?: string[];
};

export type ProviderModels = {
  provider: string;
  ready: boolean;
  detail?: string;
  checkedAt?: number;
  models: CatalogModel[];
};

export type AvailableModels = {
  providers: ProviderModels[];
  problem?: string;
};

export type ModelQuery = (executable: string, env: EnvironmentLike) => Promise<AvailableModels>;

// The daemon allows 20 seconds for this request; reserve time to return a failure.
const discoveryTimeoutMs = 15_000;

export function availableModels(
  executable: string,
  env: EnvironmentLike,
  options: { cwd?: string; timeoutMs?: number } = {},
): Promise<AvailableModels> {
  return new Promise((resolve, reject) => {
    const id = randomUUID();
    const child = spawn(executable, [
      "--mode", "rpc", "--offline", "--no-session", "--no-tools",
      "--no-skills", "--no-prompt-templates", "--no-context-files",
    ], {
      cwd: options.cwd ?? homedir(),
      env,
      // Extensions may log credentials; neither stderr nor unrelated stdout reaches the app.
      stdio: ["pipe", "pipe", "ignore"],
    });
    let pending = "";
    let result: AvailableModels | undefined;
    let failure: Error | undefined;
    const fail = (error: Error) => {
      failure ??= error;
      child.kill("SIGKILL");
    };
    const timer = setTimeout(() => {
      fail(new Error("Pi model discovery timed out. Check that Pi can start and load its extensions."));
    }, options.timeoutMs ?? discoveryTimeoutMs);

    child.on("error", (error) => { failure ??= error; });
    child.stdin.on("error", () => fail(new Error("Pi closed its model discovery input")));
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      if (result || failure) return;
      pending += chunk;
      let end: number;
      while ((end = pending.indexOf("\n")) >= 0) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 1);
        let response: unknown;
        try { response = JSON.parse(line); } catch { continue; }
        if (!isRecord(response) || response.type !== "response" || response.id !== id) continue;
        if (response.command !== "get_available_models" || response.success !== true) {
          fail(new Error("Pi could not list its models. Check its provider configuration and extensions."));
          return;
        }
        try {
          result = catalogFromModels(isRecord(response.data) ? response.data.models : undefined);
        } catch {
          fail(new Error("Pi returned an invalid model catalog"));
          return;
        }
        // EOF lets Pi dispose its runtime and extension resources without creating a session.
        child.stdin.end();
        return;
      }
    });
    child.on("close", (code, signal) => {
      clearTimeout(timer);
      if (failure) reject(failure);
      else if (!result || code !== 0) {
        reject(new Error(`Pi exited ${signal ?? code} before completing model discovery`));
      } else resolve(result);
    });
    child.stdin.write(`${JSON.stringify({ id, type: "get_available_models" })}\n`);
  });
}

export function catalogFromModels(value: unknown): AvailableModels {
  if (!Array.isArray(value)) throw new Error("Expected Pi's model list");
  const providers = new Map<string, Map<string, CatalogModel>>();
  for (const model of value) {
    if (!isRecord(model) || typeof model.provider !== "string" || !model.provider.trim()
      || typeof model.id !== "string" || !model.id.trim()) {
      throw new Error("Invalid Pi model");
    }
    let models = providers.get(model.provider);
    if (!models) providers.set(model.provider, models = new Map());
    // RPC models can carry resolved headers. Only display metadata crosses the plugin boundary.
    models.set(model.id, {
      id: model.id,
      ...(typeof model.reasoning === "boolean" && typeof model.api === "string"
        ? { effortSupport: model.reasoning ? "supported" as const : "unsupported" as const,
            effortLevels: model.reasoning ? getSupportedThinkingLevels(model as unknown as Model<Api>) : [] }
        : {}),
      ...(typeof model.name === "string" && model.name ? { name: model.name } : {}),
      ...(typeof model.contextWindow === "number" && model.contextWindow > 0
        ? { contextWindow: model.contextWindow } : {}),
    });
  }
  return {
    providers: [...providers].sort(([a], [b]) => a.localeCompare(b)).map(([provider, models]) => ({
      provider,
      ready: true,
      models: [...models.values()].sort((a, b) => a.id.localeCompare(b.id)),
    })),
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
