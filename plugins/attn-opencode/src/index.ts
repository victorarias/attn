import { AttnRPCClient } from "./attn-rpc";
import { OpenCodeDriver } from "./driver";
import { RunRegistry, runtimeRootFromSocket } from "./run-registry";
import type { DriverSpawnParams, SessionClosedParams } from "./types";

const socketPath = requiredEnvironment("ATTN_SOCKET_PATH");
const pluginName = requiredEnvironment("ATTN_PLUGIN_NAME");
const rpc = new AttnRPCClient({ socketPath, name: pluginName, version: "0.1.0" });
const driver = new OpenCodeDriver({
  rpc,
  registry: new RunRegistry(runtimeRootFromSocket(socketPath)),
});

rpc.handle("attn.health", () => driver.health());
rpc.handle("driver.spawn", (params) => driver.spawn(params as DriverSpawnParams));
rpc.handle("driver.resume", (params) => driver.resume(params as DriverSpawnParams));
rpc.handle("driver.session_closed", (params) => driver.sessionClosed(params as SessionClosedParams));

await rpc.connect();
await driver.initialize();

function requiredEnvironment(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
