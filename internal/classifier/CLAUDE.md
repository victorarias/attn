# Classifier Policy

This policy applies to stop-time assistant-message classification in this module.

## Scope

- In scope: deciding post-turn assistant state from assistant text (`idle`, `waiting_input`, `unknown`).
- Out of scope: runtime/hook-driven state transitions such as `working` and `pending_approval`.

## Requirements (In Scope)

- Do not add deterministic state classification using hard-coded string matching lists.
- Do not add regex or keyword heuristics that map assistant text directly to `idle`, `waiting_input`, or `unknown`.
- Classifier outcomes must come from LLM outputs (Claude SDK, Copilot CLI, Codex CLI) and parser logic for those LLM responses.

## Notes

- Normalization/parsing of structured LLM outputs is allowed.
- Retry/backoff and transport error handling are allowed.
- When LLM output is missing/invalid, return `unknown` rather than substituting heuristic rules.
- Hook/runtime transitions are allowed to remain deterministic:
  - `PermissionRequest` -> `pending_approval`
  - `UserPromptSubmit` / `PostToolUse` -> `working`
