import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { AttnRPCClient } from "./attn-rpc";
import { PiDriver } from "./driver";
import { nisseAgentName, NisseDriver } from "./nisse-driver";
import { RelayServer, type RelayConnection } from "./relay";
import type { DriverSpawnParams, SessionClosedParams } from "./types";

const pluginVersion = "0.2.0";

await runPlugin();

async function runPlugin(): Promise<void> {
  const socketPath = requiredEnvironment("ATTN_SOCKET_PATH");
  const pluginName = requiredEnvironment("ATTN_PLUGIN_NAME");
  const pluginGeneration = requiredGeneration();
  const rpc = new AttnRPCClient({
    socketPath,
    name: pluginName,
    version: pluginVersion,
    generation: pluginGeneration,
    // A runtime whose daemon connection closed drops relay reports, reading as an idle
    // session. Exiting closes the relay socket so every suite re-dials the replacement.
    onDaemonDisconnect: () => {
      console.error("attn-pi: attn closed the plugin connection; exiting so the next runtime owns the relay");
      process.exit(0);
    },
  });

  let driver: PiDriver;
  const relay = new RelayServer({
    socketPath: relaySocketPath(),
    delegate: {
      suiteHello: (connection: RelayConnection, params: unknown) => driver.suiteHello(connection, params),
      suiteReportState: (params: unknown) => driver.suiteReportState(params),
      suiteReportStop: (params: unknown) => driver.suiteReportStop(params),
      suiteReportDenial: (params: unknown) => driver.suiteReportDenial(params),
      suiteReportInputTaken: (params: unknown) => driver.suiteReportInputTaken(params),
      suiteReportPullRequest: (params: unknown) => driver.suiteReportPullRequest(params),
    },
  });
  driver = new PiDriver({ rpc, relay, suitePath: suitePath() });
  const hostDriver = new NisseDriver({ rpc, hostCommand: hostCommand() });

  rpc.handle("attn.health", () => combinedHealth(driver.health(), hostDriver.health()));
  rpc.handle("driver.spawn", (params) => {
    const spawnParams = params as DriverSpawnParams;
    return spawnParams.agent === nisseAgentName ? hostDriver.spawn(spawnParams) : driver.spawn(spawnParams);
  });
  rpc.handle("driver.resume", (params) => driver.resume(params as DriverSpawnParams));
  rpc.handle("driver.session_closed", (params) => driver.sessionClosed(params as SessionClosedParams));
  rpc.handle("driver.deliver_message", (params) => driver.deliverMessage(params));
  rpc.handle("automode.models", () => driver.models());
  rpc.handle("driver.models", () => driver.delegationModels());

  await rpc.connect();
  await driver.initialize();
  await hostDriver.initialize();
}

function combinedHealth(
  pi: { ok: boolean; message: string },
  host: { ok: boolean; message: string },
): { ok: boolean; message: string } {
  if (pi.ok && host.ok) return { ok: true, message: `${pi.message}; ${host.message}` };
  if (!pi.ok && !host.ok) return { ok: false, message: `${pi.message}; ${host.message}` };
  return pi.ok ? host : pi;
}

function hostCommand(): string[] {
  const override = process.env.ATTN_NISSE_COMMAND?.trim();
  if (override) return override.split(" ").filter((part) => part !== "");
  if (process.env.ATTN_PLUGIN_ENTRYPOINT_KIND?.trim() === "executable") {
    return [join(requiredEnvironment("ATTN_PLUGIN_ROOT"), "bin", "attn-nisse")];
  }
  return [process.execPath, join(import.meta.dir, "..", "host", "index.ts")];
}

function suitePath(): string {
  const override = process.env.ATTN_PI_SUITE_PATH?.trim();
  if (override) return override;
  if (process.env.ATTN_PLUGIN_ENTRYPOINT_KIND?.trim() === "executable") {
    return join(requiredEnvironment("ATTN_PLUGIN_ROOT"), "suite.js");
  }
  return join(import.meta.dir, "..", "suite", "index.ts");
}

// One path per profile, not per process: a pi session carries this path in its
// environment for life, so a pid-scoped path leaves it dialing a dead socket.
function relaySocketPath(): string {
  const override = process.env.ATTN_PI_RELAY_SOCKET?.trim();
  if (override) return override;
  const dataRoot = requiredEnvironment("ATTN_PLUGIN_DATA_ROOT");
  mkdirSync(dataRoot, { recursive: true, mode: 0o700 });
  return join(dataRoot, "relay.sock");
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
