# OpenCode prose stop classification

## Why

OpenCode already reports native questions and permissions accurately, but an
assistant can still ask for direction in ordinary prose and leave attn showing
the session as done. Classify only those explicit idle turns without weakening
the native signals or letting slow model judgment overwrite newer activity.

## Alignment

- Native permissions and questions remain authoritative and are reconciled
  before semantic classification starts.
- The newest completed assistant text is normalized at the OpenCode HTTP
  boundary; synthetic, ignored, reasoning, tool, and file parts are excluded.
- Classification runs in a temporary session on the same authenticated server,
  with every discovered tool disabled and deletion required on every exit path.
- Idle work reserves its attn sequence before fetching prose. New busy,
  question, permission, error, close, or monitor-abort signals cancel it and
  reserve newer sequences normally.
- Verdicts are cached by native message ID plus exact text hash in a migrated
  registry. Duplicate idle events reuse judgment but still send a fresh report.
- Uncertain extraction, model, timeout, parse, or cleanup outcomes report
  `unknown`; they never guess that user input is required.

## Execution

- [x] Add normalized message, tool, synchronous prompt, and delete APIs.
- [x] Implement completed-assistant extraction and the isolated strict classifier.
- [x] Migrate the run registry and persist per-turn verdict caches.
- [x] Add reserved reporting plus per-run classifier cancellation and generation state.
- [x] Preserve native-attention priority and cover races, caching, and run isolation.
- [x] Evaluate representative prose fixtures and record false results here.
- [x] Update plugin docs and changelog, then enable the advertised capability.
- [x] Run automated checks and required non-prod live-app scenarios.
- [ ] Open the PR, address Figgyster review, and merge only after approval.

## Evaluation

The permanent fixture set is
`plugins/attn-opencode/test/fixtures/stop-classifier-evaluation.json`. The five
semantic cases were run against the real OpenCode 1.17.18 server and its selected
model with every discovered tool disabled:

| Case | Expected | Observed |
| --- | --- | --- |
| Direct question | `waiting_input` | `waiting_input` |
| Confirmation request | `waiting_input` | `waiting_input` |
| Completed summary containing a rhetorical question | `idle` | `idle` |
| Completed “let me know” closing | `idle` | `idle` |
| Clean completion | `idle` | `idle` |

False negatives: **0/2**. False positives: **0/3**. The native-question fixture
is deliberately routed around the semantic classifier; driver coverage verifies
that its pending request remains authoritative.

The final non-production live-app scenario reused one linked native session. A
prose-only “Should I continue?” turn moved attn to `waiting_input`; the following
completed turn moved it to `idle`. The linked OpenCode TUI remained selected
throughout, and no temporary classifier session remained afterward.
