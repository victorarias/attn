# PR Review Automation

This repository uses [shitty-reviewing-agent](https://github.com/victorarias/shitty-reviewing-agent) for advisory pull-request reviews on GitHub.

## Workflow

`.github/workflows/pr-review.yml`:

- runs on non-draft pull requests
- skips fork PRs so privileged review credentials are not exposed
- runs `victorarias/shitty-reviewing-agent@main` against OpenRouter with `minimax/minimax-m2.7`
- posts the review back to the PR

## Required GitHub secrets

- `OPEN_ROUTER_API_KEY`

## Notes

- The workflow is advisory.
- Reasoning effort is set to `xhigh` and `max-files` to 100.
- The first PR run after any workflow changes is the real end-to-end validation.
