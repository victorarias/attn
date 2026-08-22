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
    "What gets through: the user approving this in the conversation. Tell them",
    "what you were trying to do and why, and ask them to approve it. Their",
    "approval lets you run the same call again. A retry on its own is judged",
    "the same way and blocks again. Your turn has not ended. Do not reach the",
    "same end by another route.",
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
    "What gets through: nothing you can do from here. Approval does not move",
    "it and neither does retrying. Only a change to auto mode's own setup",
    "lifts it, and the user makes that outside this session. Tell them what",
    "you were trying to do and that auto mode refuses it, then carry on with",
    "the work that does not need it. Do not reach the same end by another",
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
    "What gets through: making the same call again. Do not ask the user to",
    "approve it; approval changes nothing, because the retry reaches the same",
    "model either way. If it keeps failing, tell the user their classifier",
    "model looks to be down.",
  ].join("\n");
}

function unshowableToolResult(denial: Denial): string {
  return [
    "attn auto mode could not judge this tool call, so it blocked it.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Nothing refused this action. The classifier's model would not take a",
    "conversation this size, so it never saw the call. The user was already",
    "asked about it directly and said no. What gets through: nothing here.",
    "Retrying reaches the same limit and there is no approval left to ask for.",
    "Tell the user what you were trying to do, then carry on with the work",
    "that does not need it.",
  ].join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}
