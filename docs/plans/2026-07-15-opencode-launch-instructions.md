# OpenCode launch instructions

## Why

OpenCode sessions launched through a plugin driver currently bypass attn's built-in
workspace, workflow, ticket, and chief guidance. Transport one attn-owned bundle
through the generic driver contract and let the OpenCode plugin install it as a
private per-run instruction file.

## Alignment

- Core composes the exact existing `hooks.AgentInstructions` or
  `hooks.ChiefGuidance` text; plugins do not reinterpret policy.
- Ordinary sessions get an explicit pre-persistence workspace checkout. A failed
  launch removes only a checkout created for that attempt.
- OpenCode loads a private per-run system-transform plugin through
  `OPENCODE_CONFIG_CONTENT`, preserving inherited config and rereading the
  instruction file for every prompt, including resumed conversations.
- Chief promotion and demotion reload only plugin drivers advertising both
  `launch_instructions` and `resume`. The daemon reconstructs the plugin command
  before killing the live worker.
- Resume always recomposes current guidance, so neither workspace revisions nor
  chief role are frozen in persisted metadata.
- Live refresh during an already-running turn and a reusable SDK file helper are
  deferred.

## Execution

- [x] Add the generic capability and instruction-bundle wire contract; bump plugin API.
- [x] Prepare workspace/chief instructions before plugin spawn, with safe checkout rollback.
- [x] Store private OpenCode instruction files and merge them into launch config.
- [x] Support capability-driven plugin chief reload while preserving launch metadata and flags.
- [x] Add focused daemon, registry, launcher, and compatibility tests.
- [x] Update user-facing and plugin-driver documentation.
- [x] Run automated checks and the required non-prod live-app scenarios.
- [ ] Open the PR, address Figgyster review, and merge only after approval.
