import { describe, expect, test } from "bun:test";
import { StubClassifier, type Classifier } from "../automode/classifier";
import { defaultAutoModeConfig } from "../automode/config";
import {
  createAutoMode,
  type AutoModeContextLike,
  type AutoModeDenial,
  type AutoModeExtensionAPILike,
  type InputEventLike,
  type ToolCallEventLike,
  type ToolCallEventResultLike,
} from "../automode/index";

type ToolCallHandler = (
  event: ToolCallEventLike,
  ctx: AutoModeContextLike,
) => ToolCallEventResultLike | undefined | Promise<ToolCallEventResultLike | undefined>;

// Fake pi ExtensionAPI: records the handlers the factory registers so a test
// can fire the events pi would.
class FakePi implements AutoModeExtensionAPILike {
  toolCall: ToolCallHandler | undefined;
  input: ((event: InputEventLike, ctx: AutoModeContextLike) => void) | undefined;

  on(event: "tool_call", handler: ToolCallHandler): void;
  on(event: "input", handler: (event: InputEventLike, ctx: AutoModeContextLike) => void): void;
  on(event: string, handler: unknown): void {
    if (event === "tool_call") this.toolCall = handler as ToolCallHandler;
    if (event === "input") this.input = handler as (event: InputEventLike, ctx: AutoModeContextLike) => void;
  }
}

const ctx: AutoModeContextLike = { cwd: "/work/repo" };

function toolCall(toolName: string, input: Record<string, unknown>): ToolCallEventLike {
  return { type: "tool_call", toolCallId: "call-1", toolName, input };
}

function userInput(text: string): InputEventLike {
  return { type: "input", text, source: "interactive" };
}

function wire(
  classifier: Classifier,
  extra: { enabled?: boolean; onDenial?: (denial: AutoModeDenial) => void } = {},
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

  test("the working directory comes from the context of each call", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    const write = toolCall("write", { path: "/other/tree/file.ts", content: "x" });
    expect(await pi.toolCall?.(write, { cwd: "/other/tree" })).toBeUndefined();
    expect((await pi.toolCall?.(write, ctx))?.block).toBe(true);
  });
});
