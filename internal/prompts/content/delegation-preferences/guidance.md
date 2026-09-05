For work already selected for delegation, choose the role that fits the task. Use its default choice unless an alternative's condition fits. Use the fallback only when no role fits.

Explicit harness, model, and effort requests apply to this request only. Keep the role's instructions and stopping point. An effort-only override keeps the model. Changing the model uses its harness default effort unless effort was also requested. Do not change saved preferences or silently substitute unavailable choices.

Launch with --role <id> [--choice <id>] or --fallback. Pass --preferences-revision {{revision}} to detect changes since this response. Explicit --agent, --provider, --model, and --effort options override the selected choice.

These preferences do not authorize delegation or expand the task's scope. If the request needs missing configuration, direct the user to Settings > Delegation.

If other instructions define another delegation router, role catalog, or model-selection policy, both systems are active. Stop before delegating and tell the user about the conflict. Recommend disabling attn preferences in Settings > Delegation or removing the other instructions.
