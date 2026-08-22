export type Denial = {
  action: string;
  reason: string;

  judged?: boolean;

  clearable?: boolean;
};

export function denialToolResult(denial: Denial): string {
  if (denial.judged === false) return unjudgedToolResult(denial);
  if (denial.clearable === false) return settledToolResult(denial);
  return [
    "attn auto mode blocked this tool call.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Auto mode runs work inside this session's working directory and refuses",
    "what reaches past it. The session has not stopped. Say in your reply what",
    "you wanted to do and why, then ask. The user's explicit approval in the",
    "conversation lets you retry the same call. Do not work around the block",
    "by another route.",
  ].join("\n");
}

function settledToolResult(denial: Denial): string {
  return [
    "attn auto mode blocked this tool call, and approving it in the",
    "conversation will not lift it.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Do not ask the user to approve this one. Approval does not move it, and",
    "neither does retrying. What lifts it is a change to auto mode's own setup,",
    "which the user makes outside this session. Say plainly in your reply what",
    "you were trying to do and that auto mode refuses it, then carry on with",
    "the work that does not need it. Do not work around the block by another",
    "route.",
  ].join("\n");
}

function unjudgedToolResult(denial: Denial): string {
  if (denial.clearable === false) return unshowableToolResult(denial);
  return [
    "attn auto mode could not judge this tool call, so it blocked it.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Nothing refused this action and no limit was crossed. Auto mode asks a",
    "model before letting a call like this through, and no answer came back.",
    "Retrying is what gets through. The user's approval will not unblock this",
    "one, because the retry reaches the same classifier. If it keeps failing,",
    "say so in your reply so the user knows the judge is down.",
  ].join("\n");
}

function unshowableToolResult(denial: Denial): string {
  return [
    "attn auto mode could not judge this tool call, so it blocked it.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Nothing refused this action and no limit was crossed. Auto mode never",
    "shows its classifier part of a conversation, and this one does not fit,",
    "so no model was asked. Retrying changes nothing and the user's approval",
    "does not reach this. Say in your reply what you were trying to do and ask",
    "the user for it directly.",
  ].join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}
