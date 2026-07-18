# Plan: session instructions

## Goal

Add a generic command that answers a question about another attn session from
that session's conversation:

```sh
attn session instructions <target-session-id> \
  --question "Was creating, updating, and merging PR #571 authorized?"
```

The command sends the session's user and assistant messages, plus the question,
to a small model and returns a concise answer with a few exact excerpts. It does
not understand pull requests, authorization types, or workflows. Those are only
things callers may ask about.

The first implementation supports Codex transcripts and `gpt-5.6-luna`.
Claude transcripts, Haiku/Sonnet, and model selection in Settings follow
immediately after the first slice.

## Product contract

### Command

```text
attn session instructions <target-session-id> --question <text> [--json]
```

- The target session ID and a non-blank question are required.
- `--json` changes presentation only.
- There are no action, resource, workflow, transcript-path, model, or reasoning
  flags.
- The command is read-only and does not create an authorization record.

Example human output:

```text
Yes. Victor authorized merging PR #571 after the earlier prohibition.

User — 16:41
  "Yes, do it."

Assistant — 16:40
  "The PR is ready. Should I merge #571 now?"

Transcript: /Users/victora/.codex/sessions/2026/07/17/example.jsonl
```

The assistant excerpt supplies context for the user's pronoun. It is never
treated as authorization by itself.

### Result

Keep the machine result small and generic:

```go
type SessionInstructionsResult struct {
    Answer         string            `json:"answer"`
    Evidence       []EvidenceExcerpt `json:"evidence"`
    SessionID      string            `json:"session_id"`
    TranscriptPath string            `json:"transcript_path"`
    Fingerprint    string            `json:"transcript_fingerprint"`
    Model          string            `json:"model"`
    Effort         string            `json:"reasoning_effort"`
}

type EvidenceExcerpt struct {
    TurnID    string `json:"turn_id"`
    Author    string `json:"author"` // "user" or "assistant"
    Quote     string `json:"quote"`
    Timestamp string `json:"timestamp,omitempty"`
}
```

`Answer` is free text. For a yes/no question it should begin with `Yes.`, `No.`,
or `Unclear.`. `Unclear` is a successful result: the model inspected the
conversation but could not support a more definite answer.

`TranscriptPath` is the absolute path of the native transcript that attn read.
The fingerprint identifies the exact contents read from that path. `Model` and
`Effort` describe the attempt whose answer was returned, including an escalated
retry.

### Failure contract

The command has three outcomes:

1. **Answered:** the model produced an answer backed by validated excerpts.
   Exit zero.
2. **Inconclusive:** the model successfully determined that the conversation
   does not answer the question. Return `Unclear`; exit zero.
3. **Failed:** attn could not perform a trustworthy lookup. Return no answer or
   evidence; exit non-zero.

Failure must never be converted into `No` or `Unclear`. Fail closed means that
the caller receives no usable verdict, not that attn assumes the action was
unauthorized.

JSON errors use a stable code and short message:

```json
{
  "error": {
    "code": "invalid_evidence",
    "message": "The model did not return verifiable evidence"
  }
}
```

The first slice needs these codes:

| Code | Meaning |
| --- | --- |
| `session_not_found` | The target session does not exist. |
| `transcript_unavailable` | Its exact native transcript cannot be read or parsed. |
| `conversation_too_large` | The projected conversation exceeds the model input limit. |
| `model_unavailable` | The model invocation failed or timed out. |
| `invalid_response` | Both attempts returned malformed structured output. |
| `invalid_evidence` | Both attempts returned evidence that could not be resolved. |

Human mode prints the corresponding message to stderr without transcript or
question content.

## Runtime shape

```text
attn session instructions
  -> daemon resolves the target session and its native resume ID
  -> read one transcript snapshot
  -> project user and assistant conversation messages
  -> Luna low answers the question with cited turn IDs and quote hints
  -> validate citations against the snapshot
     -> valid: return the answer and exact source excerpts
     -> invalid: retry the whole request with Luna medium
        -> valid: return the replacement answer and exact source excerpts
        -> invalid: return an error with no answer
```

The implementation may live in a small `internal/sessioninstructions` package,
called from the daemon's Unix-socket command handler. It should have one main
entry point:

```go
type Request struct {
    TargetSessionID string
    Question        string
}

type Service struct {
    Store  *store.Store
    Model  ModelRunner
}

func (s *Service) Ask(ctx context.Context, req Request) (SessionInstructionsResult, error)
```

Use the existing client, daemon command, and TypeSpec generation patterns for
the CLI-to-daemon boundary. Do not introduce a provider framework, access-token
scheme, origin database, or authorization domain model for this feature.

## Conversation projection

Resolve the target through the attn session store, then use its stored native
resume ID with the agent driver's `TranscriptFinder`. Exact native-ID lookup is
required: the prototype showed that cwd/time-based discovery can select a
plausible but wrong transcript.

Read a single bounded snapshot and compute its fingerprint before parsing. For
v1, project only the native Codex records representing:

- user input;
- assistant output.

Omit system instructions, developer instructions, reasoning, tool calls, tool
results, hooks, shell output, and attn metadata. Preserve message order and
assign stable turn IDs within the snapshot.

