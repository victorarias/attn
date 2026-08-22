export type Denial = {
  action: string;
  reason: string;

  judged?: boolean;

  clearable?: boolean;
};

// Claude Code's own denial result, adapted: attn's name, attn's remedy, and one
// sentence for the case CC does not have, where nothing judged the call at all.
export function denialToolResult(denial: Denial): string {
  const lines = [
    "Permission for this action was denied by the attn auto mode classifier.",
    // The action rides here in full. The widget beside it is clamped for a
    // person, and the model must not be judging a truncated command.
    `Blocked: ${oneLine(denial.action)}`,
    `Reason: ${oneLine(denial.reason)}`,
    "",
  ];
  if (denial.judged === false) {
    lines.push(
      "Nothing judged this call and nothing refused the action: auto mode could " +
        "not put it in front of a classifier, and it fails closed. This is an " +
        "outage, not a verdict.",
      "",
    );
  }
  lines.push(
    "If you have other tasks that don't depend on this action, continue working " +
      "on those. IMPORTANT: You *may* attempt to accomplish this action using " +
      "other tools that might naturally be used to accomplish this goal, e.g. " +
      "using head instead of cat. But you *should not* attempt to work around " +
      "this denial in malicious ways, e.g. do not use your ability to run tests " +
      "to execute non-test actions. You should only try to work around this " +
      "restriction in reasonable ways that do not attempt to bypass the intent " +
      "behind this denial. If you believe this capability is essential to " +
      "complete the user's request, STOP and explain to the user what you were " +
      "trying to do and why you need this permission. Let the user decide how to " +
      "proceed.",
  );
  if (denial.clearable === false) {
    lines.push(
      "",
      "This one is not lifted by the user approving it in the conversation. To " +
        "allow this type of action in the future, the user changes auto mode's " +
        "own setup: an allow pattern, or the rule that refused it.",
    );
  } else {
    lines.push(
      "",
      "To allow this type of action in the future, the user can add an allow " +
        "pattern in auto mode's settings.",
    );
  }
  return lines.join("\n");
}

function oneLine(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  return collapsed === "" ? "(not stated)" : collapsed;
}
