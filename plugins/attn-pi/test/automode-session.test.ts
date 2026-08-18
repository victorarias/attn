import { describe, expect, test } from "bun:test";
import { StubClassifier } from "../automode/classifier";
import { defaultAutoModeConfig, type AutoModeConfig } from "../automode/config";
import { denialToolResult } from "../automode/denial";
import type { ToolCall } from "../automode/policy";
import { AutoModeSession, consecutiveDenialLimit, totalDenialLimit } from "../automode/session";

const cwd = "/work/repo";

function sessionWith(
  classifier = new StubClassifier(),
  overrides: Partial<AutoModeConfig> = {},
): { session: AutoModeSession; classifier: StubClassifier } {
  return { session: new AutoModeSession({ ...defaultAutoModeConfig, ...overrides }, classifier), classifier };
}

function bash(command: string): ToolCall {
  return { toolName: "bash", input: { command } };
}

describe("denial text contract", () => {
  const text = denialToolResult({ action: "bash: git push --force", reason: "force pushes rewrite shared history" });

  test("names auto mode", () => {
    expect(text).toContain("auto mode");
  });

  test("names the blocked action and the reason", () => {
    expect(text).toContain("Blocked: bash: git push --force");
    expect(text).toContain("Reason: force pushes rewrite shared history");
  });

  test("says the user's approval in conversation permits a retry", () => {
    expect(text).toContain("approval in the conversation");
    expect(text).toContain("retry");
  });

  test("multi-line input stays on the labelled lines", () => {
    const collapsed = denialToolResult({ action: "bash: a\n  b", reason: "one\ntwo" });
    expect(collapsed).toContain("Blocked: bash: a b");
    expect(collapsed).toContain("Reason: one two");
  });

  test("an empty reason says so rather than showing a blank", () => {
    expect(denialToolResult({ action: "write /etc/hosts", reason: "" })).toContain("Reason: (not stated)");
  });
});

describe("session decisions", () => {
  test("an envelope call never reaches the classifier", async () => {
    const { session, classifier } = sessionWith();
    expect(await session.decide(bash("git status"), { cwd })).toEqual({ outcome: "run", rule: "read-only-bash" });
    expect(classifier.requests).toHaveLength(0);
  });

  test("a classified allow runs and carries the reason the tree gave", async () => {
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "allow" }));
    expect(await session.decide(bash("go build ./..."), { cwd })).toEqual({ outcome: "run", rule: "classifier" });
    expect(classifier.requests[0]?.reason).toContain("go is not in the read-only set");
    expect(classifier.requests[0]?.cwd).toBe(cwd);
  });

  test("the classifier is handed the environment prose and the turn signal", async () => {
    const controller = new AbortController();
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "allow" }), {
      environment: ["this machine has no production access"],
    });
    await session.decide(bash("go build ./..."), { cwd, signal: controller.signal });
    expect(classifier.requests[0]?.environment).toEqual(["this machine has no production access"]);
    expect(classifier.requests[0]?.signal).toBe(controller.signal);
  });

  test("a classified deny blocks with the classifier's reason in the tool result", async () => {
    const { session } = sessionWith(new StubClassifier({ verdict: "deny", reason: "this deletes untracked work" }));
    const decision = await session.decide(bash("git clean -fdx"), { cwd });
    expect(decision.outcome).toBe("block");
    if (decision.outcome === "block") {
      expect(decision.rule).toBe("classifier");
      expect(decision.action).toBe("bash: git clean -fdx");
      expect(decision.toolResult).toContain("this deletes untracked work");
    }
  });

  test("uncertain fails closed, and says it could not judge", async () => {
    const { session } = sessionWith(new StubClassifier({ verdict: "uncertain", reason: "no stated intent" }));
    const decision = await session.decide(bash("go build ./..."), { cwd });
    expect(decision.outcome).toBe("block");
    if (decision.outcome === "block") expect(decision.reason).toContain("could not judge");
  });
});

describe("verdict cache", () => {
  test("an allow verdict is reused for the same intent", async () => {
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "allow" }));
    await session.decide(bash("go build ./..."), { cwd });
    const second = await session.decide(bash("go   build   ./..."), { cwd });
    expect(second).toEqual({ outcome: "run", rule: "cached-allow" });
    expect(classifier.requests).toHaveLength(1);
  });

  test("an allow verdict survives new user input", async () => {
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "allow" }));
    await session.decide(bash("go build ./..."), { cwd });
    session.noteUserInput();
    expect(await session.decide(bash("go build ./..."), { cwd })).toEqual({ outcome: "run", rule: "cached-allow" });
    expect(classifier.requests).toHaveLength(1);
  });

  test("a deny verdict is reused until the user speaks", async () => {
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "deny", reason: "not asked for" }));
    await session.decide(bash("git push origin main"), { cwd });
    expect(await session.decide(bash("git push origin main"), { cwd })).toMatchObject({ rule: "cached-deny" });
    expect(classifier.requests).toHaveLength(1);

    session.noteUserInput();
    classifier.answerWith({ verdict: "allow" });
    expect(await session.decide(bash("git push origin main"), { cwd })).toEqual({ outcome: "run", rule: "classifier" });
    expect(classifier.requests).toHaveLength(2);
  });

  test("a classifier nothing could reach blocks under its own rule", async () => {
    const classifier = new StubClassifier({
      verdict: "deny",
      layer: "2a",
      unavailable: true,
      reason: "auto mode could not reach its classifier model (layer 2a)",
    });
    const { session } = sessionWith(classifier);
    expect(await session.decide(bash("git push origin main"), { cwd })).toMatchObject({
      outcome: "block",
      rule: "classifier-unavailable",
      reason: "auto mode could not reach its classifier model (layer 2a)",
    });
  });

  test("an outage is not cached: the call is judged again once a model answers", async () => {
    const classifier = new StubClassifier({ verdict: "deny", unavailable: true, reason: "nothing could be reached" });
    const { session } = sessionWith(classifier);
    await session.decide(bash("git push origin main"), { cwd });

    classifier.answerWith({ verdict: "allow", layer: "2a" });
    expect(await session.decide(bash("git push origin main"), { cwd })).toEqual({
      outcome: "run",
      rule: "classifier-2a",
    });
    expect(classifier.requests).toHaveLength(2);
  });

  test("a different intent is judged on its own", async () => {
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "allow" }));
    await session.decide(bash("go build ./..."), { cwd });
    await session.decide(bash("go test ./..."), { cwd });
    expect(classifier.requests).toHaveLength(2);
  });
});