```go
type ConversationTurn struct {
    ID        string
    Author    string // "user" or "assistant"
    Text      string
    Timestamp string
}
```

The entire projected conversation and the caller's question go to the model in
one request. V1 does not add semantic retrieval or transcript compaction beyond
removing non-conversation events. If the result is still too large, return
`conversation_too_large`.

## Model call and retry

The model returns a complete candidate answer, not trusted evidence:

```go
type ModelAnswer struct {
    Answer   string `json:"answer"`
    Evidence []struct {
        TurnID string `json:"turn_id"`
        Quote  string `json:"quote"`
    } `json:"evidence"`
}
```

The first attempt uses `gpt-5.6-luna` with low reasoning. If its structured
output is malformed, evidence is missing, or any required excerpt cannot be
resolved, make one repair attempt with `gpt-5.6-luna` at medium reasoning.

The repair attempt receives:

- the same transcript snapshot and question;
- the validation errors from the first attempt;
- an instruction to produce a complete replacement answer with verifiable
  excerpts.

Validate the replacement from scratch. Never combine the first answer with the
second attempt's evidence, or mix citations between attempts. If the retry also
fails, return `invalid_response` or `invalid_evidence` with no answer.

A timeout or provider failure returns `model_unavailable`; escalating reasoning
does not repair unavailable infrastructure.

When Claude support lands, use the same policy with Haiku as the primary model
and Sonnet as the repair model. The subsequent Settings work should configure a
primary/retry pair per provider rather than changing this command's API.

## Evidence validation

Model excerpts are locator hints. They are never displayed directly.

For each cited item:

1. Find the cited turn ID in the projected conversation.
2. Locate the hint within that turn, first exactly and then with harmless
   normalization for whitespace and typographic punctuation.
3. Require one unique match.
4. Copy the displayed quote from the original transcript text.

An assistant turn may clarify a reference such as "yes, do it," but an answer
about what the user allowed must include supporting user-authored evidence.
Generated interpretation remains the answer; exact transcript text remains the
evidence.

If an item cannot be resolved, the entire candidate is invalid and triggers the
single repair attempt. Returning only the remaining excerpts could leave a
claim without evidence for one of its clauses.

## Minimal code-reading path

An implementer should read these locations in order:

1. `cmd/attn/main.go` — command parsing and human/JSON rendering conventions.
2. `internal/client/client.go` and the existing Unix-socket command handlers —
   the shortest path from CLI to daemon.
3. `internal/protocol/schema/main.tsp` and
   `internal/protocol/constants.go` — wire shape and protocol versioning.
4. `internal/store/store.go` — target session and stored native resume ID.
5. `internal/agent/driver.go` and `internal/agent/codex.go` — exact transcript
   discovery through `TranscriptFinder`.
6. `internal/transcript` and `cmd/session-evidence-prototype/main.go` — existing
   transcript parsing and the proven projection/model/quote flow.
7. `internal/classifier/classifier.go` — existing headless Codex invocation and
   reasoning-effort configuration conventions.

This is enough to implement the feature. The broader design document is useful
background but is not an implementation checklist.

## Tests

Keep the test suite focused on the boundaries that can make the result wrong:

1. **CLI contract:** required arguments, human output, JSON output, exit zero for
   `Unclear`, and non-zero for every execution error.
2. **Projection fixtures:** real Codex JSONL containing user messages,
   assistant messages, reasoning, tools, hooks, and system records. Assert that
   only ordered user/assistant conversation reaches the model.
3. **Exact transcript resolution:** a regression fixture with multiple plausible
   transcripts proves that the stored native resume ID selects the right one
   and returns its absolute path.
4. **Evidence validation:** exact match, normalized whitespace/quotes, missing
   turn, altered quote, ambiguous quote, and assistant-only support for a claim
   about user instructions.
5. **Retry behavior:** a fake model returns invalid evidence on attempt one and
   valid evidence on attempt two. Assert low then medium effort, identical
   snapshot/question, validation feedback, and that only attempt two is
   returned. Also assert that two invalid attempts produce no answer.
6. **Contextual references:** fixtures such as an assistant asking whether to
   merge followed by the user saying "yes, do it," plus later revocation and
   conflicting-instruction cases.
7. **Failure behavior:** missing transcript, oversized projection, timeout,
   malformed output, and invalid evidence remain distinguishable from a valid
   `Unclear` answer.
8. **Real transcript smoke test:** run the production path against the transcript
   used by the prototype and confirm an answer with byte-for-byte source quotes.

The model runner is fake in deterministic tests; the real-model smoke test is
explicit because model behavior cannot be proven by unit tests.

## Implementation order

- [ ] Add the CLI and generated daemon command/result shapes.
- [ ] Resolve the exact Codex transcript and project conversation turns.
- [ ] Add the Luna low call and structured response parsing.
- [ ] Validate hints and return exact transcript excerpts.
- [ ] Add the Luna medium repair attempt and failure mapping.
- [ ] Add focused unit, integration, and real-transcript smoke tests.
- [ ] Exercise the command through a non-production attn profile.

## Immediate follow-up

Add Claude transcript projection, Haiku primary/Sonnet repair calls, and Settings
for selecting the primary and repair model pair. The command and result shapes
remain unchanged.
