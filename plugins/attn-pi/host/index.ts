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
import { open } from "node:fs/promises";
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
  ToolDetailStore,
  TranscriptStore,
  conversationInterrupted,
  parseVerb,
  reconstructTranscript,
  type Envelope,
  type HostSessionState,
  type HostVerb,
  type HostVerbWithText,
  type RunSettledBody,
  type SessionEntryLike,
  type SessionReadyBody,
  type ToolDetailBody,
} from "./envelope";

/** The flush window for streamed text. Receipt in DeltaCoalescer's comment. */
const DELTA_WINDOW_MS = 30;

/**
 * How much tool output this host holds so an expanded card can be answered
 * without going back to disk.
 *
 * Receipt (2026-08-06, this machine, compiled host, pi 0.83.0): a host idles at
 * 130 MB RSS, and an 8-call exploration run — ls, two greps, five reads over a
 * real Go package — retained 52,463 bytes across those calls, 6.4 KB each. A
 * second run whose `seq 1 5000` pi truncated retained 10,342 bytes across 4
 * calls. 16 MB is ~320x the heavier of the two and holds ~330 calls even if
 * every one hit pi's own 50 KB per-result cap; against a 130 MB floor it is a
 * tenth of what the host already costs. Past it the oldest calls are dropped
 * and a card that asks for one says so, naming this budget. Each run logs what
 * it actually held, which is where the next remeasurement comes from.
 */
const TOOL_DETAIL_BUDGET_BYTES = 16 << 20;

/**
 * How much of pi's full-output file one `tool_detail --full` answer carries.
 *
 * pi writes the untruncated output of a clipped bash call to a temp file, and
 * that file has no bound at all — a `find /` is gigabytes. Two limits sit above
 * this one: the daemon tears a host down at a 64 MB envelope line, and the app
 * paints the answer into one DOM node. 4 MB is 80x pi's own 50 KB in-result cap
 * — roughly 50,000 terminal lines, past anything read in a chat card — and an
 * order of magnitude under the envelope ceiling. A longer file is answered with
 * its last 4 MB, which is the end pi itself keeps, and the card is told the
 * limit, the real size, and where the whole file is.
 */
const FULL_OUTPUT_LIMIT_BYTES = 4 << 20;

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

/**
 * Reads at most `limit` bytes off the END of a file.
 *
 * The end, because this reads pi's full-output file for a command whose output
 * pi itself already clipped to its tail: the last lines are the ones that
 * carry the failure, the summary, the prompt that came back. A file inside the
 * limit is read whole.
 */
async function readOutputTail(path: string, limit: number): Promise<{ text: string; clipped: boolean; size: number }> {
  const handle = await open(path, "r");
  try {
    const { size } = await handle.stat();
    if (size <= limit) {
      return { text: await handle.readFile("utf8"), clipped: false, size };
    }
    const buffer = Buffer.alloc(limit);
    await handle.read(buffer, 0, limit, size - limit);
    return { text: buffer.toString("utf8"), clipped: true, size };
  } finally {
    await handle.close();
  }
}

