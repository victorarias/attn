import { afterAll, describe, expect, test } from "bun:test";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { availableModels, catalogFromModels } from "../automode/models";

const root = mkdtempSync(join(tmpdir(), "pi-models-"));
afterAll(() => rmSync(root, { recursive: true, force: true }));
let nextFixture = 0;

function fixture(source: string) {
  const dir = join(root, String(nextFixture++));
  mkdirSync(dir);
  const executable = join(dir, "pi");
  writeFileSync(executable, `#!${process.execPath}\n${source}`, { mode: 0o755 });
  return { dir, executable };
}

const readRequest = `
let pending = "";
for await (const chunk of Bun.stdin.stream()) {
  pending += new TextDecoder().decode(chunk);
  if (pending.includes("\\n")) break;
}
const request = JSON.parse(pending.trim());
const reply = (models) => JSON.stringify({ type: "response", id: request.id,
  command: request.type, success: true, data: { models } });
`;

const waitForEOF = `
process.stdin.resume();
process.stdin.on("end", () => process.exit(0));
`;

describe("Pi model discovery", () => {
  test("keeps only display metadata and sorts providers and models", () => {
    const answer = catalogFromModels([
      { provider: "z", id: "b", apiKey: "private", headers: { apikey: "private" } },
      { provider: "a", id: "m", baseUrl: "https://private", name: "Model", contextWindow: 500000 },
      { provider: "z", id: "a" },
      { provider: "z", id: "b", name: "Updated" },
    ]);
    expect(answer).toEqual({ providers: [
      { provider: "a", ready: true, models: [{ id: "m", name: "Model", contextWindow: 500000 }] },
      { provider: "z", ready: true, models: [{ id: "a" }, { id: "b", name: "Updated" }] },
    ] });
  });

  test("uses one correlated RPC response despite logs and split JSONL records", async () => {
    const { dir, executable } = fixture(`${readRequest}
console.log("extension log with private data");
console.log(JSON.stringify({ type: "response", id: "other", data: { models: [] } }));
const output = reply([{ provider: "extension", id: "custom", name: "A\\u2028B", headers: { secret: "private" } }]);
process.stdout.write(output.slice(0, 37));
process.stdout.write(output.slice(37) + "\\r\\n");
${waitForEOF}`);
    const answer = await availableModels(executable, process.env, { cwd: dir });
    expect(answer.providers).toEqual([
      { provider: "extension", ready: true, models: [{ id: "custom", name: "A\u2028B" }] },
    ]);
  });

  test("a failed RPC cannot turn into an empty successful catalog or expose its error", async () => {
    const { dir, executable } = fixture(`${readRequest}
console.log(JSON.stringify({ type: "response", id: request.id, command: request.type,
  success: false, error: "secret from extension" }));
${waitForEOF}`);
    await expect(availableModels(executable, process.env, { cwd: dir }))
      .rejects.toThrow("Pi could not list its models");
  });

  test("rejects malformed catalog data", async () => {
    const { dir, executable } = fixture(`${readRequest}
console.log(reply({ unexpected: true }));
${waitForEOF}`);
    await expect(availableModels(executable, process.env, { cwd: dir }))
      .rejects.toThrow("Pi returned an invalid model catalog");
  });

  test("an early exit is a failure and never includes raw stderr", async () => {
    const { dir, executable } = fixture('console.error("private credential"); process.exit(7);');
    await expect(availableModels(executable, process.env, { cwd: dir }))
      .rejects.toThrow("Pi exited 7");
  });

  test("reaps a process that returns a catalog but refuses to exit", async () => {
    const { dir, executable } = fixture(`${readRequest}
await Bun.write("child.pid", String(process.pid));
console.log(reply([]));
setInterval(() => {}, 1000);
`);
    await expect(availableModels(executable, process.env, { cwd: dir, timeoutMs: 1000 }))
      .rejects.toThrow("timed out");
    const pid = Number(readFileSync(join(dir, "child.pid"), "utf8"));
    expect(() => process.kill(pid, 0)).toThrow();
  });

  test("loads native extension providers through real Pi and applies their auth", async () => {
    const cli = resolve(import.meta.dir, "../node_modules/@earendil-works/pi-coding-agent/dist/cli.js");
    const { dir, executable } = fixture(`await import(${JSON.stringify(cli)});`);
    const agentDir = join(dir, "agent");
    mkdirSync(join(agentDir, "extensions"), { recursive: true });
    writeFileSync(join(agentDir, "extensions", "provider.ts"), `
export default function(pi) {
  const model = { provider: "native-extension", id: "judge", name: "Extension Judge",
    api: "anthropic-messages", baseUrl: "http://127.0.0.1:9", reasoning: false,
    input: ["text"], contextWindow: 500000, maxTokens: 4096,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    headers: { "x-private": "must-not-leave-the-process" } };
  const noPrompt = () => { throw new Error("Model discovery must not invoke a model"); };
  pi.registerProvider({ id: model.provider, name: "Extension provider", getModels: () => [model],
    auth: { apiKey: { name: "Test key", async resolve({ ctx }) {
      const key = await ctx.env("PI_DISCOVERY_TEST_KEY");
      return key ? { auth: { apiKey: key } } : undefined;
    } } }, stream: noPrompt, streamSimple: noPrompt });
}`);
    const env = { PATH: process.env.PATH, PI_CODING_AGENT_DIR: agentDir, PI_DISCOVERY_TEST_KEY: "test-key" };
    const answer = await availableModels(executable, env, { cwd: dir });
    expect(answer.providers).toEqual([{
      provider: "native-extension", ready: true,
      models: [{ id: "judge", name: "Extension Judge", contextWindow: 500000, effortSupport: "unsupported", effortLevels: [] }],
    }]);
    expect(JSON.stringify(answer)).not.toContain("must-not-leave");
    const unauthenticated = await availableModels(executable, { ...env, PI_DISCOVERY_TEST_KEY: undefined }, { cwd: dir });
    expect(unauthenticated.providers).toEqual([]);
  }, 15000);
});
