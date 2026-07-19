import { AttnRPCClient } from "./attn-rpc";
import { PiDriver } from "./driver";
import type { DriverSpawnParams, SessionClosedParams } from "./types";

const pluginVersion = "0.1.0";

await runPlugin();

async function runPlugin(): Promise<void> {
  const socketPath = requiredEnvironment("ATTN_SOCKET_PATH");
  const pluginName = requiredEnvironment("ATTN_PLUGIN_NAME");
  const pluginGeneration = requiredGeneration();
  const rpc = new AttnRPCClient({ socketPath, name: pluginName, version: pluginVersion, generation: pluginGeneration });
  const driver = new PiDriver({ rpc });

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
