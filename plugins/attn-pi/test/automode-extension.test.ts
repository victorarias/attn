import { describe, expect, test } from "bun:test";
import { StubClassifier, type Classifier } from "../automode/classifier";
import { defaultAutoModeConfig } from "../automode/config";
import { createAutoMode, type AutoModeDenial } from "../automode/index";
import { transcriptEntryCharLimit } from "../automode/transcript";
import { UsageLedger } from "../automode/usage";
import { assistantMessage, ctx, FakePi, toolCall, userInput } from "./automode-fake-pi";
import { autoModeSystemPromptAddendum } from "../automode/addendum";

function wire(
  classifier: Classifier,
  extra: { isEnabled?: () => boolean; onDenial?: (denial: AutoModeDenial) => void; usageLedger?: UsageLedger } = {},
): FakePi {
  const pi = new FakePi();
  createAutoMode({ config: defaultAutoModeConfig, classifier, ...extra })(pi);
  return pi;
}

describe("auto mode extension", () => {
  test("a fast-path call is not blocked", async () => {
    const pi = wire(new StubClassifier());
    expect(await pi.toolCall?.(toolCall("bash", { command: "git status" }), ctx)).toBeUndefined();
  });

  test("a refused call blocks with the denial contract as its reason", async () => {
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "this rewrites shared history" }));
    const result = await pi.toolCall?.(toolCall("bash", { command: "git push --force" }), ctx);
    expect(result?.block).toBe(true);
    expect(result?.reason).toContain("this rewrites shared history");
  });

  test("a denial is reported with the call it blocked", async () => {
    const denials: AutoModeDenial[] = [];
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "nope" }), { onDenial: (d) => denials.push(d) });
    await pi.toolCall?.(toolCall("bash", { command: "git push --force" }), ctx);
    expect(denials).toEqual([
      {
        toolCallId: "call-1",
        tool: "bash",
        action: "bash: git push --force",
        reason: "nope",
        rule: "classifier",
        at: expect.any(String),
      },
    ]);
    expect(Number.isNaN(Date.parse(denials[0]!.at))).toBe(false);
  });

  test("a reporter that throws still leaves the call blocked", async () => {
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "nope" }), {
      onDenial: () => {
        throw new Error("relay is gone");
      },
    });
    const result = await pi.toolCall?.(toolCall("bash", { command: "git push --force" }), ctx);
    expect(result?.block).toBe(true);
    expect(result?.reason).toContain("nope");
  });

  test("a thrown classifier blocks the tool rather than letting it run", async () => {
    const exploding: Classifier = {
      classify: () => {
        throw new Error("classifier exploded");
      },
    };
    const pi = wire(exploding);
    const result = await pi.toolCall?.(toolCall("bash", { command: "go build ./..." }), ctx);
    expect(result?.block).toBe(true);
    expect(result?.reason).toContain("classifier exploded");
    expect(result?.reason).toContain("auto mode");
  });

  test("user input drops the cached deny, so the next reply can be the grant", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "not asked for" });
    const pi = wire(classifier);
    const call = () => pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);

    expect((await call())?.block).toBe(true);
    pi.input?.(userInput("go ahead, push it"), ctx);
    classifier.answerWith({ verdict: "allow" });
    expect(await call()).toBeUndefined();
  });

  test("input an extension sent is not the user speaking", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "not asked for" });
    const pi = wire(classifier);
    const call = () => pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);

    await call();
    pi.input?.({ type: "input", text: "automated nudge", source: "extension" }, ctx);
    classifier.answerWith({ verdict: "allow" });
    expect((await call())?.block).toBe(true);
  });

  test("auto mode off judges nothing, and still listens to the conversation", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    let on = false;
    const pi = wire(classifier, { isEnabled: () => on });
    const push = () => pi.toolCall?.(toolCall("bash", { command: "git push --force" }), ctx);

    expect(await push()).toBeUndefined();
    expect(classifier.requests).toHaveLength(0);

    pi.say("clean up the branch");
    on = true;
    expect((await push())?.block).toBe(true);
    expect(classifier.requests[0]?.transcript).toEqual([{ role: "user", text: "clean up the branch" }]);
  });

  test("auto mode off leaves the system prompt alone", () => {
    const pi = wire(new StubClassifier(), { isEnabled: () => false });
    const result = pi.beforeAgentStart?.(
      { type: "before_agent_start", prompt: "ship it", systemPrompt: "pi's own prompt" },
      ctx,
    );
    expect(result?.systemPrompt).toBeUndefined();
  });

  test("each factory run gets its own session state", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const factory = createAutoMode({ config: defaultAutoModeConfig, classifier });
    const first = new FakePi();
    factory(first);
    await first.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);

    const second = new FakePi();
    factory(second);
    classifier.answerWith({ verdict: "allow" });
    expect(await second.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx)).toBeUndefined();
  });

  test("the system prompt gains the addendum, keeping what pi assembled", () => {
    const pi = wire(new StubClassifier());
    const result = pi.beforeAgentStart?.(
      { type: "before_agent_start", prompt: "ship it", systemPrompt: "pi's own prompt" },
      ctx,
    );
    expect(result?.systemPrompt).toContain("pi's own prompt");
    expect(result?.systemPrompt).toContain(autoModeSystemPromptAddendum());
  });

  test("what the user and the agent said reaches the classifier; tool results do not", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    pi.say("get CI green", "I'll fix the retry.");
    pi.messageEnd?.(
      { type: "message_end", message: { role: "toolResult", content: [{ type: "text", text: "SECRET=hunter2" }] } },
      ctx,
    );
    await pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);

    expect(classifier.requests[0]?.transcript).toEqual([
      { role: "user", text: "get CI green" },
      { role: "assistant", text: "I'll fix the retry." },
    ]);
  });

  test("one message arriving on both seams is recorded once", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    pi.say("push it");
    await pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);
    expect(classifier.requests[0]?.transcript).toEqual([{ role: "user", text: "push it" }]);
  });

  test("an oversized message arriving on both seams is recorded once", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    pi.say(`${"x".repeat(transcriptEntryCharLimit * 2)} and don't push yet`);
    await pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);
    expect(classifier.requests[0]?.transcript).toHaveLength(1);
  });

  test("a prompt an extension submitted grants nothing", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    const push = () => pi.toolCall?.(toolCall("bash", { command: "git push --force origin main" }), ctx);
    expect((await push())?.block).toBe(true);

    pi.say("go ahead, force-push it", undefined, "extension");
    expect((await push())?.block).toBe(true);

    expect(classifier.requests).toHaveLength(1);

    pi.say("go ahead, force-push it");
    classifier.answerWith({ verdict: "allow" });
    expect(await push()).toBeUndefined();
  });

  test("an extension's prompt stays out of the transcript, and still gets the addendum", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    pi.input?.({ type: "input", text: "summarize the diff", source: "extension" }, ctx);
    const result = pi.beforeAgentStart?.(
      { type: "before_agent_start", prompt: "summarize the diff", systemPrompt: "pi's own prompt" },
      ctx,
    );
    await pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);

    expect(classifier.requests[0]?.transcript).toEqual([]);
    expect(result?.systemPrompt).toContain("Auto mode is on for this session");
  });

  test("held classifier usage rides the next tool result, keeping the tool's own", () => {
    const ledger = new UsageLedger();
    const pi = wire(new StubClassifier(), { usageLedger: ledger });
    ledger.add({ input: 900, output: 20, cost: { total: 0.0006 } });

    const result = pi.toolResult?.({ type: "tool_result", toolCallId: "call-1", usage: { input: 5, cost: { total: 1 } } }, ctx);
    expect(result?.usage?.input).toBe(905);
    expect(result?.usage?.cost?.total).toBeCloseTo(1.0006, 6);
    expect(pi.toolResult?.({ type: "tool_result", toolCallId: "call-2" }, ctx)).toBeUndefined();
  });

  test("the working directory comes from the context of each call", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    const write = toolCall("write", { path: "/other/tree/file.ts", content: "x" });
    expect(await pi.toolCall?.(write, { cwd: "/other/tree" })).toBeUndefined();
    expect((await pi.toolCall?.(write, ctx))?.block).toBe(true);
  });
});
