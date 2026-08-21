// The denial contract: the text a blocked tool call hands back to the model.
// It is the whole model-facing API of auto mode, so it says four things and
// nothing else — that auto mode blocked this, what was blocked, why, and what
// unblocks it. That fourth one has two answers, and handing over the wrong one
// costs the user a turn: a refusal is lifted by their approval, while a call
// nobody could judge is lifted by retrying and by nothing they can say.

export type Denial = {
  /** The call, one line (see policy.ts's describeCall). */
  action: string;
  /** Why it was refused, in the reader's terms. */
  reason: string;
  /**
   * False when nothing judged this call: no classifier could be reached, or
   * the one that answered said something unreadable. The block stands either
   * way, but the way through is the opposite one — a retry, not the user's
   * approval, which would only send the call back to the same classifier.
   */
  judged?: boolean;
};

export function denialToolResult(denial: Denial): string {
  if (denial.judged === false) return unjudgedToolResult(denial);
  return [
    "attn auto mode blocked this tool call.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Auto mode runs work inside this session's working directory and refuses",
    "what reaches past it. Nothing about this session has stopped: say what",
    "you wanted to do and why in your reply, and ask. The user's explicit",
    "approval in the conversation lets you retry the same call. Do not work",
    "around the block by another route.",
  ].join("\n");
}

/**
 * An outage wearing a refusal's words sends the agent to apologize to the user
 * for something nobody objected to, and the approval it asks for cannot help:
 * approval re-runs the classification, against the endpoint that is still
 * down.
 */
function unjudgedToolResult(denial: Denial): string {
  return [
    "attn auto mode could not judge this tool call, so it blocked it.",
    "",
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
    "Nothing refused this action and no boundary was crossed: auto mode asks a",
    "model before letting a call like this through, and no answer came back.",
    "Retrying is what gets through. The user's approval is not what unblocks",
    "this one — the retry would reach the same classifier. If it keeps",
    "failing, say so in your reply so the user knows the judge is down.",
  ].join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}