describe("circuit breaker", () => {
  async function denyOnce(session: AutoModeSession, command: string): Promise<void> {
    await session.decide(bash(command), { cwd });
  }

  test("counts denials and trips at the consecutive limit", async () => {
    const { session } = sessionWith(new StubClassifier({ verdict: "deny", reason: "no" }));
    for (let index = 0; index < consecutiveDenialLimit; index++) await denyOnce(session, `go run ./cmd/x${index}`);
    expect(session.breaker()).toEqual({
      consecutive: consecutiveDenialLimit,
      total: consecutiveDenialLimit,
      tripped: true,
      outage: false,
    });
  });

  test("a tripped breaker blocks without asking the classifier, naming the limits", async () => {
    const { session, classifier } = sessionWith(new StubClassifier({ verdict: "deny", reason: "no" }));
    for (let index = 0; index < consecutiveDenialLimit; index++) await denyOnce(session, `go run ./cmd/x${index}`);
    const before = classifier.requests.length;

    const decision = await session.decide(bash("go run ./cmd/y"), { cwd });
    expect(classifier.requests).toHaveLength(before);
    expect(decision.outcome).toBe("block");
    if (decision.outcome === "block") {
      expect(decision.rule).toBe("circuit-breaker");
      expect(decision.reason).toContain(String(consecutiveDenialLimit));
      expect(decision.reason).toContain(String(totalDenialLimit));
    }
  });

  test("an allowed call clears the consecutive run but not the session total", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const { session } = sessionWith(classifier);
    await denyOnce(session, "go run ./cmd/a");
    await denyOnce(session, "go run ./cmd/b");
    await session.decide(bash("git status"), { cwd });
    expect(session.breaker()).toEqual({ consecutive: 0, total: 2, tripped: false, outage: false });
  });

  test("the user speaking clears both counters", async () => {
    const { session } = sessionWith(new StubClassifier({ verdict: "deny", reason: "no" }));
    for (let index = 0; index < consecutiveDenialLimit; index++) await denyOnce(session, `go run ./cmd/x${index}`);
    expect(session.breaker().tripped).toBe(true);
    session.noteUserInput();
    expect(session.breaker()).toEqual({ consecutive: 0, total: 0, tripped: false, outage: false });
  });

  test("the total limit trips a session that keeps being told no between allows", async () => {
    const classifier = new StubClassifier({ verdict: "deny", reason: "no" });
    const { session } = sessionWith(classifier);
    for (let index = 0; index < totalDenialLimit; index++) {
      await denyOnce(session, `go run ./cmd/x${index}`);
      await session.decide(bash("git status"), { cwd });
    }
    expect(session.breaker()).toMatchObject({ consecutive: 0, total: totalDenialLimit, tripped: true });
  });

  test("an episode of pure outages says so instead of claiming refusals", async () => {
    const classifier = new StubClassifier({ verdict: "deny", unavailable: true, reason: "nothing answered" });
    const { session } = sessionWith(classifier);
    for (let index = 0; index < consecutiveDenialLimit; index++) await denyOnce(session, `go run ./cmd/x${index}`);
    expect(session.breaker()).toMatchObject({ tripped: true, outage: true });

    const decision = await session.decide(bash("go run ./cmd/y"), { cwd });
    expect(decision.outcome).toBe("block");
    if (decision.outcome === "block") {
      expect(decision.rule).toBe("circuit-breaker");
      expect(decision.reason).toContain("classifier could not be reached");
      expect(decision.reason).toContain("Nothing judged any of those calls dangerous");
      expect(decision.reason).not.toContain("has refused");
    }
  });

  test("one judged refusal in the episode makes it a refusal episode again", async () => {
    const classifier = new StubClassifier({ verdict: "deny", unavailable: true, reason: "nothing answered" });
    const { session } = sessionWith(classifier);
    await denyOnce(session, "go run ./cmd/a");
    classifier.answerWith({ verdict: "deny", layer: "2a", reason: "the user said not to" });
    await denyOnce(session, "go run ./cmd/b");
    expect(session.breaker()).toMatchObject({ total: 2, outage: false });
  });

  test("hard denies count toward the breaker", async () => {
    const { session } = sessionWith(new StubClassifier(), { hardDeny: ["git push*"] });
    await denyOnce(session, "git push origin main");
    expect(session.breaker().total).toBe(1);
  });
});
