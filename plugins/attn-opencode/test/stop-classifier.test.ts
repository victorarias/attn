import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { OpenCodeHTTP } from "../src/opencode-http";
import { OpenCodeStopClassifier, classifierSystemPrompt, classifierUserPrompt, parseStrictVerdict } from "../src/stop-classifier";
import type { OpenCodeModel } from "../src/types";
import { FakeOpenCode } from "./fake-opencode";
import evaluationFixtures from "./fixtures/stop-classifier-evaluation.json";

const servers: FakeOpenCode[] = [];
const model: OpenCodeModel = { providerID: "provider", id: "model", variant: "max" };

afterEach(() => {
  for (const server of servers.splice(0)) server.close();
});

function target(): FakeOpenCode {
  const result = new FakeOpenCode("secret");
  servers.push(result);
  return result;
}

function client(server: FakeOpenCode): OpenCodeHTTP {
  return new OpenCodeHTTP({ port: server.port, password: "secret" });
}

describe("OpenCode assistant message normalization", () => {
  test("extracts only the newest completed assistant prose", async () => {
    const server = target();
    server.messages.set("linked", [
      { info: { id: "old", role: "assistant", time: { completed: 10 }, model }, parts: [{ type: "text", text: "old" }] },
      { info: { id: "user", role: "user", time: { completed: 40 } }, parts: [{ type: "text", text: "user" }] },
      { info: { id: "error", role: "assistant", error: { name: "bad" }, time: { completed: 50 }, model }, parts: [{ type: "text", text: "bad" }] },
      { info: { id: "incomplete", role: "assistant", model }, parts: [{ type: "text", text: "unfinished" }] },
      {
        info: { id: "new", role: "assistant", time: { completed: 30 }, model },
        parts: [
          { type: "reasoning", text: "private" },
          { type: "text", text: "Could you" },
          { type: "text", text: " ignore", ignored: true },
          { type: "text", text: " synthetic", synthetic: true },
          { type: "tool", input: "secret" },
          { type: "text", text: " choose?" },
        ],
      },
    ]);

    await expect(client(server).lastAssistantTurn("linked")).resolves.toEqual({
      messageID: "new",
      completedAt: 30,
      model,
      text: "Could you choose?",
      textHash: createHash("sha256").update("Could you choose?").digest("hex"),
    });
    expect(server.requests.find((request) => request.path.endsWith("/message"))?.search).toBe("?limit=20");
  });

  test("returns no turn for empty completed prose but rejects malformed prose metadata", async () => {
    const server = target();
    const http = client(server);
    server.messages.set("empty", [
      { info: { id: "empty", role: "assistant", time: { completed: 10 }, model }, parts: [{ type: "reasoning", text: "only reasoning" }] },
    ]);
    await expect(http.lastAssistantTurn("empty")).resolves.toBeUndefined();

    server.messages.set("malformed", [
      { info: { id: "bad", role: "assistant", time: { completed: 10 } }, parts: [{ type: "text", text: "Need input?" }] },
    ]);
    await expect(http.lastAssistantTurn("malformed")).rejects.toThrow("missing identity or model metadata");
  });

  test("skips a newer completed tool-only assistant message without hiding older prose", async () => {
    const server = target();
    server.messages.set("linked", [
      { info: { id: "question", role: "assistant", time: { completed: 10 }, model }, parts: [{ type: "text", text: "Should I continue?" }] },
      { info: { id: "tool-only", role: "assistant", time: { completed: 20 }, model }, parts: [{ type: "tool", input: "opaque" }] },
    ]);

    await expect(client(server).lastAssistantTurn("linked")).resolves.toEqual(expect.objectContaining({
      messageID: "question",
      text: "Should I continue?",
    }));
  });
});

describe("isolated OpenCode stop classifier", () => {
  test("keeps the evaluation set balanced and routes native questions outside the classifier", () => {
    const semantic = evaluationFixtures.filter((fixture) => fixture.route === "classifier");
    expect(semantic.filter((fixture) => fixture.expected === "waiting_input")).toHaveLength(2);
    expect(semantic.filter((fixture) => fixture.expected === "idle")).toHaveLength(3);
    expect(evaluationFixtures.filter((fixture) => fixture.route === "native_question")).toEqual([
      expect.objectContaining({ expected: "waiting_input" }),
    ]);
  });

  test("disables every discovered tool, parses strict JSON, and deletes its session", async () => {
    const server = target();
    server.toolIDs = ["bash", "read", "bash"];
    server.classifierReplies.push('{"verdict":"WAITING"}');
    const verdict = await new OpenCodeStopClassifier(client(server)).classify({
      linkedSessionID: "linked",
      model,
      assistantText: "Would you like me to continue?",
      signal: new AbortController().signal,
    });

    expect(verdict).toBe("waiting_input");
    expect(server.classifierPrompts).toHaveLength(1);
    expect(server.classifierPrompts[0]?.body).toEqual({
      system: classifierSystemPrompt,
      tools: { bash: false, read: false },
      parts: [{ type: "text", text: classifierUserPrompt("Would you like me to continue?") }],
      model: { providerID: "provider", modelID: "model" },
      variant: "max",
    });
    expect(server.deletedSessions).toEqual(["native-1"]);
    expect(server.requests.every((request) => request.authorization?.startsWith("Basic "))).toBe(true);
  });

  test("returns unknown and still deletes the session on tool, prompt, parse, and cleanup failures", async () => {
    for (const failure of ["tools", "prompt", "parse", "cleanup"] as const) {
      const server = target();
      if (failure === "tools") server.toolIDs = { invalid: true };
      if (failure === "prompt") server.failClassifierPrompt = true;
      if (failure === "parse") server.classifierReplies.push("WAITING");
      if (failure === "cleanup") server.failDeleteSession = true;
      const verdict = await new OpenCodeStopClassifier(client(server)).classify({
        linkedSessionID: "linked",
        model,
        assistantText: "Question?",
        signal: new AbortController().signal,
      });
      expect(verdict).toBe("unknown");
      const deleteRequests = server.requests.filter((request) => request.method === "DELETE");
      expect(deleteRequests).toHaveLength(1);
    }
  });

  test("accepts only the exact verdict object", () => {
    expect(parseStrictVerdict('{"verdict":"DONE"}')).toBe("idle");
    expect(parseStrictVerdict('{"verdict":"WAITING"}')).toBe("waiting_input");
    for (const malformed of ["DONE", "```json\n{\"verdict\":\"DONE\"}\n```", '{"state":"DONE"}', '{"verdict":"DONE","why":"extra"}', '{"verdict":"done"}']) {
      expect(parseStrictVerdict(malformed)).toBe("unknown");
    }
  });
});
