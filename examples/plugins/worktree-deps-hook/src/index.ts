import {
  AttnPluginClient,
  type WorktreeAfterCreateParams,
} from "@attn/plugin";
import { spawn } from "node:child_process";
import { stat } from "node:fs/promises";
import { join } from "node:path";

const socketPath = process.env.ATTN_SOCKET_PATH?.trim();
const pluginName = process.env.ATTN_PLUGIN_NAME?.trim() || "worktree-deps-hook";

if (!socketPath) {
  throw new Error("ATTN_SOCKET_PATH is required");
}

const client = new AttnPluginClient({
  socketPath,
  name: pluginName,
  version: "0.1.0",
});

client.handle<"worktree.after_create">("worktree.after_create", async (params) => {
  await bootstrapDependencies(params);
});

await client.connect();

async function pathExists(path: string): Promise<boolean> {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
  }
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

async function bootstrapDependencies(params: WorktreeAfterCreateParams): Promise<void> {
  await installDependenciesIfPresent(params.path);
}
