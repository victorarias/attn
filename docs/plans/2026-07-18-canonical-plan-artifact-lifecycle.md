# Canonical plan artifact lifecycle

## Why / Alignment

Delegated plans currently start in a worktree because repository planning guidance
puts them in `docs/plans`, then `ticket attach` copies them into the Notebook and
declares the copy canonical. That creates two mutable-looking files with no stale-copy
detection.

The accepted contract makes authority contextual and explicit:

- Explicit user or repository guidance wins.
- In a monorepo, inspect the convention at the affected component scope rather than
  treating an unrelated sibling's docs as precedent.
- When that scope keeps plans or designs in Git, the committed repository file is
  canonical. The ticket carries a small Notebook reference with repository path,
  branch, and introducing commit.
- Otherwise, attachment promotes the plan into the Notebook. After verifying the
  copy, attn retires an untracked staging file so only the Notebook artifact remains.
- A tracked source is never deleted implicitly.

Generic ticket attachments remain snapshots copied into the Notebook. The new
lifecycle applies specifically to durable Markdown plans and designs.

## Architecture

Add `attn ticket attach-plan --file <path>` as a CLI policy layer over the existing
ticket attachment protocol. The daemon continues to own atomic copying, ticket
activity, artifact enumeration, and notifications.

The CLI resolves one of two outcomes:

1. **Repository authority**: detect a tracked plan/design convention within
   `--scope` (or the repository root), require the source to be committed and clean,
   generate a temporary Markdown reference, and attach that reference.
2. **Notebook authority**: attach the source itself, verify the returned artifact is
   byte-identical, then remove the source only when it is untracked or outside Git.

`--authority repository|notebook` provides an explicit override. `auto` is the
default. `--scope` identifies the affected monorepo component.

Repository reference cards carry machine-readable frontmatter plus a readable body:

- artifact kind and repository authority;
- remote identity when available and local repository root;
- component scope and repository-relative canonical path;
- branch and the commit that introduced the file.

When adopting repository authority for a ticket created under the old lifecycle,
the command replaces a byte-identical Notebook copy with the reference and records
the retirement on the ticket. A divergent Notebook copy is left intact and requires
explicit reconciliation; attn never guesses which divergent content should win.

## Steps

- [x] Add the attach-plan parser, convention detector, reference renderer, and safe
      Notebook promotion path.
- [x] Expose the command in ticket help and delegated-agent guidance.
- [x] Teach chief guidance to distinguish repository references from Notebook-owned
      artifacts in follow-on briefs.
- [x] Cover auto-detection, monorepo scope, committed-source validation, reference
      rendering, and safe retirement with focused tests.
- [x] Run focused Go tests and exercise both outcomes against a non-production attn
      profile.
- [x] Add the user-visible lifecycle to the changelog.
