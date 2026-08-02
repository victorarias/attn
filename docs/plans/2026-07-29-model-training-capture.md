# Plan: Local model-training capture

## Why / Alignment

attn needs a multi-day corpus of what Codex and Claude actually show so local
models can be evaluated and, only where the corpus proves it useful, fine-tuned
for busy/not-busy decisions and concise summaries.

This chunk ships an opt-in recorder that is disabled by default. When enabled it
stores exact visible viewport text locally under the active profile with private
permissions. It captures state changes promptly and changed frames at a
configurable cadence (10 seconds by default), deduplicates unchanged viewports,
and bounds storage with a configurable 5 GB oldest-first cap. Settings explains
that terminal content may contain source code, conversations, and secrets.
Disabling collection stops new writes but preserves the existing corpus.

In scope: local Codex and Claude sessions, the capture format and retention,
Settings controls, automated coverage, and live non-production verification.
Deferred: remote endpoint sessions, dataset review/label correction, export,
redaction, model inference, and fine-tuning.

## Architecture Map

```text
PTY worker / embedded Session
  -> authoritative Ghostty terminal under replayMu
    -> SnapshotInfo { styled viewport, plain viewport text, last_seq }
      -> daemon modelCaptureRecorder (enabled setting)
        -> filter Codex/Claude
        -> state-change / interval eligibility
        -> viewport hash dedupe
        -> private hourly JSONL under <data-root>/model-captures
        -> oldest-file retention sweep

SettingsModal
  -> generic set_setting
    -> validated model_capture.* settings
      -> recorder observes the new values without restarting sessions
```

Tests use real Ghostty snapshot parity where supported, worker wire round-trips,
a fake snapshot provider for deterministic recorder passes, and SettingsModal
interaction tests.

## Data Model / Interfaces

```go
type ViewportSnapshot struct {
    Payload []byte // existing styled VT
    Text    string // new plain visible viewport
    Cols, Rows uint16
}

type modelCaptureRecord struct {
    SchemaVersion int
    CapturedAt time.Time
    CaptureReason "state_change" | "interval"
    SessionKey string          // stable one-way hash, not the runtime id
    Agent string
    DaemonState string         // observed metadata, not trusted ground truth
    Running bool
    Cols, Rows uint16
    LastSeq uint32
    ViewportSHA256 string
    ViewportText string
}
```

Settings:

- `model_capture.enabled`: boolean, effective default `false`
- `model_capture.interval_seconds`: integer, effective default `10`
- `model_capture.max_gb`: integer, effective default `5`
- `model_capture.path` and `model_capture.bytes`: read-only effective status

## Boundaries

- The worker-owned Ghostty terminal produces plain text; the frontend never
  derives training data from pixels or its own render model.
- The daemon owns sampling, labels-as-observations, persistence, and retention
  because it has both session metadata and access to every local worker.
- The public WebSocket protocol carries only generic settings. Viewport text
  stays on the internal worker RPC and local disk path.
- Capture never records PTY input or raw output streams separately; only the
  visible viewport at the observation point is persisted.

## Implementation Steps

- [x] Carry plain viewport text through embedded and worker snapshot paths.
- [x] Add the daemon recorder, private JSONL writer, dedupe, and retention.
- [x] Validate and normalize capture settings; start the loop with the daemon.
- [x] Add the Settings section, warning, toggle, interval, cap, and status.
- [x] Cover worker/daemon/UI behavior with useful automated tests.
- [x] Install the dev app and verify enable, live capture, disable, and restart.

## Decisions

- Store exact local viewport text rather than best-effort redaction; partial
  redaction is neither a privacy guarantee nor faithful model input.
- Treat daemon state as provenance, not a final training label; observed state
  can lag the terminal.
- Rotate hourly, and segment the active hour before an append would exceed
  the configured cap, so retention can prune complete JSONL files without
  rewriting them.
- Keep existing captures when disabled; deletion/export/review are separate,
  explicit operations.

## Follow-ups

- Build a local review tool that promotes corrected examples into train/eval
  splits without mutating the raw observations.
- Add remote capture only with an explicit endpoint-aware privacy design.
- Evaluate the prompted 4B baseline before choosing LoRA or distillation.
