// A clean nisse shutdown has to reach extensions: pi emits `session_shutdown`
// from AgentSessionRuntime, and the host builds an AgentSession, whose
// dispose() emits nothing (pi 0.83.0). This runs the real host against a
// throwaway pi home holding one extension that records what it heard.
import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const host = join(import.meta.dir, "..", "host", "index.ts");

describe("nisse shutdown", () => {
  test("an extension's session_shutdown handler runs before the host exits", async () => {
    const root = mkdtempSync(join(tmpdir(), "attn-nisse-"));
    const extensions = join(root, "home", ".pi", "agent", "extensions");
    const cwd = join(root, "cwd");
    const sessionDir = join(root, "state");
    for (const dir of [extensions, cwd, sessionDir]) mkdirSync(dir, { recursive: true });

    const heard = join(root, "heard.log");
    writeFileSync(
      join(extensions, "shutdown-probe.ts"),
      [
        `import { appendFileSync } from "node:fs";`,
        `export default function probe(pi: any) {`,
        `  pi.on("session_start", () => appendFileSync(${JSON.stringify(heard)}, "session_start\\n"));`,
        `  pi.on("session_shutdown", (event: any) =>`,
        `    appendFileSync(${JSON.stringify(heard)}, \`session_shutdown \${event.reason}\\n\`));`,
        `}`,
        "",
      ].join("\n"),
    );

    const run = Bun.spawn([process.execPath, "run", host], {
      cwd,
      env: {
        PATH: process.env.PATH ?? "",
        // pi resolves auth, settings and extensions against $HOME/.pi/agent;
        // this run must never read or write the real one.
        HOME: join(root, "home"),
        ATTN_NISSE_SESSION_ID: "session-1",
        ATTN_NISSE_SESSION_DIR: sessionDir,
        ATTN_NISSE_CWD: cwd,
        ATTN_NISSE_MODEL: "openai/gpt-5.6-luna",
      },
      // fd 3 is the envelope stream; the host writes to it on startup.
      stdio: ["pipe", "pipe", "pipe", Bun.file(join(root, "envelopes.ndjson"))],
    });
    run.stdin.write('{"verb":"shutdown"}\n');
    run.stdin.end();
    const [stderr] = await Promise.all([new Response(run.stderr).text(), run.exited]);

    const lines = readFileSync(heard, "utf8").trim().split("\n");
    expect(lines).toEqual(["session_start", "session_shutdown quit"]);
    expect(stderr).not.toContain("session_shutdown failed");
  }, 30_000);
});
