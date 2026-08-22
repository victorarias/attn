export function autoModeSystemPromptAddendum(): string {
  return [
    "## attn auto mode",
    "",
    "Auto mode is on for this session. Work inside the working directory runs",
    "without asking. Anything reaching past that is judged before it runs and",
    "may be refused: writes outside the directory, network calls, destructive",
    "or irreversible commands.",
    "",
    "A refusal does not end the turn and is not an error to route around. Say",
    "in your reply what you wanted to do and why, then ask. The user's explicit",
    "approval in the conversation is what permits a retry. There is no dialog",
    "to click and no rules file to edit. Never reach the same end by another",
    "route after a refusal.",
  ].join("\n");
}
