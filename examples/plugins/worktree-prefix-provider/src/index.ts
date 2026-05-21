import {
  AttnPluginClient,
  decline,
  handled,
  providerError,
  type WorktreeCreateParams,
  type WorktreeCreateResult,
  type WorktreeDeleteParams,
  type WorktreeDeleteResult,
} from "@attn/plugin";
import { spawn } from "node:child_process";
import { stat } from "node:fs/promises";
import { join, relative, resolve } from "node:path";

const socketPath = process.env.ATTN_SOCKET_PATH?.trim();
const pluginName = process.env.ATTN_PLUGIN_NAME?.trim() || "worktree-prefix-provider";

if (!socketPath) {
  throw new Error("ATTN_SOCKET_PATH is required");
}

const client = new AttnPluginClient({
  socketPath,
  name: pluginName,
  version: "0.1.0",
  roles: ["provider"],
});

client.on<WorktreeCreateParams, WorktreeCreateResult>("worktree.create", (params) => {
  return createWorktree(params);
});

client.on<WorktreeDeleteParams, WorktreeDeleteResult>("worktree.delete", (params) => {
  return deleteWorktree(params);
});

await client.connect();
await client.registerProvider(["worktree.create", "worktree.delete"], 50);

async function createWorktree(params: WorktreeCreateParams): Promise<WorktreeCreateResult> {
  const mainRepo = resolve(params.main_repo);
  const branch = params.branch.trim();
  if (!branch) {
    return providerError("example provider requires a branch");
  }

  const providerRoot = join(mainRepo, ".attn-example-worktrees");
  const requestedPath = params.requested_path?.trim();
  if (!requestedPath) {
    return providerError("example provider requires requested_path");
  }
  const worktreePath = resolve(requestedPath);
  if (!pathWithin(providerRoot, worktreePath)) {
    return providerError(`worktree path must stay inside ${providerRoot}`);
  }
  if (await pathExists(worktreePath)) {
    return providerError(`worktree path already exists: ${worktreePath}`);
  }

  const startingFrom = params.starting_from?.trim();
  const args =
    startingFrom && startingFrom === branch
      ? ["worktree", "add", worktreePath, branch]
      : startingFrom
        ? ["worktree", "add", "-b", branch, worktreePath, startingFrom]
        : ["worktree", "add", "-b", branch, worktreePath];

  try {
    await runChecked("git", args, mainRepo);
    return handled({
      path: worktreePath,
      branch,
    });
  } catch (error) {
    return providerError(errorMessage(error));
  }
}

async function deleteWorktree(params: WorktreeDeleteParams): Promise<WorktreeDeleteResult> {
  const mainRepo = resolve(params.main_repo);
  const providerRoot = join(mainRepo, ".attn-example-worktrees");
  const worktreePath = resolve(params.path);
  if (!pathWithin(providerRoot, worktreePath)) {
    return providerError(`worktree path must stay inside ${providerRoot}`);
  }

  try {
    await runChecked("git", ["worktree", "remove", worktreePath], mainRepo);
    return handled();
  } catch (error) {
    return providerError(errorMessage(error));
  }
}

async function pathExists(path: string): Promise<boolean> {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
  }
}

function pathWithin(root: string, target: string): boolean {
  const rel = relative(resolve(root), resolve(target));
  return rel !== "" && rel !== ".." && !rel.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`);
}

async function runChecked(command: string, args: string[], cwd: string): Promise<void> {
  const result = await new Promise<{ code: number; stdout: string; stderr: string }>((resolveResult) => {
    const child = spawn(command, args, {
      cwd,
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", (error) => {
      resolveResult({ code: 127, stdout, stderr: error.message });
    });
    child.on("close", (code) => {
      resolveResult({ code: code ?? 1, stdout, stderr });
    });
  });

  if (result.code !== 0) {
    const details = [result.stderr.trim(), result.stdout.trim()].filter(Boolean).join("\n");
    throw new Error(details || `${command} ${args.join(" ")} exited with ${result.code}`);
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
