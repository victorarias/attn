import { describe, expect, test } from "bun:test";
import { StubClassifier, type Classifier } from "../automode/classifier";
import { defaultAutoModeConfig } from "../automode/config";
import { createAutoMode, type AutoModeDenial } from "../automode/index";
import { UsageLedger } from "../automode/usage";
import { assistantMessage, ctx, FakePi, toolCall, userInput } from "./automode-fake-pi";

function wire(
  classifier: Classifier,
  extra: { enabled?: boolean; onDenial?: (denial: AutoModeDenial) => void; usageLedger?: UsageLedger } = {},
): FakePi {
  const pi = new FakePi();
  createAutoMode({ config: defaultAutoModeConfig, classifier, ...extra })(pi);
  return pi;
}

describe("auto mode extension", () => {
  test("an envelope call is not blocked", async () => {
    const pi = wire(new StubClassifier());
    expect(await pi.toolCall?.(toolCall("bash", { command: "git status" }), ctx)).toBeUndefined();
  });

  test("a refused call blocks with the denial contract as its reason", async () => {
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "this rewrites shared history" }));
    const result = await pi.toolCall?.(toolCall("bash", { command: "git push --force" }), ctx);
    expect(result?.block).toBe(true);
    expect(result?.reason).toContain("auto mode");
    expect(result?.reason).toContain("this rewrites shared history");
    expect(result?.reason).toContain("approval in the conversation");
  });

  test("a denial is reported with the call it blocked", async () => {
    const denials: AutoModeDenial[] = [];
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "nope" }), { onDenial: (d) => denials.push(d) });
    await pi.toolCall?.(toolCall("bash", { command: "git push --force" }), ctx);
    expect(denials).toEqual([
      { toolCallId: "call-1", action: "bash: git push --force", reason: "nope", rule: "classifier" },
    ]);
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

  test("auto mode off registers nothing", () => {
    const pi = wire(new StubClassifier(), { enabled: false });
    expect(pi.toolCall).toBeUndefined();
    expect(pi.input).toBeUndefined();
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
    expect(result?.systemPrompt).toContain("Auto mode is on for this session");
    expect(result?.systemPrompt).toContain("approval in the conversation");
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
    pi.input?.(userInput("push it"), ctx);
    pi.say("push it");
    await pi.toolCall?.(toolCall("bash", { command: "git push origin main" }), ctx);
    expect(classifier.requests[0]?.transcript).toEqual([{ role: "user", text: "push it" }]);
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
