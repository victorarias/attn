// The pi headless host: one process, one attn session, one pi AgentSession.
//
// attn's daemon spawns this as a process-group leader and owns its lifetime.
// It runs pi through the SDK (`createAgentSession`) rather than a PTY, so the
// conversation is data the app draws rather than bytes a terminal paints.
//
// Three channels, deliberately separate:
//
//   fd 3    envelopes out (NDJSON). A dedicated fd, not stdout: pi discovers
//           and loads the user's own extensions, and any one of them printing
//           a line would corrupt a shared stream. This one is ours alone.
//   stdin   verbs in (NDJSON) — `prompt`, `shutdown`.
//   stdout  pi's and its extensions' own output, captured to the host log.
//   stderr  this host's diagnostics, captured to the same log.

import { createWriteStream } from "node:fs";
import { existsSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import {
  createAgentSession,
  DefaultResourceLoader,
  SessionManager,
  SettingsManager,
  VERSION as PI_VERSION,
} from "@earendil-works/pi-coding-agent";
// getModel is not exported from the package root at this pin — only from the
// /compat subpath, where it is marked deprecated but is the working form.
import { getModel } from "@earendil-works/pi-ai/compat";
// The pin, read from this plugin's exact dependency entry and inlined at build
// time. pi publishes its own VERSION by reading its package.json off disk at
// runtime, which a `bun build --compile` binary has no copy of — VERSION
// silently degrades to "0.0.0" there, so it cannot be what we report.
import piPlugin from "../package.json" with { type: "json" };

import {
  DeltaCoalescer,
  EnvelopeStream,
  PiEventMapper,
  parseVerb,
  type Envelope,
  type HostVerb,
  type RunSettledBody,
  type SessionReadyBody,
} from "./envelope";

/** The flush window for streamed text. Receipt in DeltaCoalescer's comment. */
const DELTA_WINDOW_MS = 30;

/** The fd the daemon reads envelopes from. */
const ENVELOPE_FD = 3;

/** The exact pi this host was built against. */
const PINNED_PI_VERSION = piPlugin.dependencies["@earendil-works/pi-coding-agent"];

/**
 * pi has no extension/version compat gate: an old build loads silently and
 * fails at the first missing call site. When pi can tell us what it is (running
 * from source, where it finds its package.json), disagreeing with the pin is
 * spawn-fatal rather than a surprise three calls later.
 */
function requirePinnedPi(): string {
  if (PI_VERSION !== "0.0.0" && PI_VERSION !== PINNED_PI_VERSION) {
    throw new Error(
      `loaded pi ${PI_VERSION} is not the pinned ${PINNED_PI_VERSION}; reinstall plugins/attn-pi dependencies`,
    );
  }
  return PINNED_PI_VERSION;
}

function requireEnv(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) {
    throw new Error(`${name} is required; attn's daemon sets it when it spawns the pi host`);
  }
  return value;
}

/**
 * Splits "provider/model-id" into pi's two-part model identity. The slash form
 * is what attn's `--model` pin and the `default_model_pi-host` setting carry.
 */
