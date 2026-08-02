# Making a release

attn's changelog is compiled, not accreted. PRs never edit `CHANGELOG.md`;
each PR ships a small **changelog fragment** — a raw statement of what changed
— and a release-time compilation step turns the accumulated fragments into one
curated, user-facing `CHANGELOG.md` section. This removes the merge conflicts
that concurrent branches used to hit on `CHANGELOG.md`, and it means the
changelog is written once per release by a writer that saw the whole release,
instead of thirty times by authors who each saw one PR.

## Per PR: add a changelog fragment

Every PR adds one YAML file under `changelog.d/`. Name it
`<branch>-<short-slug>.yaml` — uniqueness across in-flight branches is what
keeps fragments conflict-free.

```yaml
# changelog.d/amber-manatee-handover.yaml
kind: fixed            # added | changed | fixed | removed | internal
area: queue            # the subsystem touched, free-form
change: >
  Closing a turn now hands over the next agent that owes one regardless of
  how the turn closed, not only via Cmd+Shift+E.
symptom: >             # optional — for fixes, what the user noticed before
  Auto-settle finishing on the agent you were watching left you sitting on a
  done agent instead of moving you to the next one.
notes: >               # optional — extra context for the release writer
  Same root cause as the sidebar-row settle path; both fixed here.
```

A fragment is **evidence for the release writer, not final copy**. State
plainly what changed and what a user would notice; do not attempt release-note
voice. The writer merges related fragments, sets the tone, and decides what
makes the cut.

Rules:

- `kind`, `area`, and `change` are required. `kind` maps to the changelog
  category (`added` → "### Added", etc.).
- A change with no user-visible behavior still ships a fragment, with
  `kind: internal` and a one-line `change`. Internal fragments give the
  release writer the full picture; they never become bullets.
- `go run ./cmd/changelog-check` validates fragments locally; the same check
  runs in CI.

## The CI gate

The `Changelog` job in CI fails any PR that neither adds a
`changelog.d/*.yaml` fragment nor modifies `CHANGELOG.md`. Touching
`CHANGELOG.md` directly is the escape hatch for the compilation PR itself and
for hand-fixes to existing copy. Branches named `release/*` (the version-bump
PR that `scripts/release.sh` opens) are exempt. Run the gate locally with
`./scripts/changelog-gate.sh main`.

## At release: compile the changelog

When preparing a release, on a fresh branch off `main`:

```bash
./scripts/compile-changelog.sh            # or --dry-run to inspect the prompt
```

The script validates the pending fragments, has `claude` write a dated
`CHANGELOG.md` section from them (merging related entries, enforcing the
user-facing voice, dropping noise), inserts it at the top of the file, and
deletes the consumed fragments. Everything is left staged: **review the
section** — the writer drafts, you edit — fix copy where it misses, commit,
and open a PR. That PR passes the gate because it modifies `CHANGELOG.md`.

If every pending fragment is `internal`, the script removes the fragments
without adding a section.

## Cut the release

With the compilation PR merged and `main` clean of pending fragments:

```bash
./scripts/release.sh v0.9.5
```

`release.sh` refuses to run while `changelog.d/` still has pending fragments.
It bumps versions, opens a `release/<tag>` PR, waits for CI, merges, tags the
merge commit, and the tag triggers `.github/workflows/release.yml`.

## What's New modal

The in-app What's New modal (`app/src/components/WhatsNewModal.tsx`,
`WHATS_NEW_ID` in `app/src/hooks/useWhatsNew.ts`) stays hand-written — it
tells a story per milestone, not per release. When updating it, draw from the
compiled changelog sections since the last `WHATS_NEW_ID` bump.