async function main(): Promise<void> {
  const piVersion = requirePinnedPi();
  const sessionID = requireEnv("ATTN_PI_HOST_SESSION_ID");
  const sessionDir = requireEnv("ATTN_PI_HOST_SESSION_DIR");
  const cwd = requireEnv("ATTN_PI_HOST_CWD");
  const pinnedModel = requireEnv("ATTN_PI_HOST_MODEL");

  const envelopeOut = createWriteStream("", { fd: ENVELOPE_FD });
  // The host's own transcript is fed the same envelopes the daemon forwards, so
  // a snapshot it serves cannot disagree with what a client that watched the
  // stream ended up holding. See TranscriptStore.
  const transcript = new TranscriptStore();
  const write = (envelope: Envelope) => {
    transcript.apply(envelope.kind, envelope.body);
    envelopeOut.write(`${JSON.stringify(envelope)}\n`);
  };

  const model = resolveModel(pinnedModel);
  if (!existsSync(sessionDir)) mkdirSync(sessionDir, { recursive: true });

  // Session storage is attn's: pi's own ~/.pi/agent/sessions is never written.
  // Auth and resource discovery still resolve against the real agent dir, the
  // same way a bare `pi` invocation does, so the user's credentials and
  // extensions work without a second setup.
  const agentDir = join(homedir(), ".pi", "agent");
  // REVIVE. This directory holds exactly one attn session's pi sessions, so
  // "the most recent one here" is unambiguously this conversation — and when
  // there is none, `continueRecent` creates one, which is the whole of the
  // early-crash case: a host killed before pi's first assistant message leaves
  // no session file at all (measured, 2026-08-04 spike), and the relaunch is
  // then an ordinary fresh start. Reopening also migrates the file in place,
  // which is why the version pi writes is not something attn has to track.
  const sessionManager = SessionManager.continueRecent(cwd, sessionDir);
  const settingsManager = SettingsManager.create(cwd);
  const resourceLoader = new DefaultResourceLoader({ cwd, agentDir, settingsManager });
  await resourceLoader.reload();

  const { session } = await createAgentSession({ cwd, model, sessionManager, settingsManager, resourceLoader });

  // Required: without it `session_start` never fires and resource discovery
  // never runs, so extensions silently do nothing (receipt: 2026-08-04 spike).
  await session.bindExtensions({ mode: "print" });

  const stream = new EnvelopeStream(sessionID, write);
  const deltas = new DeltaCoalescer(DELTA_WINDOW_MS, (id, text) => stream.emit("message_delta", { id, text }));
  const toolDetails = new ToolDetailStore(TOOL_DETAIL_BUDGET_BYTES);
  const mapper = new PiEventMapper(stream, deltas, (type) => {
    console.error(`[attn-pi-host] unmapped pi event type ${type} (pi ${piVersion})`);
  }, toolDetails);

  session.subscribe((event) => {
    mapper.handle(event as { type: string });
    // What the budget above is actually holding, once per run. The store drops
    // the oldest calls silently by design — the card that asks for one is where
    // the loud answer belongs — so this is the line that says how close a real
    // session gets, and it is the receipt anyone remeasuring the budget reads.
    if ((event as { type?: string }).type === "agent_settled") {
      console.error(
        `[attn-pi-host] holding ${toolDetails.retainedBytes} bytes of tool detail ` +
        `across ${toolDetails.size} call(s), budget ${TOOL_DETAIL_BUDGET_BYTES} bytes`,
      );
    }
  });

  // What came back off disk, if anything did. `buildContextEntries` is the
  // compaction-aware path pi itself sends to the model, so the pane draws the
  // same conversation the agent remembers rather than a longer one it has
  // already summarized away.
  const history = reconstructTranscript(sessionManager.buildContextEntries() as SessionEntryLike[]);
  transcript.seed(history.items);
  for (const [callID, detail] of history.details) toolDetails.put(callID, detail);
  const interrupted = conversationInterrupted(history.items);
  if (history.items.length > 0) {
    console.error(
      `[attn-pi-host] revived ${history.items.length} item(s) and ${history.details.size} tool detail(s) ` +
      `from ${session.sessionFile ?? "(no file)"}; interrupted=${interrupted}`,
    );
  }

  // A session nobody has spoken to yet is idle, and idle owes the user a turn:
  // nothing will ever happen in it until they type. A REVIVED one whose last
  // exchange never finished is `waiting_input` instead — the agent did not stop,
  // it was stopped, and saying so is the difference between "this agent is done"
  // and "this agent lost its work". Both open a turn; only the word differs, and
  // the word is the point. This is what takes the session out of `launching`.
  const readyState: HostSessionState = interrupted ? "waiting_input" : "idle";
  const ready: SessionReadyBody = {
    session_file: session.sessionFile ?? null,
    model: pinnedModel,
    cwd,
    pi_version: piVersion,
    state: readyState,
  };
  stream.emit("session_ready", ready);
  // Unconditionally, and immediately after: this host is a new process with a
  // fresh seq spine, so every client watching has to re-seed from it whether or
  // not it revived anything. `session_ready` is what resets a client's spine and
  // this is what refills it, in that order.
  stream.emit("conversation_snapshot", transcript.snapshot());

  let running = false;
  let shuttingDown = false;

  const runPrompt = async (text: string) => {
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
      const settled: RunSettledBody = { state: "idle", error: message };
      stream.emit("run_settled", settled);
    } finally {
      running = false;
    }
  };

  /**
   * Lands text in the agent, whatever it happens to be doing.
   *
   * This is the whole of "the host picks steer vs a new prompt by run state",
   * and it is why nothing upstream — not the daemon, not a nudge countdown —
   * has to know whether a run is open before delivering a message. pi's own
   * queues only exist while the agent is running: a steer queued on an idle
   * session would sit there forever with nothing to drain it, so an idle
   * session gets a run opened for the text instead.
   *
   * The receipts behind the two queues (2026-08-04 spike, re-validated at
   * 0.83.0): a steer drains at the next turn boundary, a follow-up drains only
   * when the whole run would otherwise settle.
   */
  const deliver = async (verb: HostVerbWithText) => {
    if (!running) {
      if (verb.verb === "prompt") return runPrompt(verb.text);
      // Not a violation and not a queue: the message opens the run it would
      // have interrupted.
      console.error(`[attn-pi-host] ${verb.verb} on an idle session: starting a run`);
      return runPrompt(verb.text);
    }
    if (verb.verb === "prompt") {
      // The app's composer sends steer while a run is open, so a plain prompt
      // arriving mid-run is a contract violation worth naming rather than a
      // case to guess an intent for.
      console.error("[attn-pi-host] refused prompt: a run is already open");
      return;
    }
    try {
      if (verb.verb === "steer") await session.steer(verb.text);
      else await session.followUp(verb.text);
    } catch (error) {
      // Queueing can refuse the text outright (pi rejects extension commands
      // here). Say so in the log; the run itself is unharmed and the queue the
      // app is drawing simply never gained an entry.
      console.error(`[attn-pi-host] ${verb.verb} failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  /**
   * Answers one expanded card.
   *
   * Everything the answer needs was kept when the call finished, except the
   * full output — that lives in pi's own temp file and is read here, on demand,
   * bounded. The answer is addressed by call id and goes to every client, so a
   * second client with the same card open gets it for free.
   */
  const sendToolDetail = async (callID: string, full: boolean) => {
    const held = toolDetails.get(callID);
    if (!held) {
      const reason = toolDetails.missingReason(callID);
      console.error(`[attn-pi-host] ${reason}`);
      const body: ToolDetailBody = { call_id: callID, text: "", full: false, truncated: false, error: reason };
      stream.emit("tool_detail", body);
      return;
    }
    const body: ToolDetailBody = {
      call_id: callID,
      text: held.text,
      full: false,
      truncated: held.truncated,
      ...(held.patch === undefined ? {} : { patch: held.patch }),
      ...(held.fullOutputPath === undefined ? {} : { full_output_path: held.fullOutputPath }),
    };
    if (full && held.fullOutputPath) {
      try {
        const read = await readOutputTail(held.fullOutputPath, FULL_OUTPUT_LIMIT_BYTES);
        body.text = read.text;
        body.full = true;
        body.truncated = read.clipped;
        if (read.clipped) {
          body.error =
            `showing the last ${FULL_OUTPUT_LIMIT_BYTES >> 20} MB of ${read.size} bytes; ` +
            `the whole output is at ${held.fullOutputPath}`;
        }
      } catch (error) {
        // The file is pi's, in the system temp dir, and nothing promises it
        // outlives the run. Say which file and why rather than drawing a card
        // that silently shows the clipped text it already had.
        body.error = `could not read ${held.fullOutputPath}: ${error instanceof Error ? error.message : String(error)}`;
      }
    }
    stream.emit("tool_detail", body);
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
      case "steer":
      case "follow_up":
        void deliver(verb);
        return;
      case "tool_detail":
        void sendToolDetail(verb.callID, verb.full);
        return;
      case "snapshot": {
        // Broadcast, like every other envelope, and it replaces what each
        // client had. That is safe because this transcript is fed the same
        // envelopes they were — a client cannot be holding something this does
        // not have, except items older than the window, which is the residual
        // slice 5's paging exists to retire. Broadcasting is also what makes
        // "two clients see identical state" true rather than hoped for.
        stream.emit("conversation_snapshot", transcript.snapshot());
        return;
      }
      case "clear_queue": {
        // pi clears both queues together and emits its own queue_update, which
        // is what empties the strip. The app never removes an entry itself, so
        // a clear that pi refuses leaves the strip showing what is really still
        // queued instead of a lie.
        const dropped = session.clearQueue();
        console.error(
          `[attn-pi-host] cleared the queue: ${dropped.steering.length} steering, ${dropped.followUp.length} follow-up`,
        );
        return;
      }
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
