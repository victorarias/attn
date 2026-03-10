# PR Review Automation

This repository uses [Hodor](https://github.com/mr-karan/hodor) for advisory pull-request reviews on GitHub.

## Workflow

`.github/workflows/pr-review.yml`:

- runs on non-draft pull requests
- skips fork PRs so privileged Google Cloud credentials are not exposed
- checks out the repository with full history so base-branch diffs are available
- clones Hodor `v0.3.4`
- applies `.github/hodor/v0.3.4-google-vertex.patch`
- authenticates with `VERTEX_AI_SA`
- runs Hodor on Vertex AI with `google-vertex/gemini-3-flash-preview`
- posts the review back to the PR as an advisory review comment

## Required GitHub secrets

- `VERTEX_AI_SA`
- `GOOGLE_CLOUD_PROJECT`

The workflow uses `GOOGLE_CLOUD_LOCATION=global`.

## Why the patch exists

Upstream Hodor `v0.3.4` does not yet parse `google/...` and `google-vertex/...` model strings during preflight setup. The local patch adds the minimal Google/Vertex model handling needed for Vertex-backed Gemini reviews.

## Repository-specific review guidance

Hodor loads review guidance from:

- `.hodor/skills/attn-review/SKILL.md`

That skill tells Hodor to focus on protocol safety, generated files, session-state behavior, review-comment consumers, and daemon/app compatibility risks.

## Notes

- The workflow is advisory.
- The first PR run after any workflow changes is the real end-to-end validation.
