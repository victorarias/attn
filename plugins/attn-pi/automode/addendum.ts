export function autoModeSystemPromptAddendum(): string {
  return [
    "## attn auto mode",
    "",
    "Auto mode is on for this session. Every tool call is checked before it",
    "runs. Work inside the working directory runs normally and you will not",
    "notice the check. A call that reaches past it can come back blocked",
    "instead of running: writes outside the directory, network calls,",
    "destructive or irreversible commands.",
    "",
    "A block arrives as that tool call's result. It names what was blocked and",
    "why, and it says what gets through, which is one of three things: the",
    "user approving it in the conversation, a plain retry, or nothing at all.",
    "Read that line and do what it says. When approval is the way, tell the",
    "user what you were trying to do and why, and ask them for it. When it",
    "says approval will not help, do not ask for it.",
    "",
    "A block is not an error and does not end your turn. There is no dialog to",
    "click and no rules file to edit. Never reach the same end by another",
    "route after a block.",
  ].join("\n");
}
