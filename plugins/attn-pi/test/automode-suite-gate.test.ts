// The two entrypoints, loaded the way pi loads them: attn's suite, which
// composes auto mode in only when attn sent a config, and the standalone
// bundle a bare pi gets with `-e`.
import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { autoModeConfigEnvVar } from "../automode/source";

const probe = join(import.meta.dir, "automode-suite-probe.ts");
const suiteEntry = join(import.meta.dir, "..", "suite", "index.ts");
const standaloneEntry = join(import.meta.dir, "..", "automode", "standalone.ts");

type Registered = { events: string[]; commands: string[]; flags: string[] };

async function load(entrypoint: string, env: Record<string, string>): Promise<Registered> {
  const run = Bun.spawn([process.execPath, "run", probe, entrypoint], {
    // A clean environment, so a machine that happens to run attn does not hand
    // this test a config nobody asked for.
    env: { PATH: process.env.PATH ?? "", HOME: process.env.HOME ?? "", ...env },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [stdout, stderr, code] = await Promise.all([
    new Response(run.stdout).text(),
    new Response(run.stderr).text(),
    run.exited,
  ]);
  if (code !== 0) throw new Error(`suite probe failed (${code}): ${stderr}`);
  return JSON.parse(stdout) as Registered;
}

describe("suite composition", () => {
  test("a bare pi loading the suite is untouched by auto mode", async () => {
    const registered = await load(suiteEntry, {});
    expect(registered.commands).toEqual([]);
    expect(registered.flags).toEqual([]);
    expect(registered.events).toEqual([]);
  });

  test("attn's config composes auto mode into the suite", async () => {
    const registered = await load(suiteEntry, {
      [autoModeConfigEnvVar]: JSON.stringify({ enabled_default: true }),
    });
    expect(registered.commands).toEqual(["auto"]);
    expect(registered.flags).toEqual(["auto", "no-auto"]);
    expect(registered.events).toContain("tool_call");
    expect(registered.events).toContain("session_start");
  });
});

describe("the standalone extension", () => {
  test("`pi -e automode.js` registers auto mode with no attn anywhere", async () => {
    const registered = await load(standaloneEntry, { PI_CODING_AGENT_DIR: "/nonexistent" });
    expect(registered.commands).toEqual(["auto"]);
    expect(registered.flags).toEqual(["auto", "no-auto"]);
    expect(registered.events).toContain("tool_call");
  });
});