function resolveModel(pinned: string) {
  const split = pinned.indexOf("/");
  if (split <= 0 || split === pinned.length - 1) {
    throw new Error(`model ${JSON.stringify(pinned)} must be "provider/model-id" (e.g. "openai/gpt-5.6-luna")`);
  }
  const provider = pinned.slice(0, split);
  const id = pinned.slice(split + 1);
  try {
    return getModel(provider, id);
  } catch (error) {
    throw new Error(`pi has no model ${JSON.stringify(pinned)}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function main(): Promise<void> {
  const piVersion = requirePinnedPi();
  const sessionID = requireEnv("ATTN_PI_HOST_SESSION_ID");
  const sessionDir = requireEnv("ATTN_PI_HOST_SESSION_DIR");
  const cwd = requireEnv("ATTN_PI_HOST_CWD");
  const pinnedModel = requireEnv("ATTN_PI_HOST_MODEL");

  const envelopeOut = createWriteStream("", { fd: ENVELOPE_FD });
  const write = (envelope: Envelope) => {
    envelopeOut.write(`${JSON.stringify(envelope)}\n`);
  };

  const model = resolveModel(pinnedModel);
  if (!existsSync(sessionDir)) mkdirSync(sessionDir, { recursive: true });

  // Session storage is attn's: pi's own ~/.pi/agent/sessions is never written.
  // Auth and resource discovery still resolve against the real agent dir, the
  // same way a bare `pi` invocation does, so the user's credentials and
  // extensions work without a second setup.
  const agentDir = join(homedir(), ".pi", "agent");
  const sessionManager = SessionManager.create(cwd, sessionDir);
  const settingsManager = SettingsManager.create(cwd);
  const resourceLoader = new DefaultResourceLoader({ cwd, agentDir, settingsManager });
  await resourceLoader.reload();

  const { session } = await createAgentSession({ cwd, model, sessionManager, settingsManager, resourceLoader });

  // Required: without it `session_start` never fires and resource discovery
  // never runs, so extensions silently do nothing (receipt: 2026-08-04 spike).
  await session.bindExtensions({ mode: "print" });

  const stream = new EnvelopeStream(sessionID, write);
  const deltas = new DeltaCoalescer(DELTA_WINDOW_MS, (id, text) => stream.emit("message_delta", { id, text }));
  const mapper = new PiEventMapper(stream, deltas, (type) => {
    console.error(`[attn-pi-host] unmapped pi event type ${type} (pi ${piVersion})`);
  });

  session.subscribe((event) => mapper.handle(event as { type: string }));

  const ready: SessionReadyBody = {
    session_file: session.sessionFile ?? null,
    model: pinnedModel,
    cwd,
    pi_version: piVersion,
  };
  stream.emit("session_ready", ready);

  let running = false;
  let shuttingDown = false;

  const runPrompt = async (text: string) => {
    if (running) {
      // Landing a message mid-run is `steer`/`follow_up`, which is slice 2.
      // Until then the app disables its input while a run is open, so this is
      // a contract violation worth naming rather than a case to queue for.
      console.error("[attn-pi-host] refused prompt: a run is already open");
      return;
    }
    running = true;
    try {
      await session.prompt(text);
    } catch (error) {
      // A prompt can fail before pi opens a run at all — an unauthenticated
      // provider is the common one — in which case no agent_start and no
      // agent_settled ever arrive and the app would sit on a closed composer
      // forever waiting for a reply nobody is writing. Settle the run here and
      // carry the reason, so the pane says what went wrong instead of hanging.
      const message = error instanceof Error ? error.message : String(error);
      console.error(`[attn-pi-host] prompt failed: ${error instanceof Error ? error.stack : String(error)}`);
      deltas.flush();
      const settled: RunSettledBody = { error: message };
      stream.emit("run_settled", settled);
    } finally {
      running = false;
    }
  };

  const shutdown = () => {
    if (shuttingDown) return;
    shuttingDown = true;
    // Cooperative teardown is the only kind that reaches pi's tool
    // subprocesses: a hard kill of this process orphans them (receipt: 3x
    // reproduced, 2026-08-04 spike). The daemon SIGTERMs first for exactly
    // this path and only group-kills what survives the grace window.
    try {
      session.dispose();
    } catch (error) {
      console.error(`[attn-pi-host] dispose failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    envelopeOut.end();
    process.exit(0);
  };

  process.on("SIGTERM", shutdown);
  process.on("SIGINT", shutdown);

  const handleVerb = (verb: HostVerb) => {
    switch (verb.verb) {
      case "prompt":
        void runPrompt(verb.text);
        return;
      case "shutdown":
        shutdown();
        return;
    }
  };

  let buffer = "";
  for await (const chunk of process.stdin) {
    buffer += Buffer.from(chunk as Uint8Array).toString("utf8");
    let newline = buffer.indexOf("\n");
    while (newline >= 0) {
      const line = buffer.slice(0, newline).trim();
      buffer = buffer.slice(newline + 1);
      if (line !== "") {
        try {
          handleVerb(parseVerb(line));
        } catch (error) {
          console.error(`[attn-pi-host] bad verb: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
      newline = buffer.indexOf("\n");
    }
  }

  // stdin closed: the daemon is gone or has finished with us.
  shutdown();
}

main().catch((error) => {
  console.error(`[attn-pi-host] fatal: ${error instanceof Error ? error.stack : String(error)}`);
  process.exit(1);
});
