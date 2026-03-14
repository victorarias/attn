# PR Review Automation

This repository uses [Hodor](https://github.com/mr-karan/hodor) for advisory pull-request reviews on GitHub.

## Workflow

`.github/workflows/pr-review.yml`:

- runs on non-draft pull requests
- skips fork PRs so privileged review credentials are not exposed
- checks out the repository with full history so base-branch diffs are available
- clones Hodor `v0.3.4`
- applies `.github/hodor/v0.3.4-openrouter.patch`
- writes `~/.pi/agent/models.json` so OpenRouter provider overrides remain available for workflow tuning
- installs `pnpm` plus the checked-in frontend dependencies under `app/` so Hodor can run repo-native frontend checks without burning turns on missing tooling
- runs Hodor on OpenRouter with `qwen/qwen3-coder-next`
- posts the review back to the PR as an advisory review comment

## Required GitHub secrets

- `open_router_api_key`

## Why the patch exists

Upstream Hodor `v0.3.4` does not yet parse `openrouter/...` model strings during preflight setup and does not read `OPENROUTER_API_KEY` during provider-specific API key resolution. The local patch adds the minimal OpenRouter model parsing and API-key handling needed for OpenRouter-backed reviews.

## Repository-specific review guidance

Hodor loads review guidance from:

- `.hodor/skills/attn-review/SKILL.md`

That skill tells Hodor to focus on protocol safety, generated files, session-state behavior, review-comment consumers, and daemon/app compatibility risks.

## Notes

- The workflow is advisory.
- The workflow currently targets `qwen/qwen3-coder-next` on OpenRouter.
- The workflow uses `--reasoning-effort medium` to keep review latency and token usage under control for small PRs.
- The first PR run after any workflow changes is the real end-to-end validation.
