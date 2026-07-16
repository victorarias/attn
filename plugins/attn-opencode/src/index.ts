import { AttnRPCClient } from "./attn-rpc";
import { OpenCodeDriver } from "./driver";
import { launch } from "./launcher";
import { RunRegistry, runtimeRootFromSocket } from "./run-registry";
import type { DriverSpawnParams, SessionClosedParams } from "./types";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const pluginVersion = "0.1.0";
const launcherMarker = "--attn-opencode-launcher";
const launcherIndex = process.argv.indexOf(launcherMarker);

if (launcherIndex >= 0) {
  const configPath = process.argv[launcherIndex + 1];
  if (!configPath) throw new Error(`${launcherMarker} requires a run config path`);
  process.exitCode = await launch(configPath);
} else {
  await runPlugin();
}

async function runPlugin(): Promise<void> {
  const socketPath = requiredEnvironment("ATTN_SOCKET_PATH");
  const pluginName = requiredEnvironment("ATTN_PLUGIN_NAME");
  const pluginGeneration = requiredGeneration();
  const standalone = requiredEnvironment("ATTN_PLUGIN_ENTRYPOINT_KIND") === "executable";
  const pluginRoot = requiredEnvironment("ATTN_PLUGIN_ROOT");
  const runtimeRoot = process.env.ATTN_PLUGIN_DATA_ROOT?.trim() || runtimeRootFromSocket(socketPath);
  const rpc = new AttnRPCClient({ socketPath, name: pluginName, version: pluginVersion, generation: pluginGeneration });
  const driver = new OpenCodeDriver({
    rpc,
    registry: new RunRegistry(runtimeRoot),
    standaloneLauncher: standalone,
    guidancePluginRef: standalone
      ? pathToFileURL(join(pluginRoot, "guidance-plugin.js")).href
      : undefined,
  });

  rpc.handle("attn.health", () => driver.health());
  rpc.handle("driver.spawn", (params) => driver.spawn(params as DriverSpawnParams));
  rpc.handle("driver.resume", (params) => driver.resume(params as DriverSpawnParams));
  rpc.handle("driver.session_closed", (params) => driver.sessionClosed(params as SessionClosedParams));

  await rpc.connect();
  await driver.initialize();
}

function requiredEnvironment(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function requiredGeneration(): number {
  const value = Number(requiredEnvironment("ATTN_PLUGIN_GENERATION"));
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error("ATTN_PLUGIN_GENERATION must be a positive integer");
  return value;
}
