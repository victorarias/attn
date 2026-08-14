# Session cost in the agent header

## Outcome

Every agent header shows the USD cost of token usage observed for that attn
session. A priced session updates live, an observed model without a price says
`unknown`, and a session with no observed usage shows nothing.

## Decisions

- Extract usage in the existing transcript watcher. It already follows the exact
  transcript bound to the live agent conversation and sees appended records as
  they arrive; a periodic full-file sweep would duplicate identity, cursor, and
  idle-work machinery.
- Start accounting from this install forward. On first attachment, seed a durable
  usage cursor at the transcript head rather than backfilling old transcripts.
  New sessions mark accounting active before discovery so their first transcript
  records count. Sessions that resume provider history seed at its current head.
  After daemon restart, resume the exact cursor; if a rewritten or replacement
  transcript invalidates it, seed at the new head because replay could charge
  history twice.
- Persist a per-session, per-model token ledger plus its transcript cursor. The
  ledger keeps input, output, cache-write, and cache-read tokens separate so a
  settings correction can reprice all observed usage without rewriting history.
- Price and display in USD. Round normal costs to the nearest cent (`$1.23`) and
  show a positive sub-cent charge as `<$0.01`, so real spend never reads as free.
- Resolve prices by exact provider model id. The built-in table is the default;
  `session_cost.price.<model-id>` settings contain a complete JSON rate card in
  USD per million tokens and replace or fill that exact model's entry.
- If any observed model lacks a complete valid rate card, the whole session cost
  is unknown. A partial total would look authoritative while omitting spend.

## Shape

1. Extend transcript decoding with provider-specific usage records, covered by
   redacted real Claude Code and Codex transcript excerpts.
2. Add a small session-cost package for the token ledger, built-in rate table,
   settings overrides, and integer-safe pricing arithmetic.
3. Add durable session ledger/cursor storage and restart coverage.
4. Feed watcher deltas into the ledger, then publish the existing session snapshot
   fact only when usage changes.
5. Add optional `cost_usd` / `cost_unknown` fields to `Session`, regenerate both
   protocol clients, and bump every protocol-version lockstep constant.
6. Render the three header states and add settings controls for exact-model rate
   overrides.

## Verification

- Go fixtures: Claude and Codex extraction, cache-rate math, exact-model override,
  unknown model, cursor deduplication, and store reopen.
- Frontend: priced, unknown, and absent headers; override editing.
- `make test-quick`, the frontend suite, and `make build-app`.
- A fresh throwaway profile: one short priced session checked against transcript
  arithmetic, then a seeded unpriced-model ledger checked through daemon restart
  and the same wire/header path.

## Deliberate boundary

No budgets, alerts, member/day aggregation, image/audio pricing, or crew-lifecycle
input. Shells and truly transcript-less harnesses remain blank. A transcript-
bearing harness that produces an assistant turn but exposes no usage is explicitly
unknown, never zero.
