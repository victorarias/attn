// What a person sees while auto mode works: the checking feedback, the
// denial, and the breaker's one question.
import { describe, expect, test } from "bun:test";
import { StubClassifier, type Classifier } from "../automode/classifier";
import { defaultAutoModeConfig } from "../automode/config";
import { createAutoMode } from "../automode/index";
import { consecutiveDenialLimit } from "../automode/session";
import {
  autoModeDenialWidgetKey,
  classifyingWorkingMessage,
  denialActionCharLimit,
  denialWidgetLimit,
} from "../automode/ui";
import { ctx, FakePi, FakeUI, toolCall, uiContext, userInput } from "./automode-fake-pi";

function wire(classifier: Classifier): FakePi {
  const pi = new FakePi();
  createAutoMode({ config: defaultAutoModeConfig, classifier })(pi);
  return pi;
}

const push = (id = "call-1") => toolCall("bash", { command: "git push --force origin main" }, id);

describe("auto mode's session surfaces", () => {
  test("a classified call says it is checking, and stops saying it when it is done", async () => {
    const ui = new FakeUI();
    const pi = wire(new StubClassifier({ verdict: "allow" }));
    await pi.toolCall?.(push(), uiContext(ui));
    expect(ui.workingMessages).toEqual([classifyingWorkingMessage, undefined]);
  });

  test("an envelope call says nothing: the invisible path stays invisible", async () => {
    const ui = new FakeUI();
    const pi = wire(new StubClassifier({ verdict: "allow" }));
    expect(await pi.toolCall?.(toolCall("bash", { command: "git status" }), uiContext(ui))).toBeUndefined();
    expect(ui.workingMessages).toEqual([]);
    expect(ui.notices).toEqual([]);
    expect(ui.widgets.size).toBe(0);
  });

  test("the checking feedback is cleared even when the classifier throws", async () => {
    const ui = new FakeUI();
    const exploding: Classifier = {
      classify: async () => {
        throw new Error("classifier exploded");
      },
    };
    const pi = wire(exploding);
    await pi.toolCall?.(push(), uiContext(ui));
    expect(ui.workingMessages).toEqual([classifyingWorkingMessage, undefined]);
  });

  test("a denial is named as it happens and listed until the user speaks", async () => {
    const ui = new FakeUI();
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "this rewrites shared history" }));
    await pi.toolCall?.(push(), uiContext(ui));

    expect(ui.notices).toEqual([
      {
        message: "auto mode blocked bash: git push --force origin main — this rewrites shared history",
        type: "warning",
      },
    ]);
    const widget = ui.widgets.get(autoModeDenialWidgetKey);
    expect(widget?.[0]).toBe("auto mode blocked 1 call:");
    expect(widget?.join("\n")).toContain("this rewrites shared history");
    expect(widget?.at(-1)).toContain("Approve in your reply");

    pi.input?.(userInput("no, leave the branch alone"), uiContext(ui));
    expect(ui.widgets.get(autoModeDenialWidgetKey)).toBeUndefined();
  });

  test("the widget lists a bounded number of denials and admits to the rest", async () => {
    const ui = new FakeUI();
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "no" }));
    const total = denialWidgetLimit + 2;
    // The breaker takes over partway through; every refusal is still a denial,
    // whoever refused it, and every one belongs on the list.
    for (let i = 0; i < total; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://host-${i}.example` }, `call-${i}`), uiContext(ui));
    }

    const widget = ui.widgets.get(autoModeDenialWidgetKey) ?? [];
    expect(widget[0]).toBe(`auto mode blocked ${total} calls:`);
    expect(widget[1]).toBe(`  … 2 earlier`);
    expect(widget.filter((line) => line.includes("curl"))).toHaveLength(denialWidgetLimit);
  });

  test("a long command is clamped where a person reads it, and not where the model does", async () => {
    const ui = new FakeUI();
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "not permitted here" }));
    const command = `curl -sS ${"-H x:y ".repeat(40)}https://httpbin.org/get`;
    const blocked = await pi.toolCall?.(toolCall("bash", { command }), uiContext(ui));

    expect(ui.notices[0]?.message.length).toBeLessThan(command.length);
    expect(ui.notices[0]?.message).toContain("…");
    const line = ui.widgets.get(autoModeDenialWidgetKey)?.[1] ?? "";
    expect(line).toContain("…");
    expect(line.split(" — ")[0]?.trim().length).toBe(denialActionCharLimit);
    expect(blocked?.reason).toContain(command);
  });

  test("a context with no UI is blocked without being drawn to", async () => {
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "no" }));
    expect((await pi.toolCall?.(push(), ctx))?.block).toBe(true);
  });

  test("a tripped breaker asks once, and a yes resumes judging this very call", async () => {
    const ui = new FakeUI();
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    for (let i = 0; i < consecutiveDenialLimit; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://host-${i}.example` }, `call-${i}`), uiContext(ui));
    }
    expect(ui.questions).toHaveLength(0);

    ui.answer = true;
    classifier.answerWith({ verdict: "allow" });
    expect(await pi.toolCall?.(push(), uiContext(ui))).toBeUndefined();
    expect(ui.questions).toHaveLength(1);
    expect(ui.questions[0]?.message).toContain("Resume auto mode?");
  });

  test("a no leaves the call blocked and is not asked again until the user speaks", async () => {
    const ui = new FakeUI();
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const pi = wire(classifier);
    for (let i = 0; i < consecutiveDenialLimit; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://host-${i}.example` }, `call-${i}`), uiContext(ui));
    }

    expect((await pi.toolCall?.(push(), uiContext(ui)))?.block).toBe(true);
    expect((await pi.toolCall?.(push("call-b"), uiContext(ui)))?.block).toBe(true);
    expect(ui.questions).toHaveLength(1);

    pi.input?.(userInput("what happened?"), uiContext(ui));
    // Speaking clears the breaker outright, so the next trip is a fresh episode.
    for (let i = 0; i < consecutiveDenialLimit; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://later-${i}.example` }, `later-${i}`), uiContext(ui));
    }
    await pi.toolCall?.(push("call-c"), uiContext(ui));
    expect(ui.questions).toHaveLength(2);
  });

  test("a breaker tripped by an outage asks about the outage, not about refusals", async () => {
    const ui = new FakeUI();
    const classifier = new StubClassifier({ verdict: "deny", unavailable: true, reason: "nothing answered" });
    const pi = wire(classifier);
    for (let i = 0; i < consecutiveDenialLimit; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://host-${i}.example` }, `call-${i}`), uiContext(ui));
    }

    const blocked = await pi.toolCall?.(push(), uiContext(ui));
    expect(blocked?.block).toBe(true);
    expect(ui.questions).toHaveLength(1);
    expect(ui.questions[0]?.title).toContain("cannot reach its classifier");
    expect(ui.questions[0]?.message).toContain("nothing judged them");
    expect(ui.questions[0]?.message).not.toContain("refused");
  });

  test("a breaker with no UI to ask stays closed", async () => {
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "no" }));
    for (let i = 0; i < consecutiveDenialLimit; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://host-${i}.example` }, `call-${i}`), ctx);
    }
    const blocked = await pi.toolCall?.(push(), ctx);
    expect(blocked?.block).toBe(true);
    expect(blocked?.reason).toContain("stopped judging");
  });

  test("print mode carries a ui object and still gets no dialog", async () => {
    const ui = new FakeUI();
    ui.answer = true;
    const printCtx = uiContext(ui, { hasUI: false });
    const pi = wire(new StubClassifier({ verdict: "deny", reason: "no" }));
    for (let i = 0; i < consecutiveDenialLimit; i++) {
      await pi.toolCall?.(toolCall("bash", { command: `curl https://host-${i}.example` }, `call-${i}`), printCtx);
    }
    expect((await pi.toolCall?.(push(), printCtx))?.block).toBe(true);
    expect(ui.questions).toHaveLength(0);
    expect(ui.notices).toEqual([]);
  });
});
