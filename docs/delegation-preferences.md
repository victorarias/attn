# Delegation preferences

Settings > Delegation is an optional, daemon-local configuration for choosing how
to carry out authorized delegations. It starts off. Turning it off retains saved
roles and stops making them available to new role-based requests. Direct
`attn delegate` commands and custom routing skills keep their existing behavior.

Start with the editable Scout, Design, Build, Ship, and Review roles, or create
custom roles. Each role separates when to choose it from the instructions and
stopping point for the delegated agent. A default choice specifies a harness,
provider, model, and effort; alternatives add conditions such as ambiguous
requirements or difficult verification. The fallback applies only to unmatched
work. Blank model or effort fields use the harness default.

Discovery is explicit and does not send a model prompt:

- Claude uses the stream-JSON initialization response, the same source as the
  Agent SDK's `supportedModels()`.
- Codex uses app-server `model/list`, including pagination and supported effort
  levels.
- Pi queries its live model registry, including extension providers, using its
  existing offline RPC discovery path.
- Other harnesses may expose the plugin `model_discovery` capability and
  `driver.models` RPC. Without discovery, use exact native identifiers or the
  harness default where supported. Copilot currently uses its own selected model.

Catalog membership does not prove account access. Missing effort metadata stays
unknown; known unsupported choices are rejected when launching. Refresh models
after changing accounts, providers, or harness configuration. Discovery runs only
on request and deduplicates concurrent queries; there is no background polling.

Agents receive a short capability hint at launch. After deciding to delegate,
they read the skill's delegation reference and call `attn delegate roles`. That
single response contains the active roles, all choices, fallback, and current
revision. It contains no roles while preferences are off. Incomplete roles,
choices, and fallbacks remain saved but do not appear in agent discovery.
Configuration failures are errors, not empty catalogs.

If that response is nonempty while another instruction set defines its own
delegation router, role catalog, or model policy, the agent stops before
delegating. It tells the user both systems are active and recommends disabling
attn preferences or removing the other instructions.

Use `--role <id> [--choice <id>]` or `--fallback` when launching, and pass
`--preferences-revision <revision>` to reject stale choices. Explicit model and
effort flags affect only that request. An effort-only override preserves the
model; changing model clears inherited effort unless effort is also explicit.
Changing harness clears the inherited provider, model, and effort. The selected
role's instructions and stopping point remain in force within the task's scope.

Accepted operations persist their resolved selection and guidance. Retrying an
accepted request therefore uses the original choice, even after settings change.
The delegated agent receives the task brief and selected guidance, not the full
routing catalog. These preferences never authorize delegation or expand scope.
