import { AttnPluginClient } from "@victorarias/attn-plugin";
import { spawn } from "node:child_process";
import { stat } from "node:fs/promises";
import { join } from "node:path";

const client = new AttnPluginClient({
  version: "0.1.0",
});

client.handle<"worktree.after_create">("worktree.after_create", async (params) => {
  await installDependenciesIfPresent(params.path);
});

await client.connect();

async function pathExists(path: string): Promise<boolean> {
  try {
    await stat(path);
    return true;
  } catch (error) {
    if (isMissingPathError(error)) {
      return false;
    }
    throw error;
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

function isMissingPathError(error: unknown): boolean {
  return error instanceof Error && "code" in error && error.code === "ENOENT";
}
