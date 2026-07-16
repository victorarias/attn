import { createServer } from "node:net";
import { chmod, open, readFile, rename, rm } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { randomBytes } from "node:crypto";
import type { LaunchConfig } from "./types";

const maxPortAttempts = 3;
const sourceGuidancePluginRef = pathToFileURL(join(import.meta.dir, "guidance-plugin.ts")).href;

if (import.meta.main) {
  const configPath = process.argv[2];
  if (!configPath) throw new Error("attn-opencode launcher requires a run config path");
  process.exitCode = await launch(configPath);
}

export async function launch(path: string): Promise<number> {
  let config = await readConfig(path);
  const guidancePluginRef = process.env.ATTN_OPENCODE_GUIDANCE_PLUGIN_REF?.trim() || sourceGuidancePluginRef;
  const opencodeConfig = opencodeConfigForLaunch(process.env.OPENCODE_CONFIG_CONTENT, config.instruction_ref, guidancePluginRef);
  for (let attempt = 1; attempt <= maxPortAttempts; attempt += 1) {
    const password = (await readFile(config.password_ref, "utf8")).trim();
    const args = [config.executable, "--hostname", "127.0.0.1", "--port", String(config.port)];
    if (config.resume_session_id) args.push("--session", config.resume_session_id);
    if (config.yolo) args.push("--yolo");
    const child = Bun.spawn(args, {
      cwd: config.cwd,
      env: {
        ...process.env,
        OPENCODE_SERVER_PASSWORD: password,
        ...(config.instruction_ref
          ? {
              ATTN_OPENCODE_INSTRUCTION_REF: config.instruction_ref,
              OPENCODE_CONFIG_CONTENT: JSON.stringify(opencodeConfig),
            }
          : {}),
      },
      stdin: "inherit",
      stdout: "inherit",
      stderr: "pipe",
    });
    const [display, collect] = child.stderr.tee();
    void display.pipeTo(new WritableStream({
      write(chunk) {
        process.stderr.write(chunk);
      },
    }));
    const stderr = await new Response(collect).text();
    const status = await child.exited;
    if (attempt === maxPortAttempts || !addressInUse(stderr)) return status;
    config = { ...config, port: await allocateLoopbackPort() };
    await writeConfig(path, config);
  }
  return 1;
}

export function opencodeConfigForLaunch(
  existingJSON: string | undefined,
  instructionRef?: string,
  pluginRef = sourceGuidancePluginRef,
): Record<string, unknown> {
  let parsed: unknown = {};
  if (existingJSON !== undefined && existingJSON.trim() !== "") {
    try {
      parsed = JSON.parse(existingJSON);
    } catch (error) {
      throw new Error(`invalid inherited OPENCODE_CONFIG_CONTENT: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("invalid inherited OPENCODE_CONFIG_CONTENT: expected a JSON object");
  }
  const base = parsed as Record<string, unknown>;
  if (!instructionRef) return base;
  const inheritedInstructions = base.instructions ?? [];
  if (!Array.isArray(inheritedInstructions) || !inheritedInstructions.every((value) => typeof value === "string")) {
    throw new Error("invalid inherited OPENCODE_CONFIG_CONTENT.instructions: expected an array of strings");
  }
  const inheritedPlugins = base.plugin ?? [];
  if (!Array.isArray(inheritedPlugins)) {
    throw new Error("invalid inherited OPENCODE_CONFIG_CONTENT.plugin: expected an array");
  }
  const hasGuidancePlugin = inheritedPlugins.some((value) =>
    value === pluginRef || (Array.isArray(value) && value[0] === pluginRef)
  );
  return {
    ...base,
    plugin: hasGuidancePlugin ? inheritedPlugins : [...inheritedPlugins, pluginRef],
  };
}

async function readConfig(path: string): Promise<LaunchConfig> {
  const parsed = JSON.parse(await readFile(path, "utf8")) as LaunchConfig;
  if (parsed.schema !== 1 || !parsed.executable || !parsed.cwd || !parsed.password_ref || !Number.isSafeInteger(parsed.port)) {
    throw new Error("invalid attn-opencode run config");
  }
  return parsed;
}

async function writeConfig(path: string, config: LaunchConfig): Promise<void> {
  const temp = join(dirname(path), `.${basename(path)}.${randomBytes(8).toString("hex")}.tmp`);
  const file = await open(temp, "w", 0o600);
  try {
    await file.writeFile(`${JSON.stringify(config)}\n`, "utf8");
    await file.sync();
  } finally {
    await file.close();
  }
  await chmod(temp, 0o600);
  await rename(temp, path);
  await chmod(path, 0o600);
  await rm(temp, { force: true });
}

async function allocateLoopbackPort(): Promise<number> {
  return new Promise<number>((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen({ host: "127.0.0.1", port: 0 }, () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close(() => reject(new Error("failed to reserve loopback port")));
        return;
      }
      server.close((error) => error ? reject(error) : resolve(address.port));
    });
  });
}

function addressInUse(stderr: string): boolean {
  return /EADDRINUSE|address already in use|address in use/i.test(stderr);
}
