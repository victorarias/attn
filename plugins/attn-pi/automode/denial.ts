// The denial contract: the text a blocked tool call hands back to the model.
// It is the whole model-facing API of auto mode, so it says four things and
// nothing else — that auto mode blocked this, what was blocked, why, and
// that the user's approval in the conversation is what unblocks it.

export type Denial = {
  /** The call, one line (see policy.ts's describeCall). */
  action: string;
  /** Why it was refused, in the reader's terms. */
  reason: string;
};

export function denialToolResult(denial: Denial): string {
  return [
    "attn auto mode blocked this tool call.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Auto mode runs work inside this session's safety envelope and refuses",
    "what reaches past it. Nothing about this session has stopped: say what",
    "you wanted to do and why in your reply, and ask. The user's explicit",
    "approval in the conversation lets you retry the same call. Do not work",
    "around the block by another route.",
  ].join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}
