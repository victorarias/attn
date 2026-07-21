import { join } from "node:path";
import { tmpdir } from "node:os";
import { AttnRPCClient } from "./attn-rpc";
import { PiDriver } from "./driver";
import { RelayServer, type RelayConnection } from "./relay";
import type { DriverSpawnParams, SessionClosedParams } from "./types";

const pluginVersion = "0.1.0";

await runPlugin();

async function runPlugin(): Promise<void> {
  const socketPath = requiredEnvironment("ATTN_SOCKET_PATH");
  const pluginName = requiredEnvironment("ATTN_PLUGIN_NAME");
  const pluginGeneration = requiredGeneration();
  const rpc = new AttnRPCClient({ socketPath, name: pluginName, version: pluginVersion, generation: pluginGeneration });

  // `driver` is assigned below; the relay's delegate closes over this
  // binding rather than an instance, since RelayServer and PiDriver each
  // need a reference to the other and neither call happens before both exist.
  let driver: PiDriver;
  const relay = new RelayServer({
    socketPath: relaySocketPath(),
    delegate: {
      suiteHello: (connection: RelayConnection, params: unknown) => driver.suiteHello(connection, params),
      suiteReportState: (params: unknown) => driver.suiteReportState(params),
      suiteReportStop: (params: unknown) => driver.suiteReportStop(params),
    },
  });
  driver = new PiDriver({ rpc, relay, suitePath: suitePath() });

  rpc.handle("attn.health", () => driver.health());
  rpc.handle("driver.spawn", (params) => driver.spawn(params as DriverSpawnParams));
  rpc.handle("driver.resume", (params) => driver.resume(params as DriverSpawnParams));
  rpc.handle("driver.session_closed", (params) => driver.sessionClosed(params as SessionClosedParams));
  rpc.handle("driver.deliver_message", (params) => driver.deliverMessage(params));

  await rpc.connect();
  await driver.initialize();
}

function suitePath(): string {
  const override = process.env.ATTN_PI_SUITE_PATH?.trim();
  if (override) return override;
  if (process.env.ATTN_PLUGIN_ENTRYPOINT_KIND?.trim() === "executable") {
    return join(requiredEnvironment("ATTN_PLUGIN_ROOT"), "suite.js");
  }
  return join(import.meta.dir, "..", "suite", "index.ts");
}

function relaySocketPath(): string {
  return process.env.ATTN_PI_RELAY_SOCKET?.trim() || join(tmpdir(), `attn-pi-relay-${process.pid}.sock`);
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
