import { createServer } from "node:net";
import { chmod, open, readFile, rename, rm } from "node:fs/promises";
import { homedir } from "node:os";
import { basename, dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { randomBytes } from "node:crypto";
import { parseModelPin } from "./opencode-http";
import { writePrivate } from "./run-registry";
import { type LaunchConfig, type LaunchPin, pinnedAgent } from "./types";

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
  const opencodeConfig = opencodeConfigForLaunch(process.env.OPENCODE_CONFIG_CONTENT, {
    instructionRef: config.instruction_ref,
    pluginRef: guidancePluginRef,
    pin: config.pin,
  });
  const stateHome = config.pin && config.state_dir ? await seedTUIState(config.state_dir, config.pin) : undefined;
  for (let attempt = 1; attempt <= maxPortAttempts; attempt += 1) {
    const password = (await readFile(config.password_ref, "utf8")).trim();
    const args = [config.executable, "--hostname", "127.0.0.1", "--port", String(config.port)];
    if (config.resume_session_id) args.push("--session", config.resume_session_id);
    if (config.pin) args.push("--agent", pinnedAgent);
    if (config.yolo) args.push("--yolo");
    const child = Bun.spawn(args, {
      cwd: config.cwd,
      env: {
        ...process.env,
        OPENCODE_SERVER_PASSWORD: password,
        ...(config.instruction_ref ? { ATTN_OPENCODE_INSTRUCTION_REF: config.instruction_ref } : {}),
        ...(config.instruction_ref || config.pin ? { OPENCODE_CONFIG_CONTENT: JSON.stringify(opencodeConfig) } : {}),
        ...(stateHome ? { XDG_STATE_HOME: stateHome } : {}),
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
  options: { instructionRef?: string; pluginRef?: string; pin?: LaunchPin } = {},
): Record<string, unknown> {
  const { instructionRef, pin, pluginRef = sourceGuidancePluginRef } = options;
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
  const withPin = pin ? { ...base, agent: agentWithPin(base.agent, pin) } : base;
  if (!instructionRef) return withPin;
  const inheritedInstructions = withPin.instructions ?? [];
  if (!Array.isArray(inheritedInstructions) || !inheritedInstructions.every((value) => typeof value === "string")) {
    throw new Error("invalid inherited OPENCODE_CONFIG_CONTENT.instructions: expected an array of strings");
  }
  const inheritedPlugins = withPin.plugin ?? [];
  if (!Array.isArray(inheritedPlugins)) {
    throw new Error("invalid inherited OPENCODE_CONFIG_CONTENT.plugin: expected an array");
  }
  const hasGuidancePlugin = inheritedPlugins.some((value) =>
    value === pluginRef || (Array.isArray(value) && value[0] === pluginRef)
  );
  return {
    ...withPin,
    plugin: hasGuidancePlugin ? inheritedPlugins : [...inheritedPlugins, pluginRef],
  };
}

// The variant a submitted prompt runs on comes from the TUI's own XDG state,
// not from the agent config, so a pinned launch gets a state directory of its
// own seeded with the pin — which also stops the run from rewriting the user's
// selection. Their display preferences are copied in so a delegated pane still
// looks like their OpenCode.
async function seedTUIState(stateDir: string, pin: LaunchPin): Promise<string> {
  const { providerID, id } = parseModelPin(pin.model);
  await writePrivate(join(stateDir, "opencode", "model.json"), JSON.stringify({
    recent: [{ providerID, modelID: id }],
    favorite: [],
    variant: { [pin.model]: pin.variant },
  }));
  const inherited = join(process.env.XDG_STATE_HOME?.trim() || join(homedir(), ".local", "state"), "opencode", "kv.json");
  try {
    await writePrivate(join(stateDir, "opencode", "kv.json"), await readFile(inherited, "utf8"));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }
  return stateDir;
}

// A delegated launch pins the model on the agent OpenCode runs the session
// with, so the TUI opens it on its first frame instead of the user's default,
// and `--agent` selects that agent whatever `default_agent` says. The variant
// is not settable here — the TUI takes it from its own state, which
// `seedTUIState` writes.
function agentWithPin(inherited: unknown, pin: LaunchPin): Record<string, unknown> {
  if (inherited !== undefined && (typeof inherited !== "object" || inherited === null || Array.isArray(inherited))) {
    throw new Error("invalid inherited OPENCODE_CONFIG_CONTENT.agent: expected an object");
  }
  const agents = (inherited ?? {}) as Record<string, unknown>;
  const existing = agents[pinnedAgent];
  if (existing !== undefined && (typeof existing !== "object" || existing === null || Array.isArray(existing))) {
    throw new Error(`invalid inherited OPENCODE_CONFIG_CONTENT.agent.${pinnedAgent}: expected an object`);
  }
  return {
    ...agents,
    [pinnedAgent]: { ...(existing as Record<string, unknown> | undefined), model: pin.model },
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
