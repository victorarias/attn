import { classifyBashCommand } from "./bash";
import { matchesAnyPattern, type AutoModeConfig } from "./config";
import { locatePath } from "./paths";

export type ToolCall = {
  toolName: string;
  input: Record<string, unknown>;
};

export const readOnlyTools: readonly string[] = ["read", "grep", "find", "ls"];

export const pathTools: readonly string[] = ["write", "edit"];

export type StaticRule =
  | "hard-deny"
  | "allow-list"
  | "read-only-tool"
  | "in-cwd-write"
  | "out-of-cwd-write"
  | "read-only-bash"
  | "network-bash"
  | "unjudged-bash"
  | "unknown-tool";

export type StaticDecision =
  | { outcome: "run"; rule: StaticRule }
  | { outcome: "block"; rule: StaticRule; reason: string }
  | { outcome: "classify"; rule: StaticRule; reason: string };

export function decideStatically(call: ToolCall, config: AutoModeConfig, cwd: string): StaticDecision {
  const signature = callSignature(call);

  const denied = matchesAnyPattern(config.hardDeny, signature);
  if (denied !== undefined) {
    return { outcome: "block", rule: "hard-deny", reason: `denied by the configured pattern ${denied}` };
  }

  const allowed = matchesAnyPattern(config.allow, signature);
  if (allowed !== undefined) return { outcome: "run", rule: "allow-list" };

  if (readOnlyTools.includes(call.toolName)) return { outcome: "run", rule: "read-only-tool" };

  if (pathTools.includes(call.toolName)) return decidePathTool(call, cwd);

  if (call.toolName === "bash") return decideBash(call);

  return {
    outcome: "block",
    rule: "unknown-tool",
    reason:
      `auto mode has no static rule for the tool ${JSON.stringify(call.toolName)}, ` +
      `so it cannot judge this call. Known tools: ${[...readOnlyTools, ...pathTools, "bash"].join(", ")}.`,
  };
}

function decidePathTool(call: ToolCall, cwd: string): StaticDecision {
  const path = call.input.path;
  if (typeof path !== "string" || path.trim() === "") {
    return {
      outcome: "classify",
      rule: "out-of-cwd-write",
      reason: `${call.toolName} named no path, so the static rules cannot place it`,
    };
  }
  const located = locatePath(cwd, path);
  if (located.location === "in-cwd") return { outcome: "run", rule: "in-cwd-write" };
  if (located.location === "protected") {
    return {
      outcome: "classify",
      rule: "out-of-cwd-write",
      reason: `${located.resolved} is a protected path (${located.protectedBy})`,
    };
  }
  return {
    outcome: "classify",
    rule: "out-of-cwd-write",
    reason: `${located.resolved} resolves outside the working directory ${cwd}`,
  };
}

function decideBash(call: ToolCall): StaticDecision {
  const command = call.input.command;
  if (typeof command !== "string") {
    return { outcome: "classify", rule: "unjudged-bash", reason: "bash call carried no command string" };
  }
  const classification = classifyBashCommand(command);
  if (classification.kind === "read-only") return { outcome: "run", rule: "read-only-bash" };
  if (classification.kind === "network") {
    return {
      outcome: "classify",
      rule: "network-bash",
      reason: `${classification.command} reaches the network, which is never decided without a model`,
    };
  }
  return { outcome: "classify", rule: "unjudged-bash", reason: classification.reason };
}

export function callSignature(call: ToolCall): string {
  if (call.toolName === "bash") return stringInput(call, "command").trim();
  const argument = stringInput(call, "path") || stringInput(call, "pattern");
  return argument === "" ? call.toolName : `${call.toolName} ${argument}`;
}

export function normalizedIntent(call: ToolCall): string {
  return `${call.toolName} ${callSignature(call).replace(/\s+/g, " ").trim()}`;
}

export function describeCall(call: ToolCall): string {
  const signature = callSignature(call).replace(/\s+/g, " ").trim();
  return call.toolName === "bash" ? `bash: ${signature}` : signature;
}

function stringInput(call: ToolCall, key: string): string {
  const value = call.input[key];
  return typeof value === "string" ? value : "";
}
