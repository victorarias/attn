import {
  AttnPluginClient,
  handled,
  providerError,
  type WorktreeCreateParams,
  type WorktreeCreateResult,
  type WorktreeDeleteParams,
  type WorktreeDeleteResult,
} from "@attn/plugin";
import { spawn } from "node:child_process";
import { stat } from "node:fs/promises";
import { join, resolve } from "node:path";

const socketPath = process.env.ATTN_SOCKET_PATH?.trim();
const pluginName = process.env.ATTN_PLUGIN_NAME?.trim() || "worktree-deps-provider";

if (!socketPath) {
  throw new Error("ATTN_SOCKET_PATH is required");
}

const client = new AttnPluginClient({
  socketPath,
  name: pluginName,
  version: "0.1.0",
});

client.on<WorktreeCreateParams, WorktreeCreateResult>("worktree.create", (params) => {
  return createWorktree(params);
});

client.on<WorktreeDeleteParams, WorktreeDeleteResult>("worktree.delete", (params) => {
  return deleteWorktree(params);
});

await client.connect({
  providerSurfaces: ["worktree.create", "worktree.delete"],
});

async function createWorktree(params: WorktreeCreateParams): Promise<WorktreeCreateResult> {
  const mainRepo = resolve(params.main_repo);
  const branch = params.branch.trim();
  if (!branch) {
    return providerError("example provider requires a branch");
  }

  const requestedPath = params.requested_path?.trim();
  const worktreePath = resolve(requestedPath || defaultWorktreePath(mainRepo, branch));
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
    try {
      await installDependenciesIfPresent(worktreePath);
    } catch (error) {
      await runAllowFailure("git", ["worktree", "remove", "--force", worktreePath], mainRepo);
      throw error;
    }
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
  const worktreePath = resolve(params.path);

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

function defaultWorktreePath(mainRepo: string, branch: string): string {
  return join(mainRepo, ".worktrees", branch.replaceAll("/", "-"));
}

async function runChecked(command: string, args: string[], cwd: string): Promise<void> {
  const result = await runAllowFailure(command, args, cwd);
  if (result.code !== 0) {
    const details = [result.stderr.trim(), result.stdout.trim()].filter(Boolean).join("\n");
    throw new Error(details || `${command} ${args.join(" ")} exited with ${result.code}`);
  }
}

async function runAllowFailure(
  command: string,
  args: string[],
  cwd: string,
): Promise<{ code: number; stdout: string; stderr: string }> {
  return new Promise((resolveResult) => {
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
}

async function installDependenciesIfPresent(worktreePath: string): Promise<void> {
  const managers = [
    { lockfile: "pnpm-lock.yaml", command: "pnpm", args: ["install"] },
    { lockfile: "yarn.lock", command: "yarn", args: ["install"] },
    { lockfile: "package-lock.json", command: "npm", args: ["install"] },
  ];
  for (const manager of managers) {
    if (!(await pathExists(join(worktreePath, manager.lockfile)))) {
      continue;
    }
    await runChecked(manager.command, manager.args, worktreePath);
    return;
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
