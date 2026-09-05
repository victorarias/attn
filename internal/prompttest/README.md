# Prompt compatibility fixtures

The JSON files in `testdata` contain output captured from the prompt builders at
`1c82650bb58b2a1b33911998b68a8c8bebabfe56`, before the catalog migration. The
`TestLegacyPromptCompatibility` cases were run against an archive of that revision;
a temporary capture helper wrote their results. The catalog renderer did not
produce these expectations.

The `daemon.json` and `seed-guide.json` fixtures were captured again from
`4c127f85504e82028299baa7e9ac74aef7773151` to cover the coalesced inbox notification,
batch output and Garden update content. The other Go builders are unchanged
between these two revisions.

Tests compare exact bytes, including whitespace, optional sections, Unicode and
literal template-looking input. Agent fixtures also retain fresh/resume launch
arguments for Claude, Codex and Copilot, installed skill documents, and headless
system/user channel handling. Fixtures live here so packages use one comparison
helper without maintaining copies of the old builders.

Seed-authoring expectations include the revised body/plot guidance and instructions
to read referenced context before delegated work. These expectations were edited
at the source-text boundaries. Scenario inputs, surrounding launch arguments and
unrelated output retain the captured baseline.

The frontend's `src/prompts/testdata` and Pi's `test/testdata/prompts.json` were
captured from the original TypeScript builders at
`f68ab3f02f329f46d3b9b3d1e7603f545c27dd50`; those builders are unchanged at the Go
baseline revision. Generated catalog
parity hashes serve a separate purpose: they check that Go and TypeScript render
the same declaration, rather than proving compatibility with the old output.

Pi's rulebook expectations also include the sandbox and build-cache wording from
`385cb4fda`, edited at the changed source-text boundaries. Its
`test/testdata/security-prompts.json` captures that revision's security guidance
and denial builders before moving them into the catalog, across enabled/disabled
sandboxes, network policies, cache grants and reviewer availability.

There is no update switch. An intentional prompt change should update the
relevant expectation in the same review, with its wording change explained.
Do not regenerate compatibility fixtures from the new renderer to make a failure
pass. The app scenarios verify delivery and lifecycle behavior separately.

Delegation discovery expectations use the short launch hint and on-demand skill
reference. The launch hint also stops agents when attn preferences overlap with
another configured delegation system. The always-present Garden command primer
was removed. These edits replace only the corresponding source text in the
captured expectations.
