# Live terminal annotation refresh

Status: implemented and verified.

## Goal

Let the user annotate completed assistant prose while an agent remains working.
One daemon-owned live-transcript module must own exact transcript identity,
incremental reading, provider normalization, the bounded assistant window, and
its lifecycle. The app reads that snapshot and never guesses from session state.

Compatibility fallbacks are intentionally out of scope. A live session without
an exact transcript reports `unavailable`; it never guesses another session's
transcript or silently returns to state-gated polling.

## Architecture

```text
Current
  transcript watcher
    -> private path + raw byte offset
    -> provider watcher behavior
    -> separate assistant-content extraction + dedup
    -> invalidation

  session_messages_get
    -> independently guess transcript path
    -> independently scan JSONL from byte zero
    -> independently identify assistant messages

Target
  liveTranscript (one per live session)
    -> exact discovery lifecycle: discovering | ready | unavailable
    -> transcript follower: complete records + canonical events
    -> existing provider watcher behavior from each raw record
    -> bounded assistant window from the same canonical events
    -> compactable invalidation only when that window changes

  session_messages_get
    -> liveTranscript.assistantWindow()

  AnnotatedTerminal
    -> fetch on mount, invalidation, and reconnect
    -> render explicit window status
    -> existing transcript-to-grid alignment
```

The transcript follower is a concrete filesystem module. There is no injected
port: only one implementation exists. Its record decoder is shared with
`ReadEventPage`; the paged reader and follower keep only their distinct I/O
modes. CLI transcript inspection, activity, classification, and annotations
therefore cannot acquire competing provider parsers or echo-dedup rules.

## Interfaces

```go
type AssistantWindowStatus string

const (
    AssistantWindowDiscovering AssistantWindowStatus = "discovering"
    AssistantWindowReady       AssistantWindowStatus = "ready"
    AssistantWindowUnavailable AssistantWindowStatus = "unavailable"
)

type AssistantWindow struct {
    Status    AssistantWindowStatus
    Messages  []AssistantMessage
    Truncated bool
    Detail    string
}

func (d *Daemon) assistantWindow(sessionID string) AssistantWindow
```

```typespec
enum SessionMessageWindowStatus {
  discovering,
  ready,
  unavailable,
}

model SessionMessagesGetResultMessage {
  event: "session_messages_get_result";
  request_id: string;
  session_id: string;
  success: boolean;
  error?: string;
  status: SessionMessageWindowStatus;
  messages: SessionMessage[];
  truncated: boolean;
  detail?: string;
}
```

`success` remains the request/result transport outcome. `status` is the domain
outcome of a successful query. `unavailable` is therefore not encoded as a
failed command.

## Implementation

- [x] Extract one incremental transcript follower used by the live watcher and
      backed by the canonical event decoder.
- [x] Move exact path and discovery lifecycle into the per-session live
      transcript authority; restore that authority for surviving sessions.
- [x] Build a rolling assistant window from canonical assistant events and use
      their cursors as stable message keys.
- [x] Publish a compactable `session.assistant_window.changed` fact only when
      the canonical window changes.
- [x] Replace `pending` with the explicit status enum in TypeSpec, generated
      types, daemon handler, socket correlation, and terminal UI.
- [x] Delete broad annotation-time cwd discovery, the full-file recent-message
      reader, content-hash identities, watcher-existence inference, optional
      invalidation delivery, and state-gated refresh compatibility.
- [x] Prove same-cwd Codex isolation, paired-event dedup, distinct identical
      messages, partial records, transcript replacement, lifecycle transitions,
      reconnect, and annotation while a tool remains observably in flight.
- [x] Run full Go/frontend suites, Linux daemon cross-builds, protocol preflight,
      and the packaged annotation scenario in an isolated profile.

## Decisions

- The live transcript is derived in memory and rebuilt after daemon restart;
  no database cache or migration is needed.
- Resume ID and watcher launch identity are exact identities, not compatibility
  fallbacks. Broad cwd/newest discovery is forbidden for this feature.
- Disabling transcript watching makes live annotation unavailable. The frontend
  does not retain a polling or settled-state fallback.
- WebSocket reconnect revalidation and last-request-wins ordering remain: they
  close real message-loss and response-order races rather than preserve an old
  implementation.
- Transcript-to-grid alignment recognizes Codex's assistant marker and prefers
  that marked occurrence when a prompt quotes its requested response verbatim.

## Verification

- `go test ./... -count=1`
- `pnpm --dir app test` — 237 files, 2683 passed, 15 skipped
- `pnpm --dir app exec tsc --noEmit`
- `make build-linux-amd64` and `make build-linux-arm64`
- Bundled preflight for profile `rowdy-opossum` — Codex `gpt-5.6-sol` with high
  effort
- Packaged `TERMINAL-ANNOTATIONS` scenario — passed with the tool externally
  held until the annotation was filed, then covered persistence, relaunch,
  reprojection, a later turn, and submission
