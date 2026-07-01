# Plan: Test the daemon through its production seam

From the 2026-07-01 architecture review. Line references are as of commit
`80c62f6b` — re-anchor by symbol name.

## Goal

The dominant daemon test pattern bypasses the production interface: construct a
partial `Daemon` (or `NewForTesting`), fabricate a `&wsClient{send: make(chan
outboundMessage, N)}`, call `d.handleClientMessage(client, data)` directly, and
read `client.send`. Consequences:

- The pre-dispatch logic in `handleWS`/`handleClientMessage` (capability gate
  ~websocket.go:833, recovery gate ~842, selection capture, remote routing) is
  untested on the common path.
- Tests must **guess how many broadcasts a command emits** to size the channel
  (buffers of 4/8/64 across ~40 sites) or the handler deadlocks — implementation
  choreography leaking into every test.
- The documented harness (`testharness.go`: `TestHarnessBuilder`,
  `FakeClassifier`, `BroadcastRecorder`) is used by exactly one file, and six
  bespoke PTY-backend fakes (daemon_test.go ~586/618/644/1555/1617,
  reload_test.go ~37) re-implement the 9-method `ptybackend.Backend` by hand.

Deepen the harness so tests cross the same seam production traffic does:
`send(cmd) → events`. The interface is the test surface. This is also the safety
net the other daemon refactor plans (session-state door, command registry) need.

## Architecture Map

```text
Current (dominant):
test -> &wsClient{send: chan(guess)} -> d.handleClientMessage(...)  // gates skipped
test -> d.handleXxxWS(client, msg)                                  // dispatch skipped

Already exists (used by ~10 tests, e.g. the web-client smoke test ~daemon_test.go:3905):
real daemon Start() -> httpServer (ATTN_WS_PORT / d.httpServer ~daemon.go:1581)
  -> websocket.Dial("ws://127.0.0.1:<port>/ws") -> writeWS / waitForDaemonWebSocketEvent

Target (one harness, built FROM the existing pieces):
h := NewTestHarness(t)            // TestHarnessBuilder grows: real WS server on :0,
                                  // FakeClassifier, BroadcastRecorder, fake PTY backend
c := h.ConnectWS(t)               // real dial, real accept/read/dispatch path
c.Send(map[string]any{"cmd": ...})
evt := c.WaitFor(t, timeout, func(evt) bool)   // no channel sizing, ever
h.PTY.EmitOutput(sessionID, "...")             // scriptable in-memory backend
```

## Data Model / Interfaces

```go
// testharness.go additions
type TestHarness struct {           // existing fields stay
    ...
    PTY *FakePTYBackend             // replaces the six bespoke fakes
}
func (h *TestHarness) StartWS(t *testing.T) (port int)   // httpServer on 127.0.0.1:0
func (h *TestHarness) ConnectWS(t *testing.T) *WSTestClient

type WSTestClient struct{ ... }     // wraps the existing writeWS/waitFor helpers
func (c *WSTestClient) Send(t *testing.T, msg map[string]any)
func (c *WSTestClient) WaitFor(t *testing.T, d time.Duration, match func(map[string]any) bool) map[string]any
func (c *WSTestClient) Hello(t *testing.T)  // sendWorkspaceClientHello equivalent

// ptybackend (or a testing sub-package): one scriptable fake implementing Backend
type FakePTYBackend struct{ ... }   // Spawn/Attach/Input/Resize/Kill/Remove/SessionIDs/Recover/Shutdown
func (f *FakePTYBackend) EmitOutput(sessionID string, data []byte)  // drives subscribers
func (f *FakePTYBackend) EmitExit(sessionID string, code int)
func (f *FakePTYBackend) Inputs(sessionID string) [][]byte          // assertions
```

The generalized helpers already exist as free functions in `daemon_test.go`
(`writeWS`, `waitForDaemonWebSocketEvent`, `sendWorkspaceClientHello`,
`waitForPtyOutputContaining`) — move them into `testharness.go` and make the
smoke test the first consumer.

## Boundaries

- The harness owns: daemon construction (via the builder — one construction
  path, not per-test struct literals), server lifecycle (`t.Cleanup` shutdown),
  WS client plumbing, and the fake PTY backend.
- Tests own: commands sent, events asserted, fake-backend scripting. Tests must
  not size channels, construct `wsClient`, or call `handle*` methods directly.
- `FakePTYBackend` implements `ptybackend.Backend` exactly — if it needs a
  method the interface lacks, that is a finding, not a reason to type-assert.

## Implementation Steps

- [ ] PR 1 — harness core. Extend `TestHarnessBuilder`/`TestHarness` with
      `StartWS`/`ConnectWS`/`WSTestClient` (move + generalize the existing
      helpers). Port the web-client smoke test and 2-3 representative
      fake-`wsClient` tests (pick ones asserting broadcasts, e.g. a PR-action
      and a state-change test) to prove the shape. Builder gains
      `WithPTYBackend(b ptybackend.Backend)`.
- [ ] PR 2 — `FakePTYBackend`. Implement the scriptable fake; replace the six
      bespoke fakes with it (each keeps its test-specific scripting inline).
- [ ] PR 3+ — migration by adjacency, not big-bang: whenever a test file is
      touched for any reason, its tests move to the harness. New daemon tests
      MUST use the harness (add one line to AGENTS.md's testing notes saying so).
- [ ] Retire-or-adopt check: after PR 1-2, `TestHarness` is the single documented
      daemon test API; delete any builder options nothing uses.

## Decisions

- Build on the existing real-dial path rather than an in-memory
  `net.Pipe`-based transport: the HTTP server on `127.0.0.1:0` is already proven
  by ~10 tests, needs no new seam in production code, and tests the real
  accept/read loop. Speed is acceptable (these are integration-grade tests).
- No big-bang migration of `daemon_test.go` (5,840 lines): the two patterns
  coexist; the harness must win by being easier to use.
- The fake backend lives where daemon tests can import it without an import
  cycle — `ptybackend` (exported, `testing`-tagged file) preferred so
  `ptybackend`'s own tests can reuse it.

## Verification

```bash
go build ./...
go test ./internal/daemon -count=1        # no whole-package -race (known
                                          # TestGitStatusScheduler race; use -run to scope)
go test ./internal/ptybackend -count=1
go vet ./internal/daemon
```

Structural asserts (end state of PR 2):

- Ported tests contain no `&wsClient{` literals:
  `grep -c '&wsClient{' internal/daemon/daemon_test.go` strictly decreases per
  migration PR; new test files -> 0.
- `grep -rn 'ptybackend.Backend' internal/daemon/*_test.go` shows only
  `FakePTYBackend` (no bespoke struct fakes).

Behaviors that must survive / be newly covered:

1. Every ported test keeps its original assertions — port, don't weaken.
2. New coverage that was impossible before: a test that an unknown `cmd` is
   handled per the default arm; a test that the recovery gate rejects commands
   pre-recovery; a test that a slow client hitting the 256-message buffer
   behaves as documented (AGENTS.md Communication section).
3. Harness startup/shutdown leak-free under `go test -count=5` (t.Cleanup order).

## Follow-ups

- Once the command-registry candidate (review card #5) lands, the harness is the
  regression net proving dispatch equivalence arm-by-arm.
