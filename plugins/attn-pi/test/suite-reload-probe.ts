// Loads the suite entrypoint twice in one process, the way pi's `/reload`
// does: it clears the extension module cache, so the same file is evaluated
// again in the same process (pi 0.83.0, resource-loader.ts -> reload() ->
// clearExtensionCache(), and its jiti runs with `moduleCache: false`).
//
// Prints nothing; the relay server counting connections is the assertion.
import { copyFileSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";

const entrypoint = process.argv[2];
if (!entrypoint) throw new Error("usage: suite-reload-probe.ts <entrypoint>");

type Handler = (event: unknown, ctx: unknown) => unknown;

const handlers = new Map<string, Handler>();

// The suite dials lazily, on the session_start it announces its identity from,
// so a load that never starts a session never opens a socket.
const ctx = {
  isIdle: () => true,
  sessionManager: { getSessionId: () => "pi-session-1" },
};

const pi = {
  on(event: string, handler: Handler): void {
    handlers.set(event, handler);
  },
  registerCommand(): void {},
  registerFlag(): void {},
  getFlag(): undefined {
    return undefined;
  },
  sendUserMessage(): void {},
};

// A byte-identical copy beside the original: same relative imports, but a
// module id bun has not evaluated yet, which is what pi's cleared cache
// produces.
const secondEvaluation = join(dirname(entrypoint), `.reload-probe.${process.pid}.ts`);
copyFileSync(entrypoint, secondEvaluation);
try {
  for (const module of [entrypoint, secondEvaluation]) {
    const { default: factory } = await import(module);
    (factory as (pi: unknown) => void)(pi);
    await handlers.get("session_start")?.({ type: "session_start", reason: "reload" }, ctx);
  }
} finally {
  rmSync(secondEvaluation, { force: true });
}

// Long enough for a second dial to land, if one was made. Then leave: the
// relay holds an open socket and a request the counting server never answers.
await new Promise((resolve) => setTimeout(resolve, 300));
process.exit(0);
