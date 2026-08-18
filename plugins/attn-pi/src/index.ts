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
    // A runtime whose daemon connection closed can forward nothing: every
    // report a live pi session sends over the relay would be accepted here and
    // then dropped, which is indistinguishable from attn deciding the session
    // is idle. Exiting is also what lets the session's suite notice — its relay
    // socket closes, so it re-dials and finds the replacement runtime on the
    // stable path. Nothing of pi's dies with us: a pi process is a child of its
    // pty-worker, not of this one.
    onDaemonDisconnect: () => {
      console.error("attn-pi: attn closed the plugin connection; exiting so the next runtime owns the relay");
      process.exit(0);
    },
  });

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
      suiteReportDenial: (params: unknown) => driver.suiteReportDenial(params),
    },
  });
  driver = new PiDriver({ rpc, relay, suitePath: suitePath() });
  const hostDriver = new NisseDriver({ rpc, hostCommand: hostCommand() });

  // One plugin, two agents: `pi` in a PTY and `nisse` headless. attn names
  // which one it is launching in every spawn, so the routing is a lookup and
  // not a guess. Health is the pair's: either being broken is worth reporting.
  rpc.handle("attn.health", () => combinedHealth(driver.health(), hostDriver.health()));
  rpc.handle("driver.spawn", (params) => {
    const spawnParams = params as DriverSpawnParams;
    return spawnParams.agent === nisseAgentName ? hostDriver.spawn(spawnParams) : driver.spawn(spawnParams);
  });
  rpc.handle("driver.resume", (params) => driver.resume(params as DriverSpawnParams));
  rpc.handle("driver.session_closed", (params) => driver.sessionClosed(params as SessionClosedParams));
  rpc.handle("driver.deliver_message", (params) => driver.deliverMessage(params));

  await rpc.connect();
  await driver.initialize();
  // Independent of pi's CLI: the host runs pi's SDK in its own process, so a
  // machine without a `pi` on PATH still gets conversation sessions.
  await hostDriver.initialize();
}

// combinedHealth reports ok only when both agents can launch, and names the one
// that cannot when they disagree.
function combinedHealth(
  pi: { ok: boolean; message: string },
  host: { ok: boolean; message: string },
): { ok: boolean; message: string } {
  if (pi.ok && host.ok) return { ok: true, message: `${pi.message}; ${host.message}` };
  if (!pi.ok && !host.ok) return { ok: false, message: `${pi.message}; ${host.message}` };
  return pi.ok ? host : pi;
}

// hostCommand is how nisse is launched. Bundled, it is the compiled
// binary staged beside this plugin's own executable; from a checkout it is this
// process's bun running the host source.
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

// The relay socket lives in this plugin's own data directory, one path per
// profile, and deliberately not one per process: a pi session is spawned with
// this path baked into its environment for the life of the process, so a
// pid-scoped path meant that a runtime restart left every live session dialing
// a socket only the dead runtime ever had. `listen()` unlinks and rebinds, so
// the newest runtime owns it.
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
